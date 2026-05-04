# tasks.md — session-auto-titling-01KQ8TDS

Five work packages. Sequenced so data layer (WP01) lands first, generator package (WP02) and settings dials (WP03) fan out in parallel, chat-runner trigger + RPC manual path (WP04) consumes both, frontend (WP05) then integration test (WP06) close out.

```
WP01 ──┬── WP02 ──┐
       │          ├── WP04 ── WP05 ── WP06
       └── WP03 ──┘
```

WP02 and WP03 may run in parallel after WP01.

## WP01 — Schema migration + audit kind + cost-tag constant + manager methods

**Effort:** S · **Dependencies:** none
**Files:**
- `core/session/migrations.go` (register `migration0311()` in `Migrations()` slice)
- `core/session/migrations_auto_titled.go` (new — `migration0311()` adds `auto_titled INTEGER NOT NULL DEFAULT 0`)
- `core/session/migrations_auto_titled_test.go` (new)
- `core/session/types.go` (add `AutoTitled bool` to `Record`)
- `core/session/store.go` (extend `Rename` to set `auto_titled=1` on non-empty rename; add `AutoTitle`, `MarkAutoTitleAttempted`, `ClearTitle` methods on both `memStore` and `sqlStore`)
- `core/session/store_test.go` (new cases)
- `core/session/manager.go` (`AutoTitle`, `MarkAutoTitleAttempted`, `ClearTitle`, `RequestRetitle` methods; new event kind constants `EventKindSessionAutoTitled`)
- `core/session/manager_test.go` (new cases: re-check predicate inside `AutoTitle` drops the write when `auto_titled` flipped)
- `core/context/audit/audit.go` (add `KindSessionAutoTitled` constant + `SessionAutoTitledPayload` struct)
- `core/context/audit/audit_test.go` (JSON round-trip)
- `core/llm/cost/reducer.go` (add `KindAutoTitle = "auto_title"` constant)
- `core/llm/cost/reducer_test.go` (pin constant value)

**Acceptance:**
- Applying migration on existing DB adds column with default 0; no row touched.
- `Manager.AutoTitle(ctx, id, "Rust basics")` writes both name and `auto_titled=1` atomically.
- `Manager.AutoTitle` is no-op when row's `auto_titled` is already 1 (returns `ErrAutoTitleSuperseded`).
- `Manager.ClearTitle(ctx, id)` writes `name=""` and `auto_titled=0`.
- `Rename` with non-empty name flips `auto_titled` from 0 to 1.
- `KindSessionAutoTitled` payload round-trips JSON.

## WP02 — `core/sessions/autotitle/` generator package + LLM wiring adapter

**Effort:** M · **Dependencies:** WP01 (cost-tag constant)
**Files:**
- `core/sessions/autotitle/doc.go` (new)
- `core/sessions/autotitle/generator.go` (new — `Generator`, `LLMCaller`, `GenerateTitle`, prompt constants)
- `core/sessions/autotitle/generator_test.go` (new — fakeLLM table-driven cases)
- `core/sessions/autotitle/validate.go` (new — `Sanitize`, `ErrTitleTooShort`, `ErrModelRefused`)
- `core/sessions/autotitle/validate_test.go` (new — table-driven cases)
- `core/sessions/autotitle/wiring/llm.go` (new — `LLMCaller` adapter; tags calls with `cost.KindAutoTitle`)
- `core/sessions/autotitle/wiring/llm_test.go` (new)

**Acceptance:**
- `GenerateTitle` returns sanitized title for happy-path transcript.
- Oversize model output truncated at 49 runes + ellipsis.
- Undersize model output (<3 runes) returns `ErrTitleTooShort`.
- Model refusal returns `ErrModelRefused`.
- 5-second context timeout cancels in-flight calls.
- Wiring adapter tags every call with `cost.KindAutoTitle`.

## WP03 — Settings dials + per-field RPC accessors

**Effort:** S · **Dependencies:** WP01
**Files:**
- `core/rpc/views/settings/api.go` (add three new fields; `EffectiveAutoTitleEnabled()` accessor; extend `SettingsStore` with six load/save helpers)
- `core/rpc/views/settings/impl.go` (implement three new pairs)
- `core/rpc/views/settings/impl_test.go` (new cases: round-trip; default-value assertions)
- `core/rpc/bindings.go` (add six bindings)
- `frontend/src/lib/harnessClient.ts` (add six bindings to typed client)
- `frontend/src/views/settings/AutoTitleSettings.vue` (new — three controls)
- `frontend/src/views/settings/__tests__/AutoTitleSettings.spec.ts` (new)

