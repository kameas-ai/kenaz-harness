// Package fleet — config_pull_org.go
//
// Bundle state is per org. lastAppliedID, the 304 checksum, the cached
// bundle.json and the sections applied from it all belong to the org whose
// token fetched them. When the signed-in org changes (sign-out + sign-in to
// another org, a served workbench re-enrolled under another org) the poller
// must neither keep enforcing the old org's policy nor reject the new org's
// bundles because their bundle_id is lower than the old org's
// (VerifyWithKeySet's monotonic check). syncOrg resets that state on an org
// change: in-memory counters, the on-disk cache, and — by applying an empty
// bundle, whose sections all have replace semantics — the policy in force.
// Parity with kenaz review P2 #8.
package fleet

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// bundleOrgPath is the org the persisted bundle state belongs to.
func bundleOrgPath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "bundle_org.txt")
}

// loadBundleOrg returns the persisted org ("" when absent: an install from
// before per-org state, or nothing applied yet).
func loadBundleOrg(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	raw, err := os.ReadFile(bundleOrgPath(dataDir))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func saveBundleOrg(dataDir, org string) error {
	if dataDir == "" || org == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "fleet"), 0o700); err != nil {
		return err
	}
	return atomicWriteFile(bundleOrgPath(dataDir), org+"\n")
}

// currentTokenOrg is the default org source: the Zitadel resource-owner
// claim of the access token the next GET /api/v1/configs will send — the
// org Fleet scopes the served bundle to. "" when there is no usable token
// or the claim is absent; an unknown org never triggers a reset.
//
// The token, not identity.json's org_id, is the key: the identity file is
// written by enroll, which can lag the token (or outlive it), and its org_id
// is Fleet's internal UUID, a different namespace from the claim — mixing
// the two sources would read as an org change.
func currentTokenOrg() string {
	org, err := ResourceOwnerFromAccessToken()
	if err != nil {
		return ""
	}
	return org
}

// orgResetKey marks the context of the empty-bundle apply that clears a
// previous org's sections.
type orgResetKey struct{}

// IsOrgReset reports whether ctx is the apply that clears the previous
// org's policy after an org change (an empty bundle, not one Fleet sent).
func IsOrgReset(ctx context.Context) bool {
	v, _ := ctx.Value(orgResetKey{}).(bool)
	return v
}

// syncOrg compares the current org with the org the bundle state belongs
// to and resets that state when they differ. It returns true when it reset.
//
//   - current org unknown ("") → nothing (keep what is in force).
//   - no recorded org (first sign-in, or state from a build before per-org
//     keying) → adopt the current org; nothing is reset.
//   - recorded org == current → nothing.
//   - recorded org != current → reset.
func (p *ConfigPoller) syncOrg(ctx context.Context) bool {
	orgOf := p.orgOf
	if orgOf == nil {
		orgOf = currentTokenOrg
	}
	cur := orgOf()
	if cur == "" {
		return false
	}
	p.mu.Lock()
	prev := p.orgID
	if prev == cur {
		p.mu.Unlock()
		return false
	}
	p.orgID = cur
	if prev == "" {
		hadBundle := p.lastAppliedID > 0
		p.mu.Unlock()
		if hadBundle {
			if err := saveBundleOrg(p.dataDir, cur); err != nil {
				logging.L().Warn("fleet.config_poller.save_org_failed", "err", err.Error())
			}
		}
		return false
	}
	hadID := p.lastAppliedID
	p.lastAppliedID = 0
	p.lastAppliedAt = time.Time{}
	p.checksum = ""
	p.reapplyID = 0
	p.source = "default-deny"
	p.lastError = ""
	p.keyUnknown = false
	p.orgPaused = false
	applier := p.applier
	p.mu.Unlock()

	if p.dataDir != "" {
		for _, path := range []string{
			bundleBodyPath(p.dataDir),
			bundleIDPath(p.dataDir),
			bundleChecksumPath(p.dataDir),
			bundleApplyMetaPath(p.dataDir),
		} {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				logging.L().Warn("fleet.config_poller.org_reset_remove_failed", "file", filepath.Base(path), "err", err.Error())
			}
		}
		if err := saveBundleOrg(p.dataDir, cur); err != nil {
			logging.L().Warn("fleet.config_poller.save_org_failed", "err", err.Error())
		}
	}

	// Clear the previous org's sections: every section has replace
	// semantics (absent cedar_delta clears fleet rules, nil mcp_allowlist
	// lifts the restriction, empty provisioned_mcp / mandated_items
	// reconcile to nothing). The new org's bundle, fetched by the same
	// poll, then applies onto a clean slate; if that fetch fails, the
	// device runs with no fleet policy rather than another org's.
	if applier != nil {
		rctx := context.WithValue(ctx, orgResetKey{}, true)
		var errs []error
		if ia, ok := applier.(ConfigItemApplier); ok {
			errs, _ = ia.ApplyBundleItems(rctx, &Bundle{})
		} else {
			errs = applier.ApplyBundle(rctx, &Bundle{})
		}
		for _, e := range errs {
			if e != nil {
				logging.L().Warn("fleet.config_poller.org_reset_clear_failed", "err", e.Error())
			}
		}
	}
	logging.L().Info("fleet.config_poller.org_changed", "previous_bundle_id", hadID)
	return true
}
