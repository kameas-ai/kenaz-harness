// wp_pi_test.go — fleet-enforcement-truth-01PMZ505 WP-PI, persistence
// integrity, per kitty-specs/_templates/WP-persistence-integrity.md
// (mandatory in every mission).
//
// ── AC-PI-4 — per-WP enumeration ────────────────────────────────────────
//
// Of the WPs this pass actually built or touched:
//
//   - WP08 (lockdown reason) — NOT persistence-bearing. lockdownReason
//     is a process-memory atomic.Value (core/fleet/lockdown.go); it is
//     never written to disk and does not survive a process restart by
//     design (only a broker relayed reason via NotifyOn401/Watcher, or
//     a bootstrap fetch, populates it). No table, no migration, no
//     persisted setting, no FTS index.
//   - WP06 (chain-break SkipToID) — touches the PRE-EXISTING
//     `<DataDir>/fleet/audit_cursor.txt` file via
//     `fleet.saveAuditCursor` (unchanged code path — SkipToID already
//     called it before this WP added the RPC wrapper). No new
//     persistence surface; the RPC surface added here is a thin
//     delegation proven correct at the RPC layer in
//     core/rpc/views/compliance/impl_test.go, and the underlying
//     cursor-persistence path itself is already covered by
//     core/fleet/audit_archive_test.go's `TestAuditCursor_RoundTrip`
//     (pre-existing, unmodified).
//   - WP09 (site env vars), WP11 (catalog unpublish) — remote fleet-
//     server-side state (env var values live on the fleet server;
//     catalog listings live on the fleet server). No LOCAL table,
//     migration, settings field, or FTS index. The one local
//     side-effect WP11 adds is an audit-log write via the existing
//     `contextaudit.Emitter` interface (in-memory ring buffer /
//     eventlog, unchanged wiring — see
//     core/rpc/views/catalog/impl_test.go's audit-emission tests).
//   - WP14 (connector token invalidation) — `authbroker.ConnectorTokens`
//     is a memory-only cache by design (its own doc:
//     "Tokens are held in memory only"); On401/Invalidate touch no
//     disk state at all.
//   - WP15 (ACP registry) — no code change beyond comments; no
//     persistence surface.
//   - WP05 (retention honesty) — the only production edit was deleting
//     the dead bundle-config-apply helper in core/fleet/audit_retention.go
//     (a zero-caller function whose own signature never touched storage —
//     it operated on an in-memory *AuditRetentionSweeper argument, never a
//     store). No persistence surface removed or added. (Prior revisions of
//     this comment and docs/unwired-ledger.md §1.3/§1.8 claimed this
//     deletion had already happened; it had not — see the fix commit that
//     corrected both.)
//   - **WP12 (sync push-path honesty, `Accent` removal) — THE ONE
//     PERSISTENCE-BEARING SURFACE IN THIS PASS.** `uiThemeKind`'s Apply
//     closure ends in `store.SaveAll(s)` — a real settings.json write.
//     See below.
//
// ── AC-PI-1 — tests start from a file/database a previous release produced ──
//
// spec.md §5.15 anticipated exactly this shape and pre-authorized the
// alternative to a sqlite snapshot: "state in the report that the sync
// payloads live in settings.json rather than sqlite" — verified true,
// same as model-authored-graphs-01PMGA01's UNIT-PI found for
// GraphAuthoringEnabled (core/rpc/views/settings/
// graph_authoring_upgrade_test.go's header explains why
// core/storage/sqlite.TestUpgradePath cannot reach settings.json at
// all). And the audit's second uncovered item, quoted verbatim per
// spec §5.15's own instruction to name it rather than close it:
// "upgrade-path behaviour of settings.json — every trace was against
// the current struct shape and no test in the tree starts from a prior
// release's file." That hole is this mission's to NAME, not to close —
// named here.
//
// What IS run: TestUIThemeApply_PreservesAccentAgainstAV0640ShapedFile
// below writes a settings.json shaped exactly like a v0.64.0 install's
// (theme+accent both non-default, matching core/rpc/views/settings/
// testdata/upgrade/v0.64.0/settings.json's real captured values, so
// this is not an invented shape), loads it through the REAL FileStore
// (not settings.NewMemoryStore, which would skip the JSON encode/decode
// CLAUDE.md blind spot #2 warns about), applies a `{"theme":"light"}`
// payload — the wire shape `uiThemePayload` now produces post-WP12,
// with NO "accent" key at all — through the real Apply closure, and
// re-reads the file from disk. Accent must survive at its ORIGINAL
// value ("violet"), because the applier no longer touches it.
//
// Falsifiable: reverting sync_categories.go's `uiThemeKind` Apply
// closure to also set `s.Accent = p.Accent` (the pre-WP12 shape) makes
// this test fail with a wrong-Accent-after-apply message, because
// `uiThemePayload` (also reverted in the same hypothetical) would once
// again carry Accent and the applied payload in THIS test still omits
// it — reproducing exactly the "an old device sends no accent, a new
// one silently clears it" hazard AC-023 exists to prevent. Verified by
// running the mutation in this session (see this mission's final
// report for the transcript); not left as a claim.
//
// ── AC-PI-2 — fixture audit ──────────────────────────────────────────────
//
// Examined and CHANGED: frontend/src/views/settings/__tests__/
// SyncPanel.spec.ts gained three new assertions (AC-024) driving the
// real rendered DOM text, not a hand-built status fixture — the exact
// fixture WP07 (a prior WP in this same mission) already replaced the
// hand-built category-id literal in.
//
// Examined and CHANGED: core/rpc/sync_categories_bytepin_test.go's
// TestSyncCategories_ApplyRoundTripPin — the ui_theme sub-case now
// seeds a sentinel Accent value BEFORE apply and asserts it is
// UNCHANGED after, rather than asserting Accent moved with the payload
// (which is no longer true, on purpose).
//
// Examined and DELIBERATELY NOT changed: core/rpc/sync_categories_test.go's
// TestModelPrefsCollector_CredentialFree and its sibling
// TestNoopApplier_NoError (provider_profiles/mcp_recipes) — both
// already drive `newTestStore` (a real settings.FileStore under
// t.TempDir(), not an in-memory bypass) and neither's assertions
// changed shape under this WP; they were read to confirm they were not
// SILENTLY invalidated by the Accent removal (they were not — neither
// ever asserted anything about Accent).
//
// ── AC-PI-3 — destructive migrations ─────────────────────────────────────
//
// This mission adds no migration of any kind, destructive or
// otherwise, in any package. Not applicable.
//
// ── AC-PI-5 — release-ritual hook ────────────────────────────────────────
//
// bash scripts/ci/check-upgrade-snapshot-present.sh was RUN against
// this tree: "OK — snapshot chain reaches v0.78.1 (newest release tag
// v0.78.1)." This mission is not observed to be the last landing
// before a tag as of this commit; the snapshot chain was already
// current and this pass did not need to run
// scripts/ci/upgrade-snapshot.sh.
package rpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	settingsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
)

