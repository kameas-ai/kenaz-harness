package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// lockdownActive is the package-level flag that records whether an emergency
// lockdown is currently in effect. It is updated by Watcher and read by the
// RPC middleware without any allocation.
var lockdownActive atomic.Bool

// LockdownActive returns true when a fleet-issued emergency lockdown is
// currently in effect. Safe to call from any goroutine.
func LockdownActive() bool {
	return lockdownActive.Load()
}

// ForceSetLockdownForTest directly sets the lockdownActive flag. It is
// exported for use in tests outside the fleet package (e.g. middleware tests)
// that need to control the flag without running a Watcher.
// MUST NOT be called from production code.
func ForceSetLockdownForTest(active bool) {
	lockdownActive.Store(active)
}

// lockdownBackoffSteps are the retry intervals on network failure:
// 1 s → 5 s → 30 s (capped at the last step).
var lockdownBackoffSteps = []time.Duration{
	1 * time.Second,
	5 * time.Second,
	30 * time.Second,
}

// lockdownPollTimeout is the server-side long-poll timeout. The fleet
// endpoint holds the connection for up to 60 s and returns 204 when no
// signal fires in that window.
const lockdownPollTimeout = 65 * time.Second // client timeout slightly > server 60 s

// LockdownStatus is the response body for both the long-poll
// (GET /api/v1/lockdown/wait) and the one-shot status check
// (GET /api/v1/lockdown/status).
type LockdownStatus struct {
	Lockdown bool   `json:"lockdown"`
	Reason   string `json:"reason,omitempty"`
}

// BrokerSink is the interface the Watcher uses to publish lockdown state
// changes to the frontend via the Wails event broker. The only concrete
// implementation is the stream-broker adapter in core/rpc; tests inject a fake.
type BrokerSink interface {
	// Emit publishes topic+payload to the Wails event system.
	Emit(topic string, payload any)
}

// LockdownChangedPayload is the JSON shape emitted on TopicFleetLockdownChanged
// when the lockdown state transitions. The frontend's LockdownBanner and
// SessionsView.vue subscribe via useEventStream.
type LockdownChangedPayload struct {
	Active bool   `json:"active"`
	Reason string `json:"reason,omitempty"`
}

// TopicFleetLockdownChanged is the Wails broker topic name emitted by the
// Watcher whenever the lockdown flag flips. Declared here next to the
// payload type; stream_broker.go re-exports it for the frontend contract.
const TopicFleetLockdownChanged = "fleet:lockdown:changed"

// lockdownBackoff tracks retry cadence on network failure.
type lockdownBackoff struct {
	step int
}

func (b *lockdownBackoff) next() time.Duration {
	if b.step >= len(lockdownBackoffSteps) {
		b.step = len(lockdownBackoffSteps)
	}
	d := lockdownBackoffSteps[b.step]
	if b.step < len(lockdownBackoffSteps)-1 {
		b.step++
	}
	return d
}

func (b *lockdownBackoff) reset() { b.step = 0 }

// Watcher polls the fleet lockdown long-poll endpoint and keeps the
// package-level lockdownActive flag up-to-date. It also emits
// TopicFleetLockdownChanged broker events on every state transition so
// the frontend banner can update in real-time.
//
// Lifecycle:
//
//	w := NewWatcher(client, poller, broker)
//	w.Start(ctx)   // launch background goroutine
//	w.Stop()       // clean shutdown
type Watcher struct {
	client  *Client
	poller  *CapabilityPoller
	broker  BrokerSink
	backoff *lockdownBackoff

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewWatcher constructs a Watcher. The poller is used to gate the watcher
// behind CapEmergencyLockdown. broker may be nil in which case state
// transitions update the flag but no Wails events are emitted.
func NewWatcher(client *Client, poller *CapabilityPoller, broker BrokerSink) *Watcher {
	return &Watcher{
		client:  client,
		poller:  poller,
		broker:  broker,
		backoff: &lockdownBackoff{},
		done:    make(chan struct{}),
	}
}

// SetBroker updates the BrokerSink used for event emission. Safe to call
// while the watcher is running; the next state-change emission will use
// the new sink.
func (w *Watcher) SetBroker(sink BrokerSink) {
	w.mu.Lock()
	w.broker = sink
	w.mu.Unlock()
}

// Start launches the long-poll loop. It checks CapEmergencyLockdown before
// doing anything; if the capability is absent the watcher exits immediately
// and locks the flag to false. Calling Start a second time is a no-op if
// already running. It is safe to call after Stop (it re-creates the done chan).
func (w *Watcher) Start(ctx context.Context) {
	w.mu.Lock()
	// Re-create done channel if we were previously stopped.
	select {
	case <-w.done:
		w.done = make(chan struct{})
	default:
	}
	if w.cancel != nil {
		// Already running; do nothing.
		w.mu.Unlock()
		return
	}
	inner, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	w.mu.Unlock()

	go w.run(inner)
}

// Stop cancels the background goroutine and waits for it to exit.
func (w *Watcher) Stop() {
	w.mu.Lock()
	cancel := w.cancel
	w.cancel = nil
	w.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	<-w.done
}

// run is the goroutine body. It must close w.done when it exits.
func (w *Watcher) run(ctx context.Context) {
	defer close(w.done)

	// Capability gate: only watch when the fleet tier has CapEmergencyLockdown.
	if !w.capEnabled() {
		log.Printf("fleet.lockdown: CapEmergencyLockdown not enabled; watcher idle")
		return
	}

	for {
		if ctx.Err() != nil {
			return
		}

		status, err := w.poll(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			wait := w.backoff.next()
			log.Printf("fleet.lockdown: poll error: %v; retrying in %s", err, wait)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return
			}
			continue
		}

		w.backoff.reset()

		// 204 = timeout on the server side (no event in 60 s); reconnect immediately.
		if status == nil {
			continue
		}

		// State-change: update flag and emit broker event.
		prev := lockdownActive.Swap(status.Lockdown)
		if prev != status.Lockdown {
			w.emit(status.Lockdown, status.Reason)
			log.Printf("fleet.lockdown: state changed lockdown=%v reason=%q", status.Lockdown, status.Reason)
		}
	}
}

