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
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// configPollInterval is the normal interval between config bundle fetches.
const configPollInterval = 5 * time.Minute

// maxConfigBundleBytes caps the config bundle body the poller will read
// (4 MiB). A real bundle is a few KiB; the cap keeps a misbehaving or hostile
// endpoint from making the harness buffer an unbounded body before the
// signature check can reject it. Over the cap → ErrConfigBundleTooLarge.
const maxConfigBundleBytes int64 = 4 << 20

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

// ConfigItemApplier is the optional extension a ConfigApplier implements to
// report per-mandated-item statuses for the ACK (WP02). When the applier
// implements it the poller calls ApplyBundleItems INSTEAD of ApplyBundle.
type ConfigItemApplier interface {
	ApplyBundleItems(ctx context.Context, b *Bundle) ([]error, []MandatedItemStatus)
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
	// SigningKeyUnknown is true when the most recent bundle was rejected with
	// ErrSigningKeyUnknown: its signed key_id names no key pinned in this
	// build (the install's pins predate fleet's current signing key). Settings
	// FleetHealth projects it as ConfigSource "unknown-key", parallel to
	// "no-key" for ErrSigningKeyNotConfigured. Cleared by the next poll that
	// judges a served bundle differently (a verified bundle, a 304, or a
	// different hard rejection); transient fetch errors preserve it.
	SigningKeyUnknown bool `json:"signingKeyUnknown"`
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
	keyUnknown    bool // last rejection was ErrSigningKeyUnknown

	// buildVersion is this binary's version (SetBuildVersion). With
	// reapplyID it implements review F1's re-apply rule: when the build
	// changed since the last applied bundle, or that bundle had REFUSED
	// mandated items (a kind this build could not install), the stored
	// checksum is cleared and the SAME bundle id may be applied once more,
	// so an upgraded build installs a previously refused pack without
	// waiting for a new bundle. reapplyID is that bundle id (0 = none).
	buildVersion string
	reapplyID    int64

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
	}
	return p
}

// SetBuildVersion records this binary's version for the re-apply rule (see
// ConfigPoller.buildVersion). Call before Start.
func (p *ConfigPoller) SetBuildVersion(v string) {
	p.mu.Lock()
	p.buildVersion = v
	p.mu.Unlock()
}

// Start launches the background polling goroutine. Loads the cached bundle
// state (lastAppliedID + checksum) before the first fetch.
func (p *ConfigPoller) Start(ctx context.Context) {
	// Allocated here rather than in the constructor for the same reason as
	// CapabilityPoller.Start: it is what lets Stop distinguish
	// never-started from running instead of blocking forever.
	p.done = make(chan struct{})

	// Restore state from disk cache.
	if id, cs, err := loadBundleState(p.dataDir); err == nil {
		p.mu.Lock()
		p.lastAppliedID = id
		p.checksum = cs
		if id > 0 {
			p.source = "cache"
			meta := loadBundleApplyMeta(p.dataDir)
			if meta.BuildVersion != p.buildVersion || meta.HadRefusals {
				// F1: re-fetch (no 304) and allow bundle id to apply once more.
				p.checksum = ""
				p.reapplyID = id
			}
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
		// Recover panics from the polling loop (FR-003). The goroutine logs
		// the panic and exits cleanly rather than crashing the process.
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				logging.L().Error("fleet.config_poller.panic",
					"panic", fmt.Sprintf("%v", r),
					"stack", string(stack),
				)
			}
		}()

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
	// Same latent deadlock CapabilityPoller.Stop carried: done was allocated
	// in the constructor, so a never-started poller blocked here forever.
	// Unreachable today only because SetFleetClient's testing.Testing() guard
	// wraps the whole ConfigPoller block and leaves the field nil, so Stop is
	// never called on an unstarted one. Fixed rather than left to the next
	// person who guards it the other way.
	if p.done != nil {
		<-p.done
	}
}