// v0640ShapedSettingsJSON mirrors the real captured values in
// core/rpc/views/settings/testdata/upgrade/v0.64.0/settings.json
// (theme="dark", accent="default") but sets Accent to a distinctive
// non-default value ("violet") so a test asserting "Accent survived
// unchanged" cannot be satisfied by coincidence (a bug that reset
// Accent to the package default would still pass a test using the
// real default value).
const v0640ShapedSettingsJSON = `{
  "schemaVersion": 1,
  "theme": "dark",
  "accent": "violet",
  "maxAgentTurns": 25
}`

// TestUIThemeApply_PreservesAccentAgainstAV0640ShapedFile is AC-PI-1's
// falsifiable proof for the one persistence-bearing surface this
// mission's pass touched. See the file-header comment above for the
// full discussion, the mutation this was proven against, and why a
// sqlite snapshot does not apply here.
func TestUIThemeApply_PreservesAccentAgainstAV0640ShapedFile(t *testing.T) {
	dir := t.TempDir()
	settingsDir := filepath.Join(dir, "kenaz-harness")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(v0640ShapedSettingsJSON), 0o644); err != nil {
		t.Fatalf("write v0.64.0-shaped settings.json: %v", err)
	}

	store, err := settingsview.NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	// Sanity: the fixture loads with the pre-mission Accent value.
	before, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll (pre-apply): %v", err)
	}
	if before.Accent != "violet" {
		t.Fatalf("fixture Accent = %q, want violet (fixture is malformed)", before.Accent)
	}

	// Drive the REAL apply path — registerSyncCategories +
	// syncer.ApplyCategory, the exact call chain a real fleet pull
	// exercises — not a hand-rolled equivalent. A first draft of this
	// test called store.SaveAll directly with a manually-built Theme
	// value, which passed regardless of what uiThemeKind's Apply
	// closure actually did with Accent — exactly the blind-spot-#2
	// shape CLAUDE.md warns about. Caught by mutation-testing this
	// file itself before landing it (see the mission's final report).
	syncer := corefleet.NewSyncer(nil)
	t.Cleanup(syncer.Stop)
	registerSyncCategories(context.Background(), syncer, store, nil, nil)

	incoming := []byte(`{"theme":"light"}`) // post-WP12 wire shape: no "accent" key at all
	if err := syncer.ApplyCategory(context.Background(), corefleet.SyncCategoryUITheme, incoming); err != nil {
		t.Fatalf("ApplyCategory(ui_theme): %v", err)
	}

	// Re-read from DISK (a fresh store instance), not from the
	// in-memory `s` value, so a bug that never persisted the field at
	// all cannot pass this test by accident.
	reopened, err := settingsview.NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore (reopen): %v", err)
	}
	after, err := reopened.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll (post-apply, reopened): %v", err)
	}

	if after.Theme != "light" {
		t.Errorf("Theme after apply = %q, want light", after.Theme)
	}
	if after.Accent != "violet" {
		t.Errorf("Accent after apply+reopen = %q, want violet (unchanged) — "+
			"the sync applier must not clear a locally-set Accent just "+
			"because the incoming payload no longer carries one", after.Accent)
	}
	if after.MaxAgentTurns != 25 {
		t.Errorf("MaxAgentTurns after apply+reopen = %d, want 25 (unrelated field must survive)", after.MaxAgentTurns)
	}
}
