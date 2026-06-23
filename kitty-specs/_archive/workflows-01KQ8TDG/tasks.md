# tasks.md — workflows-01KQ8TDG

## Work packages

### WP01 — YAML schema, loader, validator
- **Effort**: M
- **Dependencies**: none
- **Files touched**:
  - `core/workflows/types.go` (new) — `Workflow`, `Step`, `Input`, `TypedValue`, `ValueType` enums.
  - `core/workflows/schema.go` (new) — closed-enum kind validators, type-compat table, `${input.x}` / `${step.x.output}` ref grammar.
  - `core/workflows/loader.go` (new) — YAML→`Workflow` parsing (uses `gopkg.in/yaml.v3`), per-field validation, file-size cap (256 KiB).
  - `core/workflows/refs.go` (new) — single-pass token scanner for ref expressions.
  - `core/workflows/loader_test.go` — tableized fixtures (one valid, one of each rejection class).
- **Acceptance**:
  - Valid 3-step YAML round-trips through Marshal/Unmarshal.
  - `inline_run:true` + multi-step rejects with named error.
  - `${step.X.output}` referencing a later step rejects.
  - Type mismatch (write_artifact.content_ref pointed at bytes-only output) rejects.
  - File >256 KiB rejects.

### WP02 — Engine + RunContext + step-output composition
- **Effort**: L
- **Dependencies**: WP01
- **Files touched**:
  - `core/workflows/engine.go` (new) — `Engine`, `Run`, `RunOptions`, sequential executor, broker progress publication, ctx cancellation.
  - `core/workflows/runcontext.go` (new) — `RunContext`, output store, ref resolver hook.
  - `core/workflows/step_runner.go` (new) — `StepRunner` interface + registry-by-kind.
  - `core/workflows/engine_test.go` — fakes for LLM/Tools/MCP/HTTP/Artifacts; sequential ordering + cancellation tests.
- **Acceptance**:
  - 3-step run executes in declared order; outputs visible in subsequent steps via `${step.x.output}`.
  - Cancellation mid-step surfaces `status=interrupted` and aborts subsequent steps.
  - Broker topic `workflow:run-progress` receives one event per transition.

### WP03 — model_turn + tool_call step impls
- **Effort**: M
- **Dependencies**: WP02
- **Files touched**:
  - `core/workflows/steps/model_turn.go` (new) — wraps `core/llm/registry.Registry.Stream`; allow_tools allowlist enforcement.
  - `core/workflows/steps/tool_call.go` (new) — dispatches via the existing tool catalog.
  - `core/workflows/steps/model_turn_test.go`, `tool_call_test.go`.
- **Acceptance**:
  - model_turn step honors workflow's declared provider profile, falls back to user default.
  - tool_call gates via Cedar; deny surfaces `cedar.PolicyDeniedError`.
  - allow_tools restricts the model's visible tool set for that turn.

### WP04 — http_request + mcp_call + shell step impls
- **Effort**: M
- **Dependencies**: WP02
- **Files touched**:
  - `core/workflows/httpclient.go` (new) — bounded-timeout client, body cap, JSON auto-decode.
  - `core/workflows/steps/http_request.go` (new) — host extraction → Cedar HTTPHost resource.
  - `core/workflows/steps/mcp_call.go` (new) — calls `core/mcp/stdio` server pool's CallMethod.
  - `core/workflows/steps/shell.go` (new) — delegates to `core/tools/bash`.
  - `*_test.go` for each.
- **Acceptance**:
  - http_request: localhost denied by default policy; oversize body rejected; JSON Content-Type auto-decodes.
  - mcp_call: server-unavailable surfaces typed error.
  - shell: timeout enforced; bash builtin sandbox semantics preserved.

### WP05 — read_artifact + write_artifact + transform + conditional step impls
- **Effort**: M
- **Dependencies**: WP02, WP01
- **Files touched**:
  - `core/workflows/steps/read_artifact.go` (new).
  - `core/workflows/steps/write_artifact.go` (new) — adds `SourceWorkflowOutput` constant in `core/artifacts/artifact.go`.
  - `core/workflows/steps/transform.go` (new) — jq/jsonpath/regex/template via vendored libs.
  - `core/workflows/steps/conditional.go` (new) — predicate evaluator + branch executor.
  - `core/artifacts/artifact.go` (modified) — new source constant.
  - `*_test.go` for each.
- **Acceptance**:
  - write_artifact captures and surfaces `artifact_id`; failure leaves no half-written row.
  - read_artifact returns bytes + mime + title.
  - transform.jq evaluates a non-trivial jq expression; transform.template uses Go text/template syntax.
  - conditional with predicate `matches` selects the right branch.

