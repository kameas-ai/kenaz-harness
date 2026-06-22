package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// pollInterval is the normal interval between capability refreshes.
const pollInterval = 5 * time.Minute

// staleCacheThreshold is the age at which a cached value is considered
// stale enough to trigger an immediate refresh on Start.
const staleCacheThreshold = 5 * time.Minute

// backoff steps used on consecutive errors: 5, 15, 60 minutes.
var backoffSteps = []time.Duration{5 * time.Minute, 15 * time.Minute, 60 * time.Minute}

// backoffState tracks the current backoff position.
type backoffState struct {
	step int
}

// next returns the next backoff duration and advances the step counter.
// Once the max step is reached it stays there.
func (b *backoffState) next() time.Duration {
	if b.step >= len(backoffSteps) {
		b.step = len(backoffSteps)
	}
	d := backoffSteps[b.step]
	if b.step < len(backoffSteps)-1 {
		b.step++
	}
	return d
}

// reset clears backoff after a successful fetch.
func (b *backoffState) reset() {
	b.step = 0
}

// CapabilityPoller polls GET /api/v1/me/capabilities on the fleet server at
// a configurable interval and keeps an in-memory snapshot of the latest
// Capabilities. Multiple concurrent callers collapse to a single HTTP request
// via singleflight.
//
// Lifecycle:
//
//	p := NewCapabilityPoller(client, dataDir)
//	p.Start(ctx)   // launch background goroutine
//	caps := p.Current()
//	p.Stop()       // clean shutdown
type CapabilityPoller struct {
	client   *Client
	dataDir  string
	interval time.Duration
	backoff  *backoffState
	sf       singleflight.Group

	mu      sync.RWMutex
	current Capabilities

	// cancel shuts down the background goroutine.
	cancel context.CancelFunc
	// done is closed when the goroutine exits (used by Stop to wait).
	done chan struct{}
}

// NewCapabilityPoller constructs a CapabilityPoller. The poller does NOT start
// automatically; call Start(ctx) to launch the background goroutine.
func NewCapabilityPoller(client *Client, dataDir string) *CapabilityPoller {
	return &CapabilityPoller{
		client:   client,
		dataDir:  dataDir,
		interval: pollInterval,
		backoff:  &backoffState{},
		done:     make(chan struct{}),
		// Initialize with default-deny so Current() is never nil.
		current: DefaultDenyCapabilities(),
	}
}

// Current returns a snapshot of the most recently fetched Capabilities.
// Returns the in-memory value; never blocks on network I/O.
func (p *CapabilityPoller) Current() Capabilities {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.current
}

// setCurrent replaces the in-memory snapshot.
func (p *CapabilityPoller) setCurrent(c Capabilities) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current = c
}

// Start launches the polling goroutine. If the cached snapshot is empty or
// stale (> staleCacheThreshold old), an immediate refresh is performed before
// sleeping. Subsequent polls use the normal interval; on error the interval
// expands via backoff.
//
// Calling Start multiple times is safe but will spawn duplicate goroutines;
// callers should only call Start once. Stop cancels the context passed here.
func (p *CapabilityPoller) Start(ctx context.Context) {
	innerCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel

	// Load the disk cache as the initial state so we have something to serve
	// before the first network fetch completes.
	if cached, err := LoadCapabilities(p.dataDir); err == nil && cached.Source == "cache" {
		p.setCurrent(cached)
	}

	go func() {
		defer close(p.done)
		defer cancel()

		// Decide whether to fetch immediately or wait.
		cur := p.Current()
		needsImmediate := cur.Source == "default-deny" ||
			cur.Source == "" ||
			(cur.Source == "cache" && time.Since(cur.FetchedAt) > staleCacheThreshold)

		if needsImmediate {
			caps, err := p.Refresh(innerCtx)
			if err != nil {
				if innerCtx.Err() != nil {
					return
				}
				// Use backoff for next attempt.
				wait := p.backoff.next()
				select {
				case <-time.After(wait):
				case <-innerCtx.Done():
					return
				}
			} else {
				p.backoff.reset()
				p.setCurrent(caps)
			}
		}

		// Regular poll loop.
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()
		for {
			select {
			case <-innerCtx.Done():
				return
			case <-ticker.C:
				caps, err := p.Refresh(innerCtx)
				if err != nil {
					if innerCtx.Err() != nil {
						return
					}
					wait := p.backoff.next()
					// Reset ticker to back-off delay.
					ticker.Reset(wait)
				} else {
					p.backoff.reset()
					p.setCurrent(caps)
					ticker.Reset(p.interval)
				}
			}
		}
	}()
}

