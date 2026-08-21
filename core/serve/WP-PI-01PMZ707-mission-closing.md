# WP-PI — persistence integrity for `served-mode-is-a-real-mode` (01PMZ707), mission-closing

This is the **mission-closing** WP-PI. `WP-PI-01PMZ707-no-persistence-surface.md`
(same directory) is the INTERIM WP-PI from an earlier session that landed
WP01/WP02/WP03/WP05 and explicitly deferred WP04/WP06/WP07/WP08/WP09/WP10
as "not attempted" — this file covers exactly that remainder, enumerated
against what actually shipped in THIS session, not assumed clean by
extension of the interim file. Both files stay; neither supersedes the
other's history.

Per `kitty-specs/_templates/WP-persistence-integrity.md` (mandatory in
every mission, no per-mission judgement call).

## What was RUN vs READ

**RAN** (verbatim, this session, after all of WP04/06/07/08/09/10 were
committed):

```
go test ./core/... -race -count=1 -short -p 4
```
→ every package `ok`, zero `FAIL` lines (checked explicitly with
`grep -E "FAIL|^---"` over the full output — empty). Includes
`core/storage/sqlite`'s existing `TestUpgradePath` suite in its normal
run (14.796s), `core/serve` (50.594s, includes this session's new
`wp01... wp04... wp08...` test files), and the top-level `github.com/kameas-ai/kenaz-harness`
package (`main.go`'s own tests, including the two new
`TestInstallServeShutdownSignal_SIGTERM_CancelsContext` /
`TestServeShutdown_SIGTERM_StopsRealHTTPServer` — stubbed
`frontend/dist/index.html` locally to satisfy the `go:embed` directive
for the test run only, then deleted it; confirmed `git status` clean of
`frontend/dist*` afterward, per CLAUDE.md's `wails generate module`
hazard note — this is `go test`/`go build`, not `wails generate`, so it
opens no real profile database).

```
cd frontend && ./node_modules/.bin/vitest run --reporter=basic
```
→ 253 files passed, 1 skipped; 2318 tests passed, 6 skipped; zero
failures.

```
bash scripts/ci/check-codegen.sh
bash scripts/ci/check-serve-dispatch-drift.sh
bash scripts/ci/check-serve-gap-classification.sh
go test ./scripts/ci/ -run TestGates -count=1
```
→ all four clean/passing (78 gate subtests, including the two new
planted-violation cases this session added:
`serve-gap-classification/untriaged-entry-missing-date-and-owner` and
the existing `serve-dispatch-drift/forward-unallowlisted-binding`).

**NOT RUN:** `bash scripts/ci/upgrade-snapshot.sh <new-tag>` — no
migration exists in this session's diff for it to capture (see AC-PI-3
below), so there is nothing new to snapshot.

## AC-PI-4 — per-WP enumeration (this session's landing touches no table/migration/setting/FTS index)

Re-verified against the actual diffs of this session's four commits
(`a73ba11a` WP04, `a5799955` WP06, `0c8eea91` WP08, `c0497176` WP07 — WP09
and WP10 produced no git-tracked diff at all, see below), not assumed
from the spec's pre-analysis:

- **WP04** (`fix(chat): WP04`). Spec §12 marked this **"maybe"**
  persistence-bearing on two specific cells: *"Sessions_SuggestTitle may
  write a session title"* and *"Config_GetFlags' source needs tracing."*
  Both resolved by reading the actual implementation, not assumed:
  - `Config_GetFlags` → `rpc.ComputeFeatureFlags()` reads three
    package-level functions (`coreslashcmd.UserSlashcmdEnabled()`,
    `llmcap.MultimodalOutEnabled()`, `gemini.IsEnabled()`), all
    env-var-derived, zero database or settings-store access. **No.**
  - `Sessions_SuggestTitle` → `sessions.SuggestTitle` → `RequestRetitle`
    → **does write** the session's `name` column (confirmed reading
    `core/rpc/views/sessions/impl.go:865-895`). This is real, but it is
    **PRE-EXISTING** code this WP did not write and does not change —
    the desktop `Sessions_SuggestTitle` binding has called the identical
    function since `session-auto-titling-01KQ8TDS` shipped, long before
    this mission, and it is already exercised by
    `TestBroker_SuggestTitle_EmitsListChanged`
    (`core/rpc/views/sessions/impl_broker_test.go:187`) plus the
    existing desktop RPC path. WP04 adds ONLY a new HTTP entry point (a
    `core/serve/server.go` dispatch `case`) that calls this same
    unchanged function — no new column, no new table, no migration, no
    change to how the write is performed. **AC-PI-1's concern is
    migration SELECTION and SCHEMA EVOLUTION being invisible on a fresh
    database — this WP touches neither.** The `sessions.name` column
    and its migration predate this mission by a wide margin and are
    already covered by `TestUpgradePath`'s existing snapshot chain.
    Judgement, stated plainly rather than asserted silently: **a new
    caller reaching an already-migrated, already-tested write path is
    not the same risk class as a migration that has never run against
    populated tables** (CLAUDE.md's blind spot #3's own framing) — this
    is the former, not the latter.
  - Also gated (Attachments_*, Slash_*, Branches_*) and boundary-panelled
    (BundlesView.vue, ProjectLandingPage.vue) — all pure frontend
    `v-if`/route-guard changes, zero Go touched by those hunks.
  - **Net: no migration, no new table, no new persisted setting, no FTS
    index.** The one write path touched is pre-existing and unmigrated
    by this change.