### WP06 — Storage + versioning + share / import
- **Effort**: M
- **Dependencies**: WP01
- **Files touched**:
  - `core/workflows/storage.go` (new) — global + project loaders, atomic save (tmp + rename), per-id mutex.
  - `core/workflows/versioning.go` (new) — `_history/<id>/v<n>.yaml` writer + janitor goroutine.
  - `core/workflows/builtin/` (new empty dir; `//go:embed builtin/*.yaml` declared but matches zero).
  - `core/workflows/storage_test.go` — concurrent save serialization, version-bump correctness, history pruning.
- **Acceptance**:
  - Two concurrent Save calls for same id serialize cleanly; version increments by exactly 2.
  - History file written for the prior version on every save.
  - Janitor removes >90-day history files only.
  - Project workflow shadows global with same id.

### WP07 — RPC surface (List/Get/Save/Delete/Run)
- **Effort**: M
- **Dependencies**: WP02, WP06
- **Files touched**:
  - `core/rpc/views/workflows/api.go` (new) — typed wire DTOs + handler bindings.
  - `core/rpc/views/workflows/impl.go` (new) — calls into `core/workflows`.
  - `core/rpc/views/workflows/slash.go` (new) — stub `ListSlashCommands`, `ResolveSlash`.
  - `core/rpc/api.go` (modified) — register Workflows view; broker subscription for `workflow:run-progress`.
  - `core/rpc/bindings.go` (modified) — TS-binding generation marker for the new methods.
  - `core/rpc/emitter.go` / `emitter_test.go` (modified) — extend privacy-CI allowlist for the four new audit kinds.
  - `core/rpc/views/workflows/api_test.go`.
- **Acceptance**:
  - `List/Get/Save/Delete/Run` exercised via existing RPC test harness.
  - Privacy CI (`core/rpc/emitter_test.go`) passes after allowlist extension.
  - `HARNESS_WORKFLOWS=0` makes all five methods return `ErrFeatureDisabled`.

### WP08 — inline_run dispatch + rerun_policy handling
- **Effort**: M
- **Dependencies**: WP02, WP07
- **Files touched**:
  - `core/workflows/dispatch.go` (new) — `runInline` vs `runSpawned` dispatcher.
  - `core/workflows/rerun.go` (new) — `ErrRerunPolicyAsk`, prior-run lookup, version-pin resolution.
  - `core/session/types.go` (modified) — add `MetaWorkflowID`, `MetaWorkflowVersion`, `MetaParentSessionID` JSON fields on the metadata blob; add `KindWorkflowRun` constant.
  - `core/session/store.go` (modified) — query helpers `MostRecentWorkflowRun(workflowID, parentID, withinDays)`.
  - `core/workflows/dispatch_test.go`, `rerun_test.go`.
- **Acceptance**:
  - inline_run:true workflow appends user+assistant messages to caller session; no new session row.
  - inline_run:false spawns a `kind=workflow_run` session with metadata populated.
  - rerun_policy=continue resumes the prior session and loads the YAML version that matches the original run, not the head version.
  - rerun_policy=ask returns the typed error envelope; an explicit RerunMode bypasses it.

### WP09 — Frontend WorkflowsView + WorkflowEditor + SimpleTemplateEditor
- **Effort**: L
- **Dependencies**: WP07
- **Files touched**:
  - `frontend/src/views/workflows/WorkflowsView.vue` (replace stub) — list + actions.
  - `frontend/src/views/workflows/SimpleTemplateEditor.vue` (new).
  - `frontend/src/views/workflows/WorkflowEditor.vue` (new) — three-column editor.
  - `frontend/src/views/workflows/StepFormModelTurn.vue`, `StepFormToolCall.vue`, `StepFormHttpRequest.vue`, `StepFormMcpCall.vue`, `StepFormShell.vue`, `StepFormReadArtifact.vue`, `StepFormWriteArtifact.vue`, `StepFormTransform.vue`, `StepFormConditional.vue` (nine new files).
  - `frontend/src/views/workflows/HistoryDropdown.vue` (new).
  - `frontend/src/views/workflows/RunInputModal.vue` (new) — typed input form.
  - `frontend/src/views/workflows/__tests__/*.spec.ts` — coverage for editor save-blocking on validation errors + run-input population.
- **Acceptance**:
  - Drag-to-reorder updates YAML preview live.
  - Save is blocked while validation errors exist.
  - History dropdown lists prior versions and restores in-place.
  - Import-from-clipboard validates and surfaces errors with line/col.

