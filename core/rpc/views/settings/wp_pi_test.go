package settings

// wp_pi_test.go — controls-and-readouts-that-tell-the-truth-01PMZ808
// UNIT-PI / WP-PI. Persistence integrity, per
// kitty-specs/_templates/WP-persistence-integrity.md (mandatory in
// every mission). This is a P0 merge gate — the release PR cannot open
// without it.
//
// UPDATED 2026-08-21: this header originally described the state as of
// UNIT-1's landing, with UNIT-3/WP06 and UNIT-8/WP13 "pending in this
// same session." Both landed in a later session (UNIT-8's E-006 was
// resolved by code-level evidence, not an owner ruling — see that
// commit body). This update folds them in rather than leaving a stale
// snapshot; the git history of this file has the earlier version.
//
// ── AC-PI-1 — tests start from a database/file a previous release produced ──
//
// This mission has THREE persistence-bearing surfaces landed as of
// this update: UNIT-1/WP03 (AutoCollapseBranchesInSidebar's *bool
// shape change), UNIT-3/WP06 (WindowSize gains a write path), and
// UNIT-8/WP13 (scroll_position gains its first production caller).
//
// SQL half: this mission adds no migration and no new table across all
// three surfaces. scroll_position is a PRE-EXISTING column
// (core/session/migrations.go) with a pre-existing UPDATE
// (core/session/store.go) — UNIT-8 built the missing
// client/binding/serve chain above it, not new schema. Verified by
// running the existing upgrade-path harness (core/storage/sqlite/
// upgrade_path_test.go's TestUpgradePath) unmodified — table-driven
// over every testdata/upgrade/<tag>/ directory, RUN (not predicted)
// after UNIT-8's v0.66.0 snapshot addition:
//
//	$ go test ./core/storage/sqlite/... -run TestUpgradePath -v -count=1
//	=== RUN   TestUpgradePath
//	=== RUN   TestUpgradePath/v0.63.0 .. v0.66.0  (8 tags)
//	--- PASS: TestUpgradePath (0.00s)
//	    --- PASS: TestUpgradePath/v0.66.0 (0.67s)
//	    [... 7 more, all PASS]
//	PASS
//	ok  	github.com/kameas-ai/kenaz-harness/core/storage/sqlite	1.093s
//
// UNIT-8 additionally added core/storage/sqlite/
// scroll_position_upgrade_test.go, which drives the FULL production
// chain (session.Manager.SaveScrollPosition -> Store.
// UpdateScrollPosition -> SQL) against the v0.66.0 snapshot, closing
// the DB, reopening it, and asserting the offset survived — not merely
// that TestUpgradePath's generic table-shape check passes.
//
// Settings half: UNIT-1/WP03 built testdata/upgrade/v0.64.0/
// settings.json + PROVENANCE.md (hand-constructed, not captured from a
// real process). UNIT-3/WP06 reused that SAME fixture (not a second
// one) for the WindowSize round-trip, in main_test.go
// (TestWindowSizeUpgradePath) since the wiring under test
// (resolveInitialWindowSize/persistWindowSize) lives in main.go, not
// this package.
//
// Falsifiability — RUN, not predicted, for all three surfaces:
//
//  1. UNIT-1/WP03 (previously verified, see prior git revision of this
//     file for the full transcript): revert AutoCollapseBranchesInSidebar
//     to plain bool. TestWP03_AC005_V0640SettingsFixture_
//     ResolvesCollapseDefaultTrue fails
//     ("EffectiveAutoCollapseBranchesInSidebar() on a v0.64.0-shaped
//     file = false, want true").
//  2. UNIT-3/WP06: mutated persistWindowSize to `return` before doing
//     any work. TestWindowSizeRoundTrip AND TestWindowSizeUpgradePath
//     both failed ("post-shutdown WindowSize = {1280 800}, want {1440
//     900} (must overwrite, not re-seed to 1280x800)"). Reverted;
//     re-ran green.
//  3. UNIT-8/WP13: mutated Manager.SaveScrollPosition to `return nil`
//     before calling the store. TestScrollPosition_
//     RoundTripsAgainstAPreviousReleaseDatabase failed ("ScrollPosition
//     after reopen = 0, want 400"). Reverted; re-ran green.
//
// AC-PI-1 is satisfied for every persistence-bearing surface landed as
// of this WP-PI update.
//
// ── AC-PI-2 — audit of this mission's own fixtures for the SQL/file bypass ──
//
// Every settings/session test this mission has added drives a real
// backing store, never an in-memory bypass:
// wp03_test.go/wp06_test.go/wp19_test.go/wp21_test.go
// (core/rpc/views/settings) all construct settings.NewFileStore under
// a t.TempDir() (wp21_test.go's memoryStore cases are DELIBERATE — see
// below, not an accidental bypass); main_test.go
// (repo root, UNIT-3) does the same;
// scroll_position_upgrade_test.go (core/storage/sqlite, UNIT-8) drives
// real sqlite via storagesqlite.Open against a materialised snapshot,
// never session.NewMemoryStore().
//
// wp21_test.go (UNIT-16, FR-032) is the one place this mission
// DELIBERATELY drives memoryStore.SaveAll in addition to
// FileStore.SaveAll — not a bypass but the fix for the exact
// two-of-four-validators gap CLAUDE.md's blind spot #2 names:
// memoryStore.SaveAll only calls validateCompactionFields and
// validateUpdateFields, so a test that drove only FileStore would have
// left memoryStore silently accepting what FileStore rejects.
// TestSaveAll_RejectsNegativeMaxGeneratedImageBytes_MemoryStore and its
// LocalRuntimeRAMOverrideGB twin exist specifically to prove the new
// validator reaches both stores, not just the one most tests happen to
// construct.
//
// Examined and deliberately NOT changed:
// frontend/src/views/settings/__tests__/SyncPanel.spec.ts (the
// pre-2026-08-14 "asserted only the negative path" shape CLAUDE.md's
// blind-spot text cites as canonical) belongs to a different mission
// (01PMZ505) and this mission's WPs do not touch SyncPanel.vue or its
// spec — left alone, correctly. Every spec file this mission's Go
// AND frontend WPs added or extended (wp00-falsify.spec.ts,
// SettingsView.wp14.spec.ts, LeftRail.branchSettings.test.ts,
// useUpdateStore.fallback.spec.ts, SessionsView.branching.test.ts,
// SessionsView.test.ts, useLongSessionNudge.test.ts) asserts BOTH
// directions (dial-on/dial-off, success/genuine-failure,
// mutation-applied/mutation-reverted), so none reproduces the
// one-direction-only anti-pattern.
//
// ── AC-PI-3 — destructive migrations ──
//
// This mission adds no migration, destructive or otherwise, across
// UNIT-1/3/8/16. UNIT-8's scroll_position column and UNIT-16's
// WAL/ForeignKeys/EventBufferSize/parallel_dispatch dials all read or
// write EXISTING schema/config surfaces. UNIT-16 explicitly does NOT
// wire storage.Config.ForeignKeys — spec R-12 / D-6 name the 0327/0332
// scratch-table-rebuild hazard that would reopen if FK enforcement
// ever became optional; TestConfig_ForeignKeys_False_StillEnforced
// asserts the invariant stays unconditional regardless of the dial.
//
// ── AC-PI-4 — units enumerated ──
//
// Per-WP, as of this WP-PI update (UNIT-0/1/2/4/5/6/7/9 landed
// 2026-08-19/20 per prior sessions; UNIT-3/8/14/16 landed
// 2026-08-21 in this session; UNIT-10 dissolved into
// audit-that-tells-the-truth-01PMZA10 UNIT-6/WP08, not this mission's
// commit):
//
//   - UNIT-1 (WP01+WP02+WP03): PERSISTENCE-BEARING (WP03).
//   - UNIT-3 (WP06): PERSISTENCE-BEARING. WindowSize gains a write
//     path (main.go's OnShutdown); no schema change.
//   - UNIT-8 (WP13): PERSISTENCE-BEARING. scroll_position gains its
//     first production caller through a pre-existing column; no
//     schema change.
//   - UNIT-9 (WP14): touches no table, no migration, no persisted
//     setting, no FTS index (a router-capture timing fix only).
//   - UNIT-14 (WP19): touches no table, no migration, no persisted
//     setting, no FTS index — ten client-doc narrowings plus one Go
//     doc correction (SaveMonthlyCostNotifyUSD's comment); no
//     behaviour or schema change.
//   - UNIT-16 (WP21): touches EXISTING config (storage.Config.WAL /
//     ForeignKeys / EventBufferSize / SecretsBackend), an EXISTING
//     agentgraph manifest attr (parallel_dispatch, narrowed not
//     wired), and adds validation (not migration) to
//     validateCompactionFields for two EXISTING settings fields
//     (MaxGeneratedImageBytes, LocalRuntimeRAMOverrideGB). No table,
//     no migration, no FTS index.
//
// Per tasks.md's own AC-PI-4 line, UNIT-5, UNIT-7, UNIT-13, UNIT-15
// and UNIT-17 are asserted (by the mission's own tasks.md) to touch
// nothing persistence-related and have NOT landed as of this update —
// still to be independently confirmed when they land, per this same
// discipline. UNIT-11 and UNIT-12 (Context Library / memory panel
// controls) are NOT yet landed either and are not enumerated here.
//
// ── AC-PI-5 — release-ritual hook ──
//
// This mission is not the last one landing before a tag as of this
// commit (no release branch cut for the next version at the time of
// writing). HOWEVER: while building UNIT-8's AC-036, this WP found the
// upgrade-snapshot chain was ALREADY behind the newest release tag
// independent of this mission (check-upgrade-snapshot-present.sh:
// newest tag v0.66.0, newest snapshot v0.65.1 — a pre-existing gap,
// not caused by this mission). Ran `bash scripts/ci/upgrade-snapshot.sh
// v0.66.0` and committed core/storage/sqlite/testdata/upgrade/v0.66.0/
// with a hand-written PROVENANCE.md (see UNIT-8's commit). Both
// check-upgrade-snapshot-present.sh and check-upgrade-snapshots-locked.sh
// were RUN and confirmed clean afterward. This closes the pre-existing
// gap; it does not exempt the release that actually cuts a v0.67.0 (or
// later) tag from running the ritual again for ITS tag.

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