// poll performs one GET /api/v1/lockdown/wait. Returns:
//   - (status, nil)   on 200 with a body
//   - (nil, nil)      on 204 (timeout; reconnect immediately)
//   - (nil, err)      on network or parse error (triggers backoff)
func (w *Watcher) poll(ctx context.Context) (*LockdownStatus, error) {
	if w.client == nil || w.client.isNop {
		return nil, ErrFleetDisabled
	}

	// Long-poll timeout: slightly longer than the server's 60 s hold.
	pollCtx, cancel := context.WithTimeout(ctx, lockdownPollTimeout)
	defer cancel()

	resp, err := w.client.Get(pollCtx, "/api/v1/lockdown/wait")
	if err != nil {
		return nil, fmt.Errorf("fleet.lockdown: GET /lockdown/wait: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusNoContent {
		// 204 → server timeout; reconnect.
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fleet.lockdown: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fleet.lockdown: read body: %w", err)
	}

	var status LockdownStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("fleet.lockdown: parse body: %w", err)
	}
	return &status, nil
}

// capEnabled checks whether CapEmergencyLockdown is set in the current
// capabilities snapshot. Returns false when the poller is nil or the
// capability is missing / stale.
func (w *Watcher) capEnabled() bool {
	if w.poller == nil {
		return false
	}
	caps := w.poller.Current()
	return caps.Has(CapEmergencyLockdown)
}

// emit publishes a LockdownChangedPayload on the fleet:lockdown:changed broker
// topic. No-op when broker is nil (test / fleet-disabled paths).
func (w *Watcher) emit(active bool, reason string) {
	if w.broker == nil {
		return
	}
	w.broker.Emit(TopicFleetLockdownChanged, LockdownChangedPayload{
		Active: active,
		Reason: reason,
	})
}

// BootstrapLockdownStatus performs a one-shot GET /api/v1/lockdown/status
// against the fleet server and sets the lockdownActive flag immediately.
// This must be called before any user-facing surface mounts so the harness
// boots into locked state when the fleet says so.
//
// Errors are logged and swallowed (fail-open on connectivity issues; the
// long-poll Watcher will catch up within its first reconnect cycle).
func BootstrapLockdownStatus(ctx context.Context, client *Client) {
	if client == nil || client.isNop {
		return
	}
	resp, err := client.Get(ctx, "/api/v1/lockdown/status")
	if err != nil {
		log.Printf("fleet.lockdown: bootstrap status check: %v", err)
		return
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusNotFound {
		// Endpoint not yet deployed on this fleet version.
		log.Printf("fleet.lockdown: /lockdown/status not available (404); skipping bootstrap")
		return
	}
	if resp.StatusCode != http.StatusOK {
		log.Printf("fleet.lockdown: bootstrap status check returned %d", resp.StatusCode)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("fleet.lockdown: bootstrap read body: %v", err)
		return
	}

	var status LockdownStatus
	if err := json.Unmarshal(body, &status); err != nil {
		log.Printf("fleet.lockdown: bootstrap parse body: %v", err)
		return
	}

	prev := lockdownActive.Swap(status.Lockdown)
	if prev != status.Lockdown {
		log.Printf("fleet.lockdown: bootstrap set lockdown=%v reason=%q", status.Lockdown, status.Reason)
	}
}
