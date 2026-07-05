// Package fleet — config_pull.go
//
// ConfigPoller polls GET /api/v1/configs every 5 minutes while the user is
// signed in, verifies ed25519 signatures, caches bundles to disk, and drives
// the apply pipeline (cedar, mcp, model prefs, weight URLs, ack).
//
// Key behaviours:
//   - 304 Not Modified → no apply (checksum-gated via query param)
//   - 200 → verify signature → apply each section → ACK → persist
//   - Signature failure → hard-reject; previous applied bundle is kept
//   - Backoff on error: 5 / 15 / 60 min ceiling
//   - Disk cache: <DataDir>/fleet/bundle.json + bundle_checksum.txt
//
// (fleet-config-pull-01NDFSEX10 WP02)
package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// configPollInterval is the normal interval between config bundle fetches.
const configPollInterval = 5 * time.Minute

// configBackoffSteps mirrors capability_poller.go: 5, 15, 60 minutes.
var configBackoffSteps = []time.Duration{5 * time.Minute, 15 * time.Minute, 60 * time.Minute}

// ConfigApplier is the callback invoked after a bundle passes signature
// verification. Each section that is non-nil/non-empty is applied in order.
// All per-section errors are returned so the ACK can carry the full list
// (FR-012). A non-empty return value means the bundle was partially applied;
// the caller will NOT advance lastAppliedID so the bundle is retried next poll.
type ConfigApplier interface {
	ApplyBundle(ctx context.Context, b *Bundle) []error
}

// ConfigPollStatus is the wire shape returned to the frontend via the
// ConfigPullStatus RPC. It is a snapshot of the poller's last state.
type ConfigPollStatus struct {
	// LastAppliedID is the bundle_id of the last successfully applied bundle, or
	// 0 if no bundle has been applied since the harness started.
	LastAppliedID int64 `json:"lastAppliedId"`
	// LastAppliedAt is the RFC3339 timestamp of the last apply, or empty string.
	LastAppliedAt string `json:"lastAppliedAt"`
	// LastError is the most recent error string, or empty when the last fetch/apply
	// succeeded.
	LastError string `json:"lastError"`
	// Source is "fleet", "cache", or "default-deny".
	Source string `json:"source"`
	// BundleChecksum is the last-seen bundle checksum (SHA-256, hex), used for
	// 304 Not-Modified gating on the next poll.
	BundleChecksum string `json:"bundleChecksum"`
}

// ConfigPoller polls the fleet config endpoint, verifies bundles, and drives
// the apply pipeline. Lifecycle mirrors CapabilityPoller.
type ConfigPoller struct {
	client   *Client
	dataDir  string
	interval time.Duration
	backoff  *configBackoffState
	applier  ConfigApplier

	mu            sync.RWMutex
	lastAppliedID int64
	lastAppliedAt time.Time
	lastError     string
	checksum      string // SHA-256 hex of last-seen bundle JSON (for 304)
	source        string

	cancel context.CancelFunc
	done   chan struct{}
}

type configBackoffState struct {
	step int
}

func (b *configBackoffState) next() time.Duration {
	if b.step >= len(configBackoffSteps) {
		b.step = len(configBackoffSteps)
	}
	d := configBackoffSteps[b.step]
	if b.step < len(configBackoffSteps)-1 {
		b.step++
	}
	// Add ±10% jitter so multiple instances don't thunderherd after an outage.
	return addJitter(d, 0.10)
}

func (b *configBackoffState) reset() { b.step = 0 }

// addJitter returns d ± (jitterFrac * d) where the offset is uniformly random.
// jitterFrac should be in [0, 1). This is a package-level helper shared by
// all fleet pollers.
func addJitter(d time.Duration, jitterFrac float64) time.Duration {
	// rand.Float64 returns [0.0, 1.0) — shift to [-0.5, 0.5) then scale.
	offset := time.Duration(float64(d) * jitterFrac * (rand.Float64()*2 - 1)) //nolint:gosec
	return d + offset
}

// NewConfigPoller constructs a ConfigPoller. Call Start(ctx) to launch the
// background goroutine.
func NewConfigPoller(client *Client, dataDir string, applier ConfigApplier) *ConfigPoller {
	p := &ConfigPoller{
		client:   client,
		dataDir:  dataDir,
		interval: configPollInterval,
		backoff:  &configBackoffState{},
		applier:  applier,
		source:   "default-deny",
		done:     make(chan struct{}),
	}
	return p
}