// Stop cancels the background goroutine and waits for it to exit.
func (p *CapabilityPoller) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	<-p.done
}

// capabilitiesWireResponse is the JSON shape returned by
// GET /api/v1/me/capabilities.
type capabilitiesWireResponse struct {
	Tier         string          `json:"tier"`
	Capabilities map[string]bool `json:"capabilities"`
	FetchedAt    time.Time       `json:"fetched_at"`
}

// Refresh calls GET /api/v1/me/capabilities on the fleet server via
// singleflight so multiple concurrent callers share one HTTP request.
//
// Graceful degradation rules:
//   - 404 → "fleet capabilities endpoint not yet available"; returns default-deny + nil error
//   - 5xx / transport errors → returns last-known cached value + the error so
//     the caller can update backoff state
//   - On success → saves to disk cache and updates in-memory state
func (p *CapabilityPoller) Refresh(ctx context.Context) (Capabilities, error) {
	type result struct {
		caps Capabilities
		err  error
	}
	v, err, _ := p.sf.Do("capabilities", func() (interface{}, error) {
		caps, fetchErr := p.fetch(ctx)
		return result{caps: caps, err: fetchErr}, nil
	})
	if err != nil {
		// singleflight itself failed (shouldn't happen with our wrapper).
		return DefaultDenyCapabilities(), fmt.Errorf("fleet: capability poller singleflight: %w", err)
	}
	r := v.(result)
	// Publish the fetched snapshot so Current() reflects the refresh for
	// direct callers (Start's loop also calls setCurrent, harmlessly). On a
	// fetch error fetch() returns the last-known value, so we only update on
	// success to avoid clobbering good state with a transient failure.
	if r.err == nil {
		p.setCurrent(r.caps)
	}
	return r.caps, r.err
}

// fetch performs the actual HTTP GET. It is the singleflight body.
func (p *CapabilityPoller) fetch(ctx context.Context) (Capabilities, error) {
	if p.client == nil || p.client.isNop {
		return DefaultDenyCapabilities(), ErrFleetDisabled
	}

	resp, err := p.client.Get(ctx, "/api/v1/me/capabilities")
	if err != nil {
		// Return last-known value (don't overwrite current) + the error for backoff.
		return p.Current(), fmt.Errorf("fleet: fetch capabilities: %w", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		// Endpoint not yet implemented on the fleet server. Degrade gracefully.
		log.Printf("fleet: capabilities endpoint not yet available (404); using default-deny")
		d := DefaultDenyCapabilities()
		return d, nil
	}

	if resp.StatusCode != http.StatusOK {
		// Keep the last-known value; the error triggers backoff.
		return p.Current(), fmt.Errorf("fleet: capabilities status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return p.Current(), fmt.Errorf("fleet: read capabilities body: %w", err)
	}

	var wire capabilitiesWireResponse
	if err := json.Unmarshal(body, &wire); err != nil {
		return p.Current(), fmt.Errorf("fleet: parse capabilities: %w", err)
	}

	// Convert string-keyed wire map to Capability-keyed map.
	enabled := make(map[Capability]bool, len(wire.Capabilities))
	for k, v := range wire.Capabilities {
		enabled[Capability(k)] = v
	}

	caps := Capabilities{
		Tier:      wire.Tier,
		Enabled:   enabled,
		FetchedAt: wire.FetchedAt,
		Source:    "fleet",
	}
	if caps.FetchedAt.IsZero() {
		caps.FetchedAt = time.Now()
	}

	// Persist to disk cache.
	if saveErr := SaveCapabilities(p.dataDir, caps); saveErr != nil {
		log.Printf("fleet: save capabilities cache: %v", saveErr)
	}

	return caps, nil
}