// Status returns a snapshot of the poller's current state.
func (p *ConfigPoller) Status() ConfigPollStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	s := ConfigPollStatus{
		LastAppliedID:     p.lastAppliedID,
		LastError:         p.lastError,
		Source:            p.source,
		BundleChecksum:    p.checksum,
		SigningKeyUnknown: p.keyUnknown,
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

	urlPath := fmt.Sprintf("/api/v1/configs?machine=%s&checksum=%s", url.QueryEscape(nodeID), url.QueryEscape(cs))

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
	// Drain-then-close keeps the connection reusable — except after an
	// over-cap body, where draining would mean reading an unbounded hostile
	// stream to the end; there we close without draining.
	drain := true
	defer func() {
		if drain {
			_, _ = io.Copy(io.Discard, resp.Body)
		}
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

	// Bounded read: one byte past the cap distinguishes "exactly at the
	// limit" from "over it" without buffering an unbounded body.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxConfigBundleBytes+1))
	if err != nil {
		e := fmt.Errorf("fleet: read config body: %w", err)
		p.setError(e.Error())
		return e
	}
	if int64(len(body)) > maxConfigBundleBytes {
		drain = false
		e := fmt.Errorf("%w: body exceeds %d bytes — rejected unparsed, nothing applied", ErrConfigBundleTooLarge, maxConfigBundleBytes)
		p.setRejection(e.Error())
		return e
	}

	var b Bundle
	if err := json.Unmarshal(body, &b); err != nil {
		e := fmt.Errorf("fleet: parse config bundle: %w", err)
		p.setRejection(e.Error())
		return e
	}

	// Signature verification using the accept-set (supports key rotation).
	keys := FleetSigningKeys()
	p.mu.RLock()
	lastID := p.lastAppliedID
	if p.reapplyID > 0 && p.reapplyID == lastID {
		// F1 re-apply: the already-applied bundle may verify once more
		// (a replay of a bundle this device already accepted is harmless).
		lastID = p.reapplyID - 1
	}
	p.mu.RUnlock()

	if err := VerifyWithKeySet(&b, keys, lastID); err != nil {
		// Hard-reject: do NOT apply; do NOT advance bundle_id; DO set error.
		// Error text and the unknown-key flag are set under ONE lock so a
		// concurrent Status() never sees one without the other.
		p.mu.Lock()
		p.lastError = fmt.Sprintf("bundle verification failed (hard-reject): %v", err)
		p.keyUnknown = errors.Is(err, ErrSigningKeyUnknown)
		p.mu.Unlock()
		return err // also triggers backoff
	}

	// Apply the bundle through the registered applier.
	// FR-012: applier returns ALL per-section errors so the ACK can carry them.
	var (
		applyErrs []error
		itemStats []MandatedItemStatus
	)
	if ia, ok := p.applier.(ConfigItemApplier); ok {
		applyErrs, itemStats = ia.ApplyBundleItems(ctx, &b)
	} else {
		applyErrs = p.applier.ApplyBundle(ctx, &b)
	}

	// Compute new checksum of the raw body (for 304 on next poll).
	newChecksum := hexChecksumOf(body)

	// FR-012: only advance lastAppliedID when the apply is fully clean.
	// On partial failure the ID stays at the previous value so the bundle is
	// re-attempted on the next poll. The checksum is also NOT advanced so
	// the next GET returns the same bundle (no 304 short-circuit).
	p.mu.Lock()
	p.keyUnknown = false // the bundle verified under a pinned key
	hadRefusals := false
	for _, st := range itemStats {
		if st.Status == MandatedStatusRefused {
			hadRefusals = true
		}
	}
	buildVersion := p.buildVersion
	if len(applyErrs) == 0 {
		p.lastAppliedID = b.BundleID
		p.lastAppliedAt = time.Now()
		p.checksum = newChecksum
		p.lastError = ""
		p.reapplyID = 0
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
		if saveErr := saveBundleApplyMeta(p.dataDir, bundleApplyMeta{BuildVersion: buildVersion, HadRefusals: hadRefusals}); saveErr != nil {
			log.Printf("fleet: save bundle apply meta: %v", saveErr)
		}
	}

	// ACK back to fleet (best-effort; errors are logged, not propagated).
	// Use a short-lived background context so ACK always completes even if
	// the caller's context is cancelled around the same time (e.g. Stop()).
	ackCtx, ackCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer ackCancel()
	machineID, _ := NodeID(p.dataDir) // the same id the pull's ?machine= sends
	if ackErr := PostConfigACK(ackCtx, p.client, b.BundleID, applyErrs, ConfigACKReport{MachineID: machineID, Items: itemStats}); ackErr != nil {
		log.Printf("fleet: config ack: %v", ackErr)
	}

	if len(applyErrs) > 0 {
		return applyErrs[0] // triggers backoff; all errors already in lastError
	}
	return nil
}

// setError records a TRANSIENT failure (fetch, non-200 status, body read).
// It deliberately leaves keyUnknown alone: a network blip says nothing about
// which key the served bundle is signed with, so an "unknown-key" state must
// not flap off and back on across retries. keyUnknown changes only when a
// bundle body is actually judged — setRejection, the verification path, a
// verified bundle, or a 304 (clearError).
func (p *ConfigPoller) setError(msg string) {
	p.mu.Lock()
	p.lastError = msg
	p.mu.Unlock()
}

// setRejection records a hard rejection of a served bundle body for a reason
// OTHER than an unknown signing key (oversized, unparseable): the served
// bundle is no longer the unknown-key one, so the flag clears.
func (p *ConfigPoller) setRejection(msg string) {
	p.mu.Lock()
	p.lastError = msg
	p.keyUnknown = false
	p.mu.Unlock()
}

func (p *ConfigPoller) clearError() {
	p.mu.Lock()
	p.lastError = ""
	p.keyUnknown = false
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

// bundleApplyMeta records, for the last fully applied bundle, the build
// that applied it and whether it carried refused mandated items (F1).
type bundleApplyMeta struct {
	BuildVersion string `json:"build_version"`
	HadRefusals  bool   `json:"had_refusals"`
}

func bundleApplyMetaPath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "bundle_apply_meta.json")
}

// loadBundleApplyMeta returns the zero meta when absent/unreadable — which
// reads as "applied by a different (older) build", triggering one re-apply.
func loadBundleApplyMeta(dataDir string) bundleApplyMeta {
	var m bundleApplyMeta
	if dataDir == "" {
		return m
	}
	if raw, err := os.ReadFile(bundleApplyMetaPath(dataDir)); err == nil {
		_ = json.Unmarshal(raw, &m)
	}
	return m
}

func saveBundleApplyMeta(dataDir string, m bundleApplyMeta) error {
	if dataDir == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "fleet"), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return atomicWriteFile(bundleApplyMetaPath(dataDir), string(raw)+"\n")
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
