// Package fleet — config_pull_restore.go
//
// Restart persistence and sign-in wake for ConfigPoller (fleet-policy
// push-down, 2026-10-31).
//
// Before this, only bundle_id.txt / bundle_checksum.txt were persisted:
// after a restart the poller sent the old checksum, the server answered 304,
// and nothing was re-applied while the status read "fleet" — the in-memory
// Cedar rules and MCP allow-list a bundle installs were simply gone.
package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// bundleBodyPath is the last fully-applied bundle's raw signed body.
func bundleBodyPath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "bundle.json")
}

// saveBundleBody atomically persists the raw signed bundle body (0600).
func saveBundleBody(dataDir string, body []byte) error {
	if dataDir == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "fleet"), 0o700); err != nil {
		return fmt.Errorf("fleet: mkdir bundle cache: %w", err)
	}
	return atomicWriteFile(bundleBodyPath(dataDir), string(body))
}

// restoreCachedBundle re-verifies and re-applies the persisted bundle. It
// runs once, on the poller goroutine, before the first poll.
//
//   - no persisted id: nothing to do.
//   - id persisted but no bundle.json (state written by an older build):
//     the checksum is cleared and the same id may apply once more, so the
//     first poll re-fetches and re-applies instead of taking a 304.
//   - bundle.json verifies (signature, this build's pinned key set, id
//     matching bundle_id.txt) and applies cleanly: source "cache" until the
//     server confirms it (200/304 flips it to "fleet").
//   - otherwise it is discarded (file removed), the checksum cleared and
//     the id allowed to re-apply, so the next poll re-fetches. Nothing from
//     an unverified cache is ever applied.
func (p *ConfigPoller) restoreCachedBundle(ctx context.Context) {
	p.mu.RLock()
	id := p.lastAppliedID
	p.mu.RUnlock()
	if id <= 0 || p.dataDir == "" || p.applier == nil {
		return
	}
	discard := func(reason string) {
		_ = os.Remove(bundleBodyPath(p.dataDir))
		p.mu.Lock()
		p.checksum = ""
		p.reapplyID = id
		p.source = "default-deny"
		p.lastError = "cached bundle not re-applied: " + reason
		p.mu.Unlock()
		logging.L().Warn("fleet.config_poller.cache_discarded", "bundle_id", id, "reason", reason)
	}

	raw, err := os.ReadFile(bundleBodyPath(p.dataDir))
	if errors.Is(err, os.ErrNotExist) {
		p.mu.Lock()
		p.checksum = ""
		p.reapplyID = id
		p.source = "default-deny"
		p.mu.Unlock()
		return
	}
	if err != nil {
		discard("cache unreadable")
		return
	}
	var b Bundle
	if err := json.Unmarshal(raw, &b); err != nil {
		discard("cache is not valid bundle JSON")
		return
	}
	if b.BundleID != id {
		discard(fmt.Sprintf("bundle_id %d does not match persisted id %d", b.BundleID, id))
		return
	}
	// Replay of a bundle this device already accepted: lastID = id-1.
	if err := VerifyWithKeySet(&b, FleetSigningKeys(), id-1); err != nil {
		discard("verification failed: " + err.Error())
		return
	}
	var applyErrs []error
	rctx := context.WithValue(ctx, cacheRestoreKey{}, true)
	if ia, ok := p.applier.(ConfigItemApplier); ok {
		applyErrs, _ = ia.ApplyBundleItems(rctx, &b)
	} else {
		applyErrs = p.applier.ApplyBundle(rctx, &b)
	}
	if len(applyErrs) > 0 {
		// Apply errors are sanitized at their source (CedarApplyError:
		// category + rule id, never parser text).
		discard(fmt.Sprintf("apply failed: %v", applyErrs[0]))
		return
	}
	p.mu.Lock()
	p.lastAppliedAt = time.Now()
	p.source = "cache"
	p.lastError = ""
	// Rollback hardening: the 304 gate must name the bundle actually in
	// force, never a separately-persisted checksum. Otherwise a planted
	// older bundle.json + id + the CURRENT bundle's checksum would be pinned
	// in place by the server's 304. F1's cleared checksum (re-fetch
	// requested) is kept cleared.
	if p.checksum != "" {
		p.checksum = hexChecksumOf(raw)
	}
	p.mu.Unlock()
	logging.L().Info("fleet.config_poller.cache_reapplied", "bundle_id", id)
}

// cacheRestoreKey marks the context of a start-up cache re-apply.
type cacheRestoreKey struct{}

// IsCacheRestore reports whether ctx is a start-up re-apply of the cached
// bundle (as opposed to a bundle just fetched from Fleet). Appliers may use
// it to skip side effects that should happen once per delivered bundle.
func IsCacheRestore(ctx context.Context) bool {
	v, _ := ctx.Value(cacheRestoreKey{}).(bool)
	return v
}

// ── Sign-in wake ─────────────────────────────────────────────────────────────

var (
	tokenWakersMu sync.Mutex
	tokenWakers   = map[*ConfigPoller]struct{}{}
	tokenGen      atomic.Uint64
)

func registerTokenWaker(p *ConfigPoller) {
	tokenWakersMu.Lock()
	tokenWakers[p] = struct{}{}
	tokenWakersMu.Unlock()
}

func unregisterTokenWaker(p *ConfigPoller) {
	tokenWakersMu.Lock()
	delete(tokenWakers, p)
	tokenWakersMu.Unlock()
}

func tokenGeneration() uint64 { return tokenGen.Load() }

// NotifyTokensAvailable tells running config pollers that a session may now
// exist (desktop sign-in, served-mode token source installed or a token
// delivered by the host broker). A poller parked on ErrNotSignedIn polls
// immediately; one that is already signed in is left on its cadence, so a
// routine token renewal does not cause an extra fetch.
func NotifyTokensAvailable() {
	tokenGen.Add(1)
	tokenWakersMu.Lock()
	ps := make([]*ConfigPoller, 0, len(tokenWakers))
	for p := range tokenWakers {
		ps = append(ps, p)
	}
	tokenWakersMu.Unlock()
	for _, p := range ps {
		p.mu.RLock()
		waiting := p.awaitingSignIn
		p.mu.RUnlock()
		if waiting {
			p.wakeNow()
		}
	}
}