- **WP06** (`fix(frontend): WP06`) — none. `frontend/src/lib/harnessClient.ts`
  (TS client shape) and its Vitest spec only. Zero Go files in the diff.
- **WP07** (`chore(serve): WP07`) — none. `scripts/ci/allowlists/i15-serve-dispatch-gap.txt`
  (a plain-text allowlist), `scripts/ci/check-serve-gap-classification.sh`
  (a new bash gate — reads a text file, writes nothing), `scripts/ci/gates_can_fail_test.go`
  (a planted-violation test case that shells out to the above and mutates
  a scratch copy of the allowlist, restored via the shared `plant()`
  helper — no database), `.github/workflows/pr.yml` (a CI step), plus
  two frontend fixes (`ChatInput.vue`'s slash-Enter gate,
  `LeftRail.vue`'s MemoryBadge gate) that are pure `v-if` changes.
- **WP08** (`fix(serve): WP08`). Spec §12 marked this **"maybe"**
  persistence-bearing: *"if WithStreamQueueCap gains a settings
  source."* It does gain a source — `EnvStreamQueueCap`
  (`KENAZ_SERVE_STREAM_QUEUE_CAP`) — but it is an **environment
  variable read at process boot** (`os.Getenv`, `core/serve/server.go`'s
  `StreamQueueCapFromEnv`), not a database or settings-store value.
  **No table, no persisted setting.** The SIGTERM handling
  (`installServeShutdownSignal`) touches signal channels and a context,
  no I/O. The `StreamTruncatedPayload.Reason` consumer
  (`useSession.ts`'s `streamTruncatedCopy`) is a pure in-memory lookup
  table, no persistence. The SD-05/06 doc-comment corrections
  (`Auth_State`/`Connectors_List`/`Connectors_Status`) touch only Go
  comments, no behaviour. **Net: no migration, no new table, no new
  persisted setting, no FTS index.**
- **WP09** (`chore(serve): WP09`) — **produced NO git-tracked diff at
  all.** `research/a14-served-dispositions.md` lives under
  `kitty-specs/served-mode-is-a-real-mode-01PMZ707/research/`, which is
  entirely `.gitignore`d (`.gitignore:13`, `kitty-specs/`). The spec's
  own instruction — *"This WP wires nothing"* — is doubly true here:
  it wired nothing AND committed nothing. No persistence surface is
  possible for a file `git` never sees.
- **WP10** (`docs(serve): WP10`) — `docs/served-mode-boundary.md` and
  `docs/unwired-ledger.md` (prose) are git-tracked and committed;
  `docs/roadmap.md` is ALSO `.gitignore`d (edited on the shared checkout
  directly per CLAUDE.md's roadmap doctrine, not via this worktree's
  git). None of the three touches Go code, a table, a migration, a
  setting, or an FTS index.
- **This file (mission-closing WP-PI)** — none; adds only this
  enumeration, git-tracked under `core/serve/`.

**Conclusion, earned by the enumeration above, not asserted ahead of
it:** across the entire mission (WP01 through WP10 plus both WP-PI
files), the only write path any WP's diff reaches is
`Sessions_SuggestTitle`'s pre-existing, pre-mission, already-migrated
`RequestRetitle` call — reached through a genuinely new caller (the serve
dispatch case) but not a new schema. **AC-PI-4 applies: this mission has
no persistence surface of its own.**

## AC-PI-1 — tests boot from a previous-release database

**N/A**, by the enumeration above — this mission adds no migration, no
new table, and no new persisted setting; `Sessions_SuggestTitle`'s write
path is pre-existing and already covered by `TestUpgradePath`'s existing
snapshot chain (unchanged by this mission). The falsifiability bar this
AC sets — *"revert this mission's production change and delete any test
written specifically for the bug; the upgrade-path assertion must STILL
fail"* — has no target here: there is no migration-selection bug this
mission fixed or could have introduced, so there is no assertion for a
reversion to falsify. `TestServedChat_SuggestTitle_Ported`
(`core/serve/wp04_chat_affordances_test.go`) DOES drive real sqlite
(`newChatHarness`'s `core.New(core.Options{DataDir: t.TempDir()})`, not
`session.NewMemoryStore()`) per blind spot #2's requirement — it just
does not need to boot from a PRIOR release's snapshot, because it is not
exercising migration selection, only a new HTTP entry point onto
already-migrated code.

## AC-PI-2 — this mission's own fixtures, audited for the SQL/file bypass

Fixtures written or extended by WP04/06/07/08:

- `core/serve/wp04_chat_affordances_test.go` (new) — `newChatHarness`,
  real sqlite. **Examined and used as-is; not modified.**
- `core/serve/wp08_served_count_test.go` (new) — reads `servedMethods`
  (an in-memory `[]string` var), zero I/O. Not a persistence fixture by
  definition.
- `core/serve/wp08_stream_queue_cap_env_test.go` (new) — a fake `getenv`
  closure and a bare `&Server{}` struct literal; zero I/O.
- `main_serve_shutdown_test.go` (new) — `rpc.New(nil)` (the test
  chassis, no core, matching `core/serve/server_test.go`'s own
  `newTestServer` pattern) for the HTTP-shutdown test, and no core API
  at all for the signal-only test. Neither touches sqlite.
- `frontend/src/components/chat/__tests__/ChatInput.wp04ServedGating.test.ts`,
  `MessageBubble.servedGating.test.ts`,
  `frontend/src/views/bundles/__tests__/BundlesView.served.test.ts`,
  `frontend/src/views/projects/__tests__/ProjectLandingPage.served.test.ts`,
  `frontend/src/lib/__tests__/harnessClient.wp06Overlay.test.ts`,
  `frontend/src/lib/__tests__/useSession.streamTruncatedCopy.test.ts`,
  `frontend/src/shell/__tests__/LeftRail.memoryBadge.served.test.ts` —
  all Vitest/TS, outside blind spot #2's scope (Go's
  `session.NewMemoryStore()` SQL bypass).

**Examined and deliberately NOT changed, with reason (required by
AC-PI-2's own falsifiability bar — a report that changed everything it
looked at, or nothing, did not look):**

- `core/serve/chat_rpc_test.go`'s `newChatHarness` — the interim WP-PI
  already audited this (real core, real bus, not a fake) before WP01
  landed; this session's WP04 test REUSES it unmodified rather than
  building a parallel harness, which is itself the correct outcome of
  that earlier audit, not a gap.
- `core/serve/server_test.go`'s `newTestServer` (`rpc.New(nil)`, no
  core) — used by `main_serve_shutdown_test.go`'s HTTP-shutdown test
  because that test needs a REAL `http.Server` to observe `Shutdown`
  running, but explicitly does NOT need real session/message
  persistence (it never creates a session) — `rpc.New(nil)`'s stable
  stub surface is the correct, narrower tool for that specific
  assertion, not a shortcut around blind spot #2 (which is about tests
  that assert persistence and skip SQL — this test asserts transport
  shutdown, not persistence, so the memory-store bypass concern does
  not apply to it in the first place).

## AC-PI-3 — destructive migrations

None. This mission adds and repairs no migration anywhere across WP01–WP10.

## AC-PI-5 — release-ritual hook

**Not determined by this WP-PI.** Per CLAUDE.md's parallel sub-agent
doctrine, this mission landed in a worktree alongside multiple sibling
missions merging into the same release branch concurrently ("five
sibling agents running concurrently" — the harness's own briefing for
this session). Whether `served-mode-is-a-real-mode-01PMZ707` is the LAST
mission to land before the next release tag is a fact about the release
as a whole, not about this mission in isolation, and this WP has no
visibility into the other worktrees' state. **This mission adds no
migration (AC-PI-3), so it carries no upgrade-snapshot obligation of its
own regardless of landing order** — but the release orchestrator (whoever
merges the final worktree into `release/v<X.Y.Z>` and cuts the tag) still
owns running `bash scripts/ci/upgrade-snapshot.sh <new-tag>` for the
RELEASE as a whole if any OTHER sibling mission added a migration.
Flagging the obligation to that place, per this AC's own instruction to
name where it lands rather than let it drop, since this document cannot
discharge it on the release's behalf.
