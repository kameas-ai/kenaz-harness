package settings

// wp_pi_test.go — controls-and-readouts-that-tell-the-truth-01PMZ808
// UNIT-PI / WP-PI. Persistence integrity, per
// kitty-specs/_templates/WP-persistence-integrity.md (mandatory in
// every mission). This is a P0 merge gate — the release PR cannot open
// without it.
//
// ── AC-PI-1 — tests start from a database/file a previous release produced ──
//
// This mission has two persistence-bearing surfaces committed so far:
// UNIT-1/WP03 (AutoCollapseBranchesInSidebar's *bool shape change) and,
// pending in this same session, UNIT-3/WP06 (WindowSize) and UNIT-8/WP13
// (scroll_position — currently blocked on E-006). UNIT-1 is the unit this
// WP-PI commit lands with, per tasks.md's "lands with or before the last
// of UNIT-1 / UNIT-3 / UNIT-8."
//
// SQL half: this mission adds no migration and no new table. Verified by
// running the existing upgrade-path harness (core/storage/sqlite/
// upgrade_path_test.go's TestUpgradePath) unmodified — it is table-driven
// over every core/storage/sqlite/testdata/upgrade/<tag>/ directory and
// needed no change from this mission:
//
//	$ go test ./core/storage/sqlite/... -run TestUpgradePath -v -count=1
//	=== RUN   TestUpgradePath
//	=== RUN   TestUpgradePath/v0.63.0
//	=== RUN   TestUpgradePath/v0.63.1
//	=== RUN   TestUpgradePath/v0.63.2
//	=== RUN   TestUpgradePath/v0.64.0
//	=== RUN   TestUpgradePath/v0.64.1
//	--- PASS: TestUpgradePath (0.00s)
//	PASS
//	ok  	github.com/kameas-ai/kenaz-harness/core/storage/sqlite	0.562s
//
// Settings half: there was no settings.json fixture of any kind in the
// repo before this mission (scripts/ci/upgrade-snapshot.sh covers
// data.db only). UNIT-1/WP03 built one:
// testdata/upgrade/v0.64.0/settings.json + a hand-written PROVENANCE.md,
// hand-constructed from the v0.64.0-tagged Settings field set (not
// captured from a real process — a representative subset, not a
// field-complete dump; see PROVENANCE.md for exactly what that means and
// what it deliberately does not claim). wp03_test.go's
// TestWP03_AC005_V0640SettingsFixture_ResolvesCollapseDefaultTrue
// materialises it at <tmpdir>/kenaz-harness/settings.json — the exact
// layout FileStore.Path() expects — and boots it through the real
// settings.NewFileStore + LoadAll, mirroring TestUpgradePath's own
// "materialise a previous release's file, boot it under HEAD" shape
// rather than hand-rolling a second one.
//
// Falsifiability — RUN, not predicted. AC-005's own doc comment states
// the mutation (revert AutoCollapseBranchesInSidebar to plain `bool` and
// EffectiveAutoCollapseBranchesInSidebar to `return
// s.AutoCollapseBranchesInSidebar`). That exact revert was performed by
// hand against the working tree (api.go's field type + accessor body,
// plus a matching temporary edit to this package's other tests so the
// package still compiled) and the test was re-run:
//
//	$ go test ./core/rpc/views/settings/... \
//	    -run TestWP03_AC005_V0640SettingsFixture_ResolvesCollapseDefaultTrue -v -count=1
//	=== RUN   TestWP03_AC005_V0640SettingsFixture_ResolvesCollapseDefaultTrue
//	    wp03_test.go:72: EffectiveAutoCollapseBranchesInSidebar() on a v0.64.0-shaped file = false, want true
//	--- FAIL: TestWP03_AC005_V0640SettingsFixture_ResolvesCollapseDefaultTrue (0.00s)
//	FAIL
//
// The revert was then undone in full (git diff --stat against the
// committed api.go and wp03_test.go showed zero lines after restoring),
// and the suite re-run green. AC-PI-1 is satisfied for every
// persistence-bearing surface committed as of this WP-PI commit.
//
// ── AC-PI-2 — audit of this mission's own fixtures for the SQL/file bypass ──
//
// Every settings test this mission has added — wp03_test.go
// (core/rpc/views/settings) and wp07_test.go (core/rpc) — drives a real
// settings.NewFileStore (via wp03_test.go's own store construction and
// via core/rpc's newTestStore(t) helper, itself settings.NewFileStore
// under a t.TempDir()), never the in-memory memoryStore bypass. None
// asserts a validator whose logic differs between FileStore.SaveAll and
// memoryStore.SaveAll (the two-of-four-validators gap CLAUDE.md's blind
// spot #2 names) — this mission's WP03/WP07 changes touch a field
// (AutoCollapseBranchesInSidebar) and a poller lifecycle
// (ReconfigureUpdatePoll), neither of which has its own SaveAll
// validator, so the memoryStore/FileStore validator-parity gap is
// unaffected by anything landed so far and is out of this WP-PI
// snapshot's scope — a future settings-validator-touching WP in this
// mission (none currently planned) would need to re-run this check.
//
// Examined and deliberately NOT changed:
// frontend/src/views/settings/__tests__/SyncPanel.spec.ts (the
// pre-2026-08-14 "asserted only the negative path" shape CLAUDE.md's
// blind-spot text cites as canonical) belongs to a different mission
// (01PMZ505) and this mission's WPs do not touch SyncPanel.vue or its
// spec — left alone, correctly. Checked every spec file this mission DID
// add or extend (wp00-falsify.spec.ts, SettingsView.wp14.spec.ts,
// LeftRail.branchSettings.test.ts, useUpdateStore.fallback.spec.ts) for
// the same one-direction-only shape: all four assert BOTH the "dial on"
// and "dial off" (or "success" and "genuine failure") directions, so
// none reproduces the anti-pattern.
//
// ── AC-PI-3 — destructive migrations ──
//
// This mission adds no migration, destructive or otherwise, as of this
// WP-PI commit. UNIT-16/WP21 (not yet landed) will touch
// storage.Config.WAL/EventBufferSize and explicitly NOT wire
// storage.Config.ForeignKeys — spec R-12 names the 0327/0332
// scratch-table-rebuild hazard that would reopen if FK enforcement ever
// became optional. If/when WP21 lands, it adds no migration either (it
// wires existing dial-application code, not schema).
//
// ── AC-PI-4 — units enumerated ──
//
// See docs/unwired-ledger.md-style enumeration in this mission's commit
// history; restated here per-WP for the units committed as of this
// WP-PI commit:
//
//   - UNIT-0 (WP00): touches no table, no migration, no persisted
//     setting, no FTS index. Two throwaway Go tests + one Vitest spec,
//     all deleted or trimmed once their target WPs landed.
//   - UNIT-1 (WP01+WP02+WP03): PERSISTENCE-BEARING. WP03 changes the
//     persisted shape of AutoCollapseBranchesInSidebar (bool -> *bool).
//     WP01/WP02 touch no table, no migration, no persisted setting, no
//     FTS index (WP01 reads an existing field; WP02 is a doc narrowing).
//   - UNIT-4 (WP07+WP08): touches no table, no migration, no persisted
//     setting, no FTS index. Reads three EXISTING settings fields
//     (AutoCheckUpdates/UpdateChannel/UpdateCheckInterval) that were
//     already persisted before this mission; adds no new field and no
//     schema change.
//   - UNIT-9 (WP14): touches no table, no migration, no persisted
//     setting, no FTS index (a router-capture timing fix only).
//
// Per tasks.md's own AC-PI-4 line, UNIT-5, UNIT-7, UNIT-9, UNIT-13,
// UNIT-14, UNIT-15 and UNIT-17 are asserted (by the mission's own
// tasks.md) to touch nothing persistence-related; this WP-PI commit
// independently confirms that for UNIT-9, the only one of that list
// landed as of this commit.
//
// ── AC-PI-5 — release-ritual hook ──
//
// This mission is NOT the last one landing before the v0.65.0 tag (many
// sibling missions in the v0.65.0 campaign are still in flight in
// parallel worktrees as of this commit). bash scripts/ci/
// upgrade-snapshot.sh v0.65.0 was NOT run and no
// core/storage/sqlite/testdata/upgrade/v0.65.0/ was committed here —
// that is the responsibility of whichever commit is actually last before
// the tag, not this WP-PI. Flagging explicitly so it is not silently
// assumed done.

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWPPI_V0640SettingsFixtureExists is a guard, not new coverage: it
// fails loudly (rather than letting wp03_test.go fail with a confusing
// "file not found") if the AC-PI-1 settings fixture this WP-PI commit
// depends on is ever deleted or moved.
func TestWPPI_V0640SettingsFixtureExists(t *testing.T) {
	for _, name := range []string{"settings.json", "PROVENANCE.md"} {
		p := filepath.Join("testdata", "upgrade", "v0.64.0", name)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("AC-PI-1 settings fixture missing: %s: %v", p, err)
		}
	}
}
