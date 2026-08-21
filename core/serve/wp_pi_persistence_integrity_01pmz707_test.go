package serve_test

// WP-PI — persistence integrity for served-mode-is-a-real-mode-01PMZ707,
// scoped to THIS session's landing: WP02 (drift-gate promotion),
// WP03 (boundary-panel the graph/policy routes), WP05 (/audit and
// /permissions stop fabricating). WP01 (session-scoped served fan-out)
// landed in an earlier session (83e311c3, before this one started) and
// already carries its own coverage in wp01_session_scoping_test.go; this
// file does not re-audit WP01's fixtures, only re-runs its tests to
// confirm they still pass against this landing.
//
// Per kitty-specs/_templates/WP-persistence-integrity.md (mandatory in
// every mission, no per-mission judgement call) and this mission's
// tasks.md UNIT-1..UNIT-8 structure. This landing does NOT include WP04
// (chat-surface port/gate), WP06 (client overlay), WP07 (per-method
// triage), WP08 (lifecycle + doc dispositions), WP09 (A-14 artifact) or
// WP10 (doc reconciliation) — those remain unlanded and are named below.
// This is an interim per-landing WP-PI, not the mission-closing one; per
// plan.md Rule 5, WP01+WP02+WP03+WP05 is the mission's whole product
// claim if cut short, and this session cut there.
//
// # AC-PI-4 — per-WP enumeration (this landing touches no
// table/migration/setting/FTS index)
//
// Re-verified against the actual diffs (`git diff --stat 8ffd0766 HEAD`),
// not assumed from the plan:
//
//   - WP02 (fix(ci): WP02 — give check-serve-dispatch-drift.sh an
//     allowlist and make it able to fail) — none. Touches
//     scripts/ci/check-serve-dispatch-drift.sh (a bash script diffing two
//     greps over Go source text), two new plain-text allowlist files
//     under scripts/ci/allowlists/, scripts/ci/gates_can_fail_test.go
//     (Go test file — meta-tests that shell out to the above script; the
//     test process itself opens no database), .github/workflows/pr.yml
//     (a comment), and docs/unwired-ledger.md (prose). No table,
//     migration, persisted setting, or FTS index anywhere in this set.
//   - WP03 (fix(frontend): WP03 — boundary-panel the graph and policy
//     routes served mode cannot answer) — none. Touches only .vue
//     templates/route guards (GraphsView.vue, GraphEditor.vue,
//     RunView.vue, PolicyView.vue, LeftRail.vue), lib/useCommandPalette.ts,
//     and their Vitest specs. No Go file touched at all — verified via
//     `git diff --stat 8ffd0766 HEAD -- '*.go'` returning empty for this
//     commit's file set.
//   - WP05 (fix(frontend): WP05 — a refused audit query is not an empty
//     audit trail, and an unread dial is not 'normal') — none. Touches
//     AuditView.vue, PermissionDialsPanel.vue, useCommandPalette.ts, and
//     their Vitest specs, plus docs/unwired-ledger.md (prose). Both
//     components call existing Go RPCs (Audit_Filter,
//     Settings_GetPermissionMode) through the SAME client interfaces
//     that existed before this WP — the change is client-side handling
//     of a REJECTED call (serve-mode short-circuit / an unread-state
//     render), never a new read or write path, and never a change to
//     the Go-side persistence those RPCs are backed by. No Go file
//     touched by this commit either.
//   - WP-PI (this file) — none; adds only this enumeration.
//
// Not landed in this pass — recorded so a future WP-PI does not have to
// re-derive which units this session covered:
//
//   - WP04 (chat-surface port/gate decisions: paperclip, /, autonomy
//     chip, title suggestion, Branches_*, Config_GetFlags, /bundles,
//     /projects/:id) — NOT attempted. Spec §12 marks this WP "maybe"
//     persistence-bearing (Sessions_SuggestTitle may write a session
//     title; Config_GetFlags' source needs tracing) — if a future
//     session lands it, AC-PI-1 must be re-evaluated against what
//     actually ships, not assumed clean by extension of this file.
//   - WP06 (served client overlay enumeration + wrapValue holes) — not
//     attempted.
//   - WP07 (per-method triage of the 419-entry i15-serve-dispatch-gap.txt
//     allowlist — currently ALL entries carry the single class
//     `untriaged`, dated 2026-08-21, owner "WP07 of this same mission")
//     — not attempted.
//   - WP08 (SIGTERM shutdown fix, 33→34 doc correction, SD-05/06/14/15/16
//     dispositions) — spec §12 marks this "maybe" persistence-bearing
//     (WithStreamQueueCap may gain a settings source) — not attempted.
//   - WP09 (A-14 served-mode disposition artifact), WP10 (doc
//     reconciliation + integration + upgrade-snapshot ritual check) —
//     not attempted.
//
// # AC-PI-1 — tests boot from a previous-release database
//
// N/A by the enumeration above: this landing (WP02/WP03/WP05) adds no
// table, migration, or persisted setting. Falsifiability was NOT
// re-verified against a committed upgrade snapshot in this session,
// because there is nothing in this landing's diff for such a test to
// exercise — WP04/WP08's "maybe" cells are the ones that would require
// it, and neither landed. `go test ./core/... -race -count=1 -short -p 4`
// was RUN in full after all three commits (see the mission report for
// the exact result) and includes core/storage/sqlite's existing
// TestUpgradePath suite in its normal run; that suite passed, but this
// is coverage this landing inherited, not coverage this landing added
// or needed to add.
//
// # AC-PI-2 — this session's own fixtures, audited for the SQL/file bypass
//
// This session wrote zero new Go test fixtures — WP02's Go-side addition
// is two cases table entries plus one dedicated function in the
// PRE-EXISTING scripts/ci/gates_can_fail_test.go, neither of which reads
// or writes session/sqlite state (they shell out to a bash script that
// greps two Go source files); WP03 and WP05 wrote only Vitest/TS
// fixtures, which are outside blind spot #2's scope (that blind spot is
// specifically about Go's session.NewMemoryStore() bypassing SQL
// encode/decode).
//
// Examined and NOT changed, with reason: core/serve/chat_rpc_test.go's
// newChatHarness — named in docs/dead-code-audit-2026-08-18.md:1625 as
// "seen and never opened" before WP01. This session read it (it drives a
// real core.New-backed rpc.API and a real HTTP+WS server, not a fake bus
// — confirmed by reading wp01_session_scoping_test.go's use of
// newChatHarness and api.EventBus().Publish against the real bus) but
// did not modify it: none of WP02/WP03/WP05 touch core/serve/*.go, so
// there was nothing in this landing that needed a newChatHarness-backed
// test written or changed.
//
// # AC-PI-3 — destructive migrations
//
// None. This landing adds and repairs no migration.
//
// # AC-PI-5 — release-ritual hook
//
// This session did not land the mission's remaining WPs (WP04/06/07/08/
// 09/10), so it is not the mission's closing landing and is not
// positioned to know whether it is "the last mission to land before a
// release tag." No upgrade-snapshot obligation is claimed or discharged
// by this file.