// Start launches the background polling goroutine. Loads the cached bundle
// state (lastAppliedID + checksum) before the first fetch.
func (p *ConfigPoller) Start(ctx context.Context) {
	// Restore state from disk cache.
	if id, cs, err := loadBundleState(p.dataDir); err == nil {
		p.mu.Lock()
		p.lastAppliedID = id
		p.checksum = cs
		if id > 0 {
			p.source = "cache"
		}
		p.mu.Unlock()
	}

	innerCtx, cancel := context.WithCancel(ctx)
	p.mu.Lock()
	p.cancel = cancel
	p.mu.Unlock()

	go func() {
		defer close(p.done)
		defer cancel()

		// Immediate first poll.
		if err := p.poll(innerCtx); err != nil {
			if innerCtx.Err() != nil {
				return
			}
			wait := p.backoff.next()
			select {
			case <-time.After(wait):
			case <-innerCtx.Done():
				return
			}
		} else {
			p.backoff.reset()
		}

		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()
		for {
			select {
			case <-innerCtx.Done():
				return
			case <-ticker.C:
				if err := p.poll(innerCtx); err != nil {
					if innerCtx.Err() != nil {
						return
					}
					wait := p.backoff.next()
					ticker.Reset(wait)
				} else {
					p.backoff.reset()
					ticker.Reset(p.interval)
				}
			}
		}
	}()
}

// Stop cancels the background goroutine and waits for it to exit.
func (p *ConfigPoller) Stop() {
	p.mu.RLock()
	cancel := p.cancel
	p.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
	<-p.done
}

// Status returns a snapshot of the poller's current state.
func (p *ConfigPoller) Status() ConfigPollStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	s := ConfigPollStatus{
		LastAppliedID:  p.lastAppliedID,
		LastError:      p.lastError,
		Source:         p.source,
		BundleChecksum: p.checksum,
	}
	if !p.lastAppliedAt.IsZero() {
		s.LastAppliedAt = p.lastAppliedAt.UTC().Format(time.RFC3339)
	}
	return s
}