**Acceptance:**
- `EffectiveAutoTitleEnabled()` returns `true` on freshly-installed Settings.
- Round-trip persists three fields through `SaveAll`/`LoadAll`.
- Per-field accessors round-trip independently.
- Frontend Settings panel renders, toggles, and persists each field.

## WP04 — Chat-runner trigger + manual `Sessions_SuggestTitle` / `Sessions_ClearTitle` RPC

**Effort:** M · **Dependencies:** WP01, WP02, WP03
**Files:**
- `core/rpc/views/agentgraph/chat/chat_runner.go` (extend `Config` with `AutoTitle *AutoTitleDeps`; add `fireAutoTitle` method; predicate-then-fire async with 5s timeout; emits `KindSessionAutoTitled`)
- `core/rpc/views/agentgraph/chat/chat_runner_test.go` (new cases: trigger fires once; skipped on disabled dial; skipped on workflow_run with `AutoTitleWorkflowRuns=false`; race with user rename drops cleanly; failure path emits audit)
- `core/rpc/api.go` (extend `buildChatRunner` to wire `AutoTitleDeps`; construct `autotitle.Generator` + wiring adapter)
- `core/rpc/views/sessions/api.go` (extend `SessionsAPI` with `SuggestTitle`, `ClearTitle`; extend `Session` with `AutoTitled bool`)
- `core/rpc/views/sessions/impl.go` (`SuggestTitle` calls `Manager.RequestRetitle`; `ClearTitle` calls `Manager.ClearTitle`)
- `core/rpc/views/sessions/impl_test.go` (new cases)
- `core/rpc/bindings.go` (add `Sessions_SuggestTitle`, `Sessions_ClearTitle`)
- `core/rpc/stubs.go` (stub implementations)

**Acceptance:**
- First eligible chat run fires exactly one auto-title call.
- Disabled dial skips trigger entirely.
- Race test: user `Rename` between trigger schedule and fire causes auto-title write to no-op.
- Failure path emits `KindSessionAutoTitled` with `error_kind` and leaves session name untouched.
- `Sessions_SuggestTitle` happy path overwrites name + `auto_titled=1` + emits audit with `trigger="manual"`.
- `Sessions_ClearTitle` writes empty name + `auto_titled=0` + emits `session.renamed` event.

## WP05 — Frontend rail distinction + "Suggest new title" affordance + clear-on-empty rename

**Effort:** S · **Dependencies:** WP04
**Files:**
- `frontend/src/lib/useSession.ts` (add `suggestTitle(id)` and `clearTitle(id)` actions; add `autoTitled` to view-model)
- `frontend/src/lib/__tests__/useSession.test.ts` (new cases)
- `frontend/src/shell/LeftRail.vue` (read `session.autoTitled`; apply `session-row__name--auto` class; modify `commitRename` to call `clearTitle` when input empty)
- `frontend/src/shell/__tests__/LeftRail.spec.ts` (extend)
- `frontend/src/components/chat/SessionHeader.vue` (new — title + "Suggest new title" button; confirm-overwrite modal when title non-empty + non-auto)
- `frontend/src/components/chat/__tests__/SessionHeader.spec.ts` (new)
- `frontend/src/views/sessions/SessionsView.vue` (mount `SessionHeader` above message-list)
- `frontend/src/styles/sessions.css` (`.session-row__name--auto { font-style: italic; opacity: 0.85; }`)

**Acceptance:**
- Auto-titled rail rows render italic + muted.
- Tooltip shows "Auto-generated title — click to edit" on hover.
- Empty-string rename submission clears title (calls `clearTitle`).
- "Suggest new title" header button triggers fresh title generation.
- Confirm-overwrite modal appears when title is non-empty + non-auto-titled.

## WP06 — Integration test + smoke harness

**Effort:** S · **Dependencies:** WP01–WP05
**Files:**
- `core/rpc/views/agentgraph/chat/chat_runner_integration_test.go` (extend with auto-titling scenarios: happy path, dial off, failure path, manual re-trigger)
- `docs/dev/auto-titling.md` (new — operator-facing notes)

**Acceptance:**
- All four scenarios green.
- Smoke checklist (plan §4.2) walked manually before merge.
- `go test ./core/rpc/views/agentgraph/chat/...` green end-to-end.