### WP10 — Sidebar Workflow runs section
- **Effort**: S
- **Dependencies**: WP08
- **Files touched**:
  - `frontend/src/shell/LeftRail.vue` (modified) — new collapsible section grouped by workflow_id.
  - `frontend/src/shell/__tests__/LeftRail.workflow-runs.spec.ts` (new).
  - `frontend/src/composables/useWorkflowRuns.ts` (new) — subscribes to broker topic, derives grouped state.
- **Acceptance**:
  - Section hidden when no workflow_run sessions exist.
  - Continue-resumed run updates the existing row's `•` indicator without spawning a new row.
  - Group collapse state persists across reloads.

### WP11 — Cedar gate + audit emission + integration tests
- **Effort**: M
- **Dependencies**: WP02–WP05, WP07
- **Files touched**:
  - `core/context/audit/audit.go` (modified) — four new `Kind` constants + `WorkflowStepPayload` struct.
  - `core/policy/cedar/policies/default_policy.cedar` (modified) — sane defaults for new actions; HTTPHost forbid list (localhost, link-local, metadata).
  - `core/workflows/cedar.go` (new) — per-step gate helpers + `requires_policy` pre-flight check.
  - `core/workflows/audit.go` (new) — emission helpers.
  - `core/workflows/integration_test.go` (new) — end-to-end runs covering all nine in-scope step kinds with Cedar gating + audit assertions.
- **Acceptance**:
  - Each in-scope step kind emits exactly two events on success (started + completed) and exactly one on failure (failed). Plus one `run_completed`.
  - `requires_policy: missing_name` returns `ErrRequiredPolicyMissing` at Run entry.
  - http_request to `localhost` denied by default policy with structured `PolicyDeniedError`.
  - Privacy CI: assert no payload field carries step inputs / outputs / prompt bytes.

## Sequencing diagram

```
            ┌──────────┐
            │  WP01    │  schema + loader
            └────┬─────┘
                 │
        ┌────────┴────────┐
        │                 │
   ┌────▼────┐        ┌───▼─────┐
   │  WP02   │        │  WP06   │  storage + versioning
   │ engine  │        └────┬────┘
   └────┬────┘             │
        │                  │
   ┌────┼──────────────────┤
   │    │                  │
┌──▼┐ ┌─▼─┐ ┌──┐         ┌─▼─┐
│03 │ │04 │ │05│         │   │
│m_t│ │htt│ │ar│         │   │
│t_c│ │mcp│ │tr│         │   │
│   │ │sh │ │co│         │   │
└─┬─┘ └─┬─┘ └┬─┘         │   │
  │     │    │           │   │
  └──┬──┴────┘           │   │
     │                   │   │
   ┌─▼───────────────────▼─┐ │
   │       WP07 RPC        │ │
   └───────┬────┬──────────┘ │
           │    │            │
       ┌───▼──┐ │            │
       │ WP08 │ │            │
       │ inl/ │ │            │
       │ rer  │ │            │
       └─┬────┘ │            │
         │      │            │
   ┌─────┴──────┼────┐       │
   │            │    │       │
┌──▼──┐    ┌────▼─┐  │       │
│WP09 │    │ WP10 │  │       │
│ FE  │    │ rail │  │       │
└──┬──┘    └──────┘  │       │
   │                 │       │
   └─────────┬───────┘       │
             │               │
         ┌───▼────┐          │
         │  WP11  │ Cedar+audit+integration
         └────────┘
```

Parallelism notes:
- WP02 and WP06 run in parallel after WP01 (separate packages, separate test fixtures).
- WP03/WP04/WP05 run in parallel after WP02 (each touches its own `steps/<kind>.go`).
- WP09 and WP10 run in parallel after WP07/WP08 (separate frontend areas).
- WP11 lands last (touches the audit kinds + cedar policies that the integration tests assert on).

### Critical Files for Implementation
- /Users/alecfeeman/PycharmProjects/kenaz-harness/core/workflows/engine.go (new — central executor)
- /Users/alecfeeman/PycharmProjects/kenaz-harness/core/workflows/loader.go (new — YAML schema + validator)
- /Users/alecfeeman/PycharmProjects/kenaz-harness/core/rpc/views/workflows/api.go (new — RPC surface bridging frontend to engine)
- /Users/alecfeeman/PycharmProjects/kenaz-harness/core/workflows/dispatch.go (new — inline_run vs runSpawned + rerun_policy)
- /Users/alecfeeman/PycharmProjects/kenaz-harness/frontend/src/views/workflows/WorkflowEditor.vue (new — primary author UX)
