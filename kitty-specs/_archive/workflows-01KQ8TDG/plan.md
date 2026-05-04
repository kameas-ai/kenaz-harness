# plan.md — workflows-01KQ8TDG

## Branch contract

| Field | Value |
|---|---|
| Branch | `mission/workflows-01KQ8TDG` |
| Base | `main` |
| Merge gate | Green CI (Go + Vue test suites + privacy CI), ≥1 reviewer, mission-acceptance smoke (§Rollout) executed on a clean DataDir |
| Public Go API additions | `core/workflows.Engine`, `core/workflows.Workflow`, `core/workflows.Step`, `core/workflows.Input`, `core/workflows.Run`, `core/workflows.RunContext`, `core/workflows.TypedValue`, `core/workflows.ErrRerunPolicyAsk`, RPC layer `core/rpc/views/workflows.{List,Get,Save,Delete,Run}` |
| Feature flag | `HARNESS_WORKFLOWS` env var, default **on**. When off: RPC methods return `ErrFeatureDisabled`; sidebar + view hide. |
| Soft deps | `user-slash-commands-01KQ8TDK` (consumes `slash_command` field on workflow YAML; this mission ships the wire shape but not the slash-command dispatcher itself); `multimodal-io-01KQ8TDF` (input kind `file` borrows the file-picker contract — accepted as a string ref, picker integration is a no-op fallback if multimodal-io has not landed) |

## Architecture

### 1. Workflow YAML schema

Authoritative schema (validated at `Workflows.Save` time and at engine load). One YAML file per workflow.

```yaml
id: release_notes              # required, kebab-case, globally unique
name: "Release notes"          # required, human label
description: "..."             # optional
version: 7                     # required, sequential int (engine bumps on save)
inline_run: false              # optional; default = true iff steps.length == 1 && steps[0].kind == model_turn
rerun_policy: fresh            # optional; one of fresh|continue|ask. Default fresh. Ignored when inline_run: true.
slash_command: "/release-notes" # optional; consumed by user-slash-commands-01KQ8TDK
requires_policy: workflow.release_notes # optional; pre-flight Cedar check at Run time
inputs:
  - name: since_ref
    kind: string               # one of: string|multiline|enum|file|artifact_ref|project_ref
    required: true
    default: "main~10"
    validation: { pattern: "^[a-zA-Z0-9_./~^-]+$" }
  - name: tone
    kind: enum
    options: ["formal","casual"]
    default: "formal"
steps:
  - name: gather_commits
    kind: shell
    cmd: "git"
    args: ["log","--oneline","${input.since_ref}..HEAD"]
    timeout_ms: 5000
  - name: summarize
    kind: model_turn
    user_prompt: |
      Summarize these commits in a ${input.tone} tone:
      ${step.gather_commits.output.stdout}
    allow_tools: []
  - name: save
    kind: write_artifact
    title: "Release notes for ${input.since_ref}"
    content_ref: "${step.summarize.output.text}"
    mime_type: "text/markdown"
```

**Field rules**:
- `id`: regex `^[a-z][a-z0-9_-]{0,63}$`. Filename is `<id>.yaml` exactly.
- `version`: write-only from engine; rejected on incoming `Save` if it disagrees with stored+1 (concurrency check).
- `inline_run`: load-time validator REJECTS `inline_run: true` when `len(steps) != 1` or `steps[0].kind != "model_turn"`. (Risk register row.)
- `inputs[].kind`: closed enum. `file`/`artifact_ref`/`project_ref` carry an opaque ID string at runtime; the engine does NOT dereference them, individual step impls do.
- File size cap NFR-003: 256 KiB; enforced at `Save`.

**Per-step examples** (`kind` discriminator):

| Kind | Required fields | Output shape |
|---|---|---|
| `model_turn` | `user_prompt` | `{text, tool_calls?, usage}` |
| `tool_call` | `tool_name`, `args` (map) | `{result, is_error, duration_ms}` |
| `mcp_call` | `server_id`, `method`, `params` | `{result, is_error}` |
| `http_request` | `method`, `url`; opt `headers/body/query/auth/timeout_ms` | `{status, headers, body_text, body_json?}` |
| `shell` | `cmd`, `args[]`; opt `cwd, timeout_ms, env_allowlist` | `{stdout, stderr, exit_code}` |
| `read_artifact` | `artifact_id_ref` (resolves a `${...}` or input ref) | `{bytes, mime_type, title}` |
| `write_artifact` | `title`, `content_ref`, opt `mime_type` | `{artifact_id}` |
| `transform` | `kind: jq\|jsonpath\|regex\|template`, `expr`, `input_ref` | typed per `kind` |
| `conditional` | `predicate{step_ref,op,value}`, `then[]`, `else[]` | output of last executed branch step |