// poll performs one round: fetch → verify → apply → ack.
// Returns nil on success or 304 (no-op). Non-nil error triggers backoff.
func (p *ConfigPoller) poll(ctx context.Context) error {
	if p.client == nil || p.client.isNop {
		return ErrFleetDisabled
	}

	// Build the URL with machine + checksum query params.
	nodeID, _ := NodeID(p.dataDir)
	p.mu.RLock()
	cs := p.checksum
	p.mu.RUnlock()

	urlPath := fmt.Sprintf("/api/v1/configs?machine=%s&checksum=%s", nodeID, cs)

	// We need to inspect the status code before letting the fleet http.Client
	// discard non-2xx bodies, so we call Get directly.
	resp, err := p.client.Get(ctx, urlPath)
	if err != nil {
		if errors.Is(err, ErrNotSignedIn) {
			// Not signed in — skip silently; don't backoff.
			return nil
		}
		p.setError(fmt.Sprintf("fetch config: %v", err))
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusNotModified {
		// 304 → our current bundle is still current.
		p.clearError()
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		e := fmt.Errorf("fleet: config status %d", resp.StatusCode)
		p.setError(e.Error())
		return e
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		e := fmt.Errorf("fleet: read config body: %w", err)
		p.setError(e.Error())
		return e
	}

	var b Bundle
	if err := json.Unmarshal(body, &b); err != nil {
		e := fmt.Errorf("fleet: parse config bundle: %w", err)
		p.setError(e.Error())
		return e
	}

	// Signature verification using the accept-set (supports key rotation).
	keys := FleetSigningKeys()
	p.mu.RLock()
	lastID := p.lastAppliedID
	p.mu.RUnlock()

	if err := VerifyWithKeySet(&b, keys, lastID); err != nil {
		// Hard-reject: do NOT apply; do NOT advance bundle_id; DO set error.
		p.setError(fmt.Sprintf("bundle verification failed (hard-reject): %v", err))
		return err // also triggers backoff
	}

	// Apply the bundle through the registered applier.
	// FR-012: applier returns ALL per-section errors so the ACK can carry them.
	applyErrs := p.applier.ApplyBundle(ctx, &b)

	// Compute new checksum of the raw body (for 304 on next poll).
	newChecksum := hexChecksumOf(body)

	// FR-012: only advance lastAppliedID when the apply is fully clean.
	// On partial failure the ID stays at the previous value so the bundle is
	// re-attempted on the next poll. The checksum is also NOT advanced so
	// the next GET returns the same bundle (no 304 short-circuit).
	p.mu.Lock()
	if len(applyErrs) == 0 {
		p.lastAppliedID = b.BundleID
		p.lastAppliedAt = time.Now()
		p.checksum = newChecksum
		p.lastError = ""
	} else {
		// Surface the error set but leave ID + checksum untouched.
		msgs := make([]string, 0, len(applyErrs))
		for _, e := range applyErrs {
			if e != nil {
				msgs = append(msgs, e.Error())
			}
		}
		p.lastError = fmt.Sprintf("partial apply failure: %d error(s): %v", len(msgs), msgs)
	}
	p.source = "fleet"
	p.mu.Unlock()

	// Persist state to disk only on full success.
	if len(applyErrs) == 0 {
		if saveErr := saveBundleState(p.dataDir, b.BundleID, newChecksum); saveErr != nil {
			log.Printf("fleet: save bundle state: %v", saveErr)
		}
	}

	// ACK back to fleet (best-effort; errors are logged, not propagated).
	// Use a short-lived background context so ACK always completes even if
	// the caller's context is cancelled around the same time (e.g. Stop()).
	ackCtx, ackCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer ackCancel()
	if ackErr := PostConfigACK(ackCtx, p.client, b.BundleID, applyErrs); ackErr != nil {
		log.Printf("fleet: config ack: %v", ackErr)
	}

	if len(applyErrs) > 0 {
		return applyErrs[0] // triggers backoff; all errors already in lastError
	}
	return nil
}

func (p *ConfigPoller) setError(msg string) {
	p.mu.Lock()
	p.lastError = msg
	p.mu.Unlock()
}

func (p *ConfigPoller) clearError() {
	p.mu.Lock()
	p.lastError = ""
	p.mu.Unlock()
}

// ── Disk state helpers ────────────────────────────────────────────────────────

// bundleIDPath returns the path to the persisted last-applied bundle_id.
func bundleIDPath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "bundle_id.txt")
}

// bundleChecksumPath returns the path to the last-seen bundle checksum.
func bundleChecksumPath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "bundle_checksum.txt")
}

// loadBundleState reads (lastAppliedID, checksum) from disk. Returns (0, "", nil)
// when files don't exist.
func loadBundleState(dataDir string) (int64, string, error) {
	if dataDir == "" {
		return 0, "", nil
	}
	var id int64
	idData, err := os.ReadFile(bundleIDPath(dataDir))
	if err == nil {
		_, _ = fmt.Sscanf(string(idData), "%d", &id)
	}
	var cs string
	csData, err := os.ReadFile(bundleChecksumPath(dataDir))
	if err == nil {
		cs = string(csData)
		// Trim possible newline.
		for len(cs) > 0 && (cs[len(cs)-1] == '\n' || cs[len(cs)-1] == '\r') {
			cs = cs[:len(cs)-1]
		}
	}
	return id, cs, nil
}

// saveBundleState atomically persists (lastAppliedID, checksum) to disk.
func saveBundleState(dataDir string, id int64, checksum string) error {
	if dataDir == "" {
		return nil
	}
	dir := filepath.Join(dataDir, "fleet")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("fleet: mkdir bundle state: %w", err)
	}
	if err := atomicWriteFile(bundleIDPath(dataDir), fmt.Sprintf("%d\n", id)); err != nil {
		return err
	}
	return atomicWriteFile(bundleChecksumPath(dataDir), checksum+"\n")
}

// atomicWriteFile writes content to path via tmp+rename.
func atomicWriteFile(path, content string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return fmt.Errorf("fleet: write %s tmp: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("fleet: rename %s: %w", path, err)
	}
	return nil
}

// hexChecksumOf computes the hex-encoded SHA-256 of b.
func hexChecksumOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