### 2. Step-kind implementations

Each step impl lives at `core/workflows/steps/<kind>.go` and satisfies a `StepRunner` interface:

```go
type StepRunner interface {
    Validate(step Step, prior []Step) error  // load-time
    Run(ctx context.Context, step Step, rc *RunContext) (TypedValue, error)
}
```

Per kind:

- **model_turn** — calls `core/llm/registry.Registry.Stream`. Cedar `action="model_turn", resource=ProviderProfile::"<id>"`. Audit emits without prompt bytes (privacy CI). Errors: `ErrLLMTransport`, `ErrLLMRateLimited`, `ErrPolicyDenied`.
- **tool_call** — dispatches through the existing tool catalog (the chat runner's dispatch table). Cedar `action="tool_call", resource=Tool::"<name>"`. Output mirrors what the chat runner sees today.
- **mcp_call** — calls `core/mcp/stdio` server pool's `CallMethod(serverID, method, params)`. Cedar `action="mcp_call", resource=MCPServer::"<server_id>"`. Errors include server-unavailable, method-unknown.
- **http_request** — new `core/workflows/httpclient.go` (stdlib `net/http` + bounded timeout, default 15s, max 60s). Cedar `action="http_request", resource=HTTPHost::"<scheme>://<host>"` (host extraction handles port). Body capped at 5 MiB; oversize returns `ErrHTTPBodyTooLarge`. JSON-decoded into `body_json` only when `Content-Type` matches `application/json`.
- **shell** — delegates to `core/tools/bash` (existing builtin). Cedar gate identical to bash's normal gate (`action="tool_call", resource=Tool::"bash"`). No new sandbox.
- **read_artifact** — calls existing artifact-fetch RPC backend (`core/artifacts.Manager.Get`). Cedar `action="read_artifact", resource=Artifact::"<id>"`.
- **write_artifact** — calls `core/artifacts.Manager.Capture` with `Source: SourceWorkflowOutput` (new constant). Cedar `action="write_artifact", resource=Project::"<id>"|Loose`. Atomic: capture either commits or fails — never half-writes (mitigates cancellation row in risk register).
- **transform** — pure Go, no I/O. `jq` via `github.com/itchyny/gojq`, `jsonpath` via `github.com/PaesslerAG/jsonpath`, `regex` via stdlib, `template` via `text/template`. NO Cedar gate (data-only). NO audit emission besides the generic step events.
- **conditional** — evaluates predicate against `${step.<step_ref>.output}`; executes `then[]` or `else[]` branch sequentially. NO Cedar gate at the conditional itself; child steps gate normally.

Common error semantics: a step that returns a non-nil `error` aborts the run; engine emits `workflow.step_failed` and `workflow.run_completed{status:"failed"}`. Cedar denials surface as `cedar.PolicyDeniedError`.

### 3. Step output composition

`RunContext` (in-memory, lifetime = one run):

```go
type RunContext struct {
    RunID         string
    Workflow      Workflow
    Inputs        map[string]TypedValue
    StepOutputs   map[string]TypedValue   // keyed by step.name
    ParentSession string
    Cancel        context.CancelFunc
}

type TypedValue struct {
    Type ValueType  // text|json|bytes|artifact_id|error
    Text string
    JSON any        // map[string]any | []any | scalar
    Bytes []byte
    ArtifactID string
}
```

Reference resolution uses `${input.<name>}` and `${step.<name>.output[.<jsonpath>]}`. Resolver is a single-pass token scanner in `core/workflows/refs.go`. Type compat is checked at YAML load against a static type table per step kind: e.g. `write_artifact.content_ref` accepts `text|json|bytes`; `http_request.body` accepts `text|json|bytes`. Mismatches surface as `Save` errors with `{step_name, ref_path, expected, actual}`.

JSON edge case (risk register): an `http_request` returning JSON-shaped text but `Content-Type: text/plain` does NOT auto-decode. Author must use a `transform{kind:"jq"}` step to coerce, or set the upstream content-type. Documented in editor hint.

### 4. Engine

`core/workflows/engine.go`:

```go
type Engine struct {
    LLM      *llmregistry.Registry
    MCP      *mcpstdio.Pool
    Tools    toolloop.Dispatcher
    Artifacts *artifacts.Manager
    Cedar    *cedar.Engine
    Audit    audit.Emitter
    Sessions *session.Manager
    Broker   *rpc.StreamBroker
    HTTP     *workflows.HTTPClient
}

func (e *Engine) Run(ctx context.Context, wf Workflow, inputs map[string]TypedValue, opts RunOptions) (*Run, error)
```

Compiles YAML → in-memory step graph at load time (called once per `Save` and once per first `Run` after reload). Executes sequentially; `conditional` is the only branching primitive in scope. Each step boundary:

1. Cedar pre-check.
2. Emit `workflow.step_started`.
3. Resolve refs from `RunContext`.
4. Invoke `StepRunner.Run`.
5. Persist output to `RunContext.StepOutputs`.
6. Emit `workflow.step_completed` (or `_failed`).
7. Publish progress event on broker topic `workflow:run-progress` (one event per step transition).

Cancellation: ctx.Done propagates; in-flight HTTP/LLM requests are canceled via their own ctx; partial multi-step state remains in the `workflow_run` session marked `status=interrupted`.

### 5. Storage

- Global: `<DataDir>/workflows/*.yaml`.
- Project: `<DataDir>/projects/<project_id>/workflows/*.yaml`.
- Loader walks both. Project workflows shadow global on `id` collision.
- Versioning: every `Save` writes the new YAML, then copies the previous version (if any) to `<DataDir>/workflows/_history/<id>/v<n>.yaml`. A daily janitor goroutine prunes history older than 90 days.
- Save is atomic: write to `<id>.yaml.tmp` → fsync → rename. Concurrent saves are serialized via a per-id mutex inside the storage package.
- Built-in starter set: this mission ships an EMPTY `core/workflows/builtin/` directory (the `//go:embed` glob exists but matches zero files). The follow-up mission `workflows-extended-01KQ8TDN` ships the actual content.

### 6. RPC surface

Lives at `core/rpc/views/workflows/api.go` (matches existing `views/sessions` pattern). Bound onto `API` struct in `core/rpc/api.go`.

```go
Workflows.List(ctx, projectID *string) ([]Summary, error)
Workflows.Get(ctx, id string) (Workflow, error)
Workflows.Save(ctx, w Workflow) (Workflow, error)              // returns w with bumped version
Workflows.Delete(ctx, id string) error
Workflows.Run(ctx, id string, inputValues map[string]any, opts RunOpts) (RunResult, error)
```

`RunOpts` carries `{ParentSessionID string, RerunMode "" | "fresh" | "continue"}`. `RunResult = {SessionID string, InlineMessageID string, RerunAsk *RerunAsk}`. When the engine returns `ErrRerunPolicyAsk`, the RPC packages the prior session ID + date into `RerunAsk` and returns nil error.

Cross-mission seam: `ListSlashCommands(ctx) []SlashCommand` and `ResolveSlash(ctx, name string) (Workflow, error)` are declared as wire shapes in `core/rpc/views/workflows/slash.go` BUT the dispatcher implementation is owned by `user-slash-commands-01KQ8TDK`. This mission stubs `ResolveSlash` to look up `slash_command` field on workflows.

### 7. inline_run vs spawn-session execution paths

```go
func (e *Engine) Run(ctx, wf, inputs, opts) (*Run, error) {
    if wf.InlineRun {
        return e.runInline(ctx, wf, inputs, opts.ParentSessionID)
    }
    return e.runSpawned(ctx, wf, inputs, opts)
}
```

- **runInline**: requires `opts.ParentSessionID`. Treats the (single) `model_turn` step as the next user→assistant turn in that session: appends a synthetic user message (input expansion) + invokes LLM + appends assistant message. Tool/http/etc. steps are NOT permitted in inline workflows (validator enforces). UI shows a small "Workflow `<name>` running…" inline indicator via the `workflow:run-progress` topic.
- **runSpawned**: creates a new session (`kind="workflow_run"`, `metadata={workflow_id, workflow_version, parent_session_id?}`). Each step appears as a `Role=System` synthetic message with `{step_name, step_kind, output_summary}`. Inputs and outputs full bytes are NOT in messages — those live in `RunContext` (memory) + on-disk artifacts only, satisfying privacy-CI's audit constraints.

### 8. rerun_policy implementation

Resolution at `Workflows.Run` entry, before engine dispatch (only when `wf.InlineRun == false`):

```
switch opts.RerunMode {
case "fresh":
    spawn new session
case "continue":
    look up most-recent prior workflow_run session WHERE workflow_id=<id>
                                                   AND parent_session_id=<caller>
                                                   AND created_at >= now-30d
    if found: resume in that session (engine sees it as fresh run, but the session row + sidebar entry is reused)
    if not found: spawn new session (graceful fallback)
case "":
    apply wf.RerunPolicy:
      fresh    -> spawn new
      continue -> follow continue path above
      ask      -> if a prior run exists in 30d window, return ErrRerunPolicyAsk{LastRunSessionID, LastRunDate}
                  frontend renders modal; user picks; FE re-calls Run with explicit RerunMode
}
```

Workflow YAML changing between runs (risk register): when `continue` reuses a session, the engine reads the **stored session metadata's `workflow_version`** and loads THAT version from `_history/<id>/v<n>.yaml` (not the current head). This guarantees deterministic resume against the originally-committed version.

### 9. Sidebar UX

`frontend/src/shell/LeftRail.vue` extension:
- New top-level section "Workflow runs" rendered below "Sessions". Hidden when no `workflow_run` sessions exist.
- Sections grouped by `metadata.workflow_id`. Group header shows workflow name + run count + collapse caret. Persistent collapse state in localStorage (`leftRail.workflowRuns.<id>.collapsed`).
- Each row: workflow run timestamp, status icon (`spinner | check | x | dot-interrupted`), opens session on click.
- Continue-resumed row: when `Workflows.Run` returns the SAME session ID that already exists in the rail, the row gets a transient `•` indicator (resets after 5s) — no new row.
- `inline_run: true` invocations are invisible in the rail (they're chat turns).

### 10. Workflow author UI

Two surfaces under `frontend/src/views/workflows/`:

- **`SimpleTemplateEditor.vue`** — single-step shortcut. Form: `{name, description, system_prompt?, user_prompt, slash_command?, allow_tools[]?, inline_run: bool}`. Auto-builds a one-step `model_turn` workflow YAML in memory, posts to `Workflows.Save`. Coordinates with `user-slash-commands-01KQ8TDK` (slash field is the integration point).
- **`WorkflowEditor.vue`** — full multi-step. Three columns: (a) step list with drag-handle reorder + add-step picker, (b) per-step type-discriminated form (one Vue component per step kind: `StepFormModelTurn.vue`, `StepFormToolCall.vue`, etc.), (c) live YAML preview using `monaco-editor` (already in the bundle for other panels). Validation hints inline as red text under offending fields; save is blocked while errors exist. History dropdown lists prior versions; selecting one rolls the editor to that snapshot (rollback is a `Save` of the snapshot bytes, which itself bumps version).
- `WorkflowsView.vue` (replace the stub): list + filter by project + "New simple template" / "New workflow" / "Import from clipboard" / per-row "Run", "Edit", "Share", "Delete", "History".

### 11. Cedar policy gate per step

- Per step kind, the engine queries Cedar with a fixed `(action, resource)` shape (table in §2). Principal is `Agent::"workflow_runner_<workflow_id>"` (one synthetic agent identity per workflow so policies can target workflow names directly).
- Workflow-level `requires_policy: <name>` is a pre-flight check at `Workflows.Run` entry: the engine asserts that a Cedar policy with `@name("<name>")` annotation exists in the loaded bundle. Missing → `ErrRequiredPolicyMissing` returned to frontend with policy name.
- C-003 invariant: workflows can only RESTRICT, never expand. The Cedar gate uses the same engine instance the chat surface uses — there is no second policy bundle. Expansion attempts manifest as denials at step time.

### 12. Audit emission

Four new kinds in `core/context/audit/audit.go`:
- `KindWorkflowStepStarted   Kind = "workflow.step_started"`
- `KindWorkflowStepCompleted Kind = "workflow.step_completed"`
- `KindWorkflowStepFailed    Kind = "workflow.step_failed"`
- `KindWorkflowRunCompleted  Kind = "workflow.run_completed"`

Payload (all four share a struct):

```go
type WorkflowStepPayload struct {
    RunID        string `json:"run_id"`
    WorkflowID   string `json:"workflow_id"`
    Version      int    `json:"workflow_version"`
    StepName     string `json:"step_name,omitempty"`
    StepKind     string `json:"step_kind,omitempty"`
    DurationMS   int64  `json:"duration_ms,omitempty"`
    ErrorKind    string `json:"error_kind,omitempty"`
}
```

Privacy-CI invariant: NO step inputs, prompts, or outputs in audit payloads. The `core/rpc/emitter.go` privacy CI rule already covers stream-broker payload shape; we extend its allowlist to include these four kinds and the schema above (CI test in `core/rpc/emitter_test.go`).

### 13. Versioning + share / import

- Versions: every `Save` increments `version`, copies the prior file to `_history/<id>/v<n>.yaml`. Editor history dropdown calls `Workflows.GetVersion(id, n)` (new RPC) for read-only diff display + "Restore this version" button.
- Share: editor "Share" copies current YAML bytes to clipboard via `navigator.clipboard.writeText`. No file-system involvement.
- Import: "Import from clipboard" reads clipboard text → `Workflows.Save` (which validates). Validation errors render in a modal with line/col references.

### 14. Cross-mission seams

- `user-slash-commands-01KQ8TDK`: consumes `Workflows.ResolveSlash(name)` + the `slash_command` field on workflow YAML. This mission ships the field + RPC stub; the slash dispatcher itself is in that mission.
- `branch-as-subagent-recommendation-01KQ8TDJ`: anticipates `invoke_workflow` step kind for chaining (deferred to `workflows-extended-01KQ8TDN`).
- `multimodal-io-01KQ8TDF`: input kind `file` borrows the file-picker contract. Defaults to a string-id fallback when multimodal-io picker is absent.

## Risk register

| Risk | Mitigation |
|---|---|
| Step output composition typing edge cases (JSON-shaped text vs binary on http_request) | Type table per kind; `transform` step is the documented coercion path. Editor surfaces type mismatches inline at save time. |
| Cedar policy explosion (every step kind = new action) | Step actions enumerated in a fixed list; tests assert no dynamic action names; Cedar starter policy ships sane defaults that allow all step kinds at user scope. |
| `continue` rerun_policy + workflow YAML changed between runs | Continue resume uses the workflow_version stored in session metadata, loaded from `_history/<id>/v<n>.yaml`. Documented in editor as "continued runs pin to original version". |
| Engine cancellation mid-step | `write_artifact` is atomic (commit-or-fail). Other side-effecting steps surface their own partial state via the `workflow_run` session messages; the run is marked `status=interrupted`. No global rollback semantics. |
| `shell` step + sandbox correctness | Delegate fully to `core/tools/bash` builtin. Don't invent a new sandbox. Workflow shell step's allowed-cwd / env-allowlist defaults inherit from bash builtin. |
| `http_request` to internal hosts (localhost, 127.0.0.1, link-local, metadata services) | Cedar `HTTPHost` resource lets policies forbid host patterns. Default policy bundle adds a forbid for `localhost`, `127.0.0.1`, `169.254.*`, `metadata.google.internal`, `[::1]` — overridable by user. |
| MCP server unavailable mid-run | `mcp_call` step returns typed `ErrMCPUnavailable`; engine treats it as a step failure and emits `step_failed`. The `conditional` kind lets authors fall back. |
| Sidebar sprawl when running same workflow many times | Group by workflow_id with collapsible header (FR-008a). Default collapsed when run count > 5. |
| `inline_run: true` workflow with multi-step structure | Load-time validator REJECTS this combination at `Save`. Save error references FR-006b explicitly. |
| MCP method names colliding with tool catalog names | `tool_call` routes ONLY through the catalog; `mcp_call` requires explicit `server_id` — distinct surfaces by design. |

## Rollout

- Feature flag: `HARNESS_WORKFLOWS=1` default on. To disable: env var `HARNESS_WORKFLOWS=0`. RPC returns `ErrFeatureDisabled` and frontend hides nav slot.
- Acceptance smoke (manual checklist on a clean DataDir):
  1. Open Workflows view → verify empty state. Click "New simple template" → save a `summarize` workflow with a one-line `user_prompt: Summarize ${input.text}`. Run from chat → verify inline output appears as next assistant turn.
  2. Click "New workflow" → build a 3-step (`http_request` → `transform` → `write_artifact`) flow against a public JSON endpoint. Save → Run → verify workflow_run session appears in sidebar under "Workflow runs", artifact captures.
  3. Edit a step that introduces a type mismatch (e.g. point `write_artifact.content_ref` at a `transform` returning bytes when text is required) → verify Save is blocked with inline error.
  4. Set `rerun_policy: ask` on a workflow, run twice, verify modal appears the second time. Pick "continue" → verify the original session updates, no new sidebar row.
  5. Share via clipboard → import on a fresh DataDir worktree → verify identical execution.
