# Spec — Workflows (`workflows-01KQ8TDG`)

**Status**: draft · **Owner**: alecfeeman

## 1. Why

Kenaz-harness today gives the user a chat surface, a tool catalog, an artifact pipeline, and a Cedar-gated execution model. What it does NOT give the user is a way to **package a repeatable multi-step procedure** they perform regularly into something reusable.

Concrete examples of what users do today as ad-hoc per-session work:
- "Check the latest commit on `main`, summarize the changes, write a release note draft as an artifact, save the diff for reference."
- "Pull the latest design doc from Notion, generate a critique, save the critique as an artifact, paste a 3-bullet summary into Slack."
- "Fetch the open issues from this repo, group by milestone, write a triage recommendation."

Each of these is a sequence of (1) tool calls + (2) model reasoning + (3) artifact output. Today the user retypes the prompt skeleton every time, hopes the model picks the right tools in the right order, and re-explains constraints across sessions.

A **Workflow** is a saved, parameterized recipe for this pattern: ordered steps (each = `{kind: tool_call | model_turn | artifact_save}`), inputs (typed parameters the user fills at run-time), and an explicit Cedar-gated execution surface.

The harness has the substrate for this already — the agent kernel graph (29+ callable kinds, 6 archetypes via YAML manifests) is essentially a workflow engine. What's missing is the **user-facing authoring + invocation UX** and a **storage shape** that's separate from the kernel-internal manifests.

## 2. Goals

- Users can **author workflows** in a UI (or by editing YAML in `<DataDir>/workflows/<name>.yaml`).
- Each workflow has typed inputs (string, file, artifact-ref, project-ref, enum, multiline-text).
- Workflows compose existing primitives: model turns (with system + user prompts referencing inputs), built-in tool calls (`bash`, `websearch`, `save_artifact`), MCP tool calls (filesystem, etc.), Cedar policy checks.
- Workflows are **invokable from the chat surface** (slash command) AND from a dedicated "Workflows" panel.
- Workflow runs are **first-class sessions** — the run produces a session in the existing sessions table, with `session.kind = "workflow_run"` and `session.metadata.workflow_id`. Lets us reuse all chat-surface affordances (history, artifacts, branching, audit).
- Workflows can **emit artifacts** at named steps; those artifacts attach to the run session and are promotable to project scope per the existing pipeline.
- Workflows are **shareable as YAML files** — copy/paste between machines; no harness account required.

## 3. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | New `<DataDir>/workflows/*.yaml` directory; one file per workflow. Schema includes `id`, `name`, `description`, `inputs[]`, `steps[]`, `version`. | proposed |
| FR-002 | Workflow `inputs[]` schema: `{name, kind: string|file|artifact_ref|project_ref|enum|multiline, required, default?, validation?}`. | proposed |
| FR-003 | Workflow `steps[]` schema: each step has `kind` (one of the agentic step types in FR-003a), plus `name` (for referencing outputs in later steps), `inputs_ref[]` (which workflow inputs / prior step outputs feed this step), and step-kind-specific fields. Outputs are typed (`text | json | bytes | artifact_id | error`) and downstream steps' `inputs_ref[]` validates type compat at YAML load time. | proposed |
| FR-003a | **Step-kind catalog (agentic workflow primitives)** — workflows are not just chained prompts; they are agentic mini-pipelines. The catalog: <br>• `model_turn` — single LLM turn (FR-004). <br>• `tool_call` — invoke a registered tool/MCP server method by name (FR-005). <br>• `http_request` — raw HTTP fetch: `{method, url, headers?, body?, query?, auth?, timeout_ms?}`. Output: `{status, headers, body_text, body_json?}`. Cedar gate `Action::"http_request"` against `HTTPHost::"<host>"` resource for outbound-network policy. <br>• `mcp_call` — explicit MCP server method invocation: `{server_id, method, params}`. Distinct from `tool_call` (which routes through the unified tool catalog) — `mcp_call` is for stepping outside the catalog when a workflow author knows exactly which MCP method they want. <br>• `web_scrape` — fetches a URL through a headless browser context: `{url, selector?, wait_for?, render_js: bool, timeout_ms}`. Output: `{html, text, structured?}`. Backend: ships behind a `HARNESS_WEB_SCRAPE` flag (default off in v1) and uses an embedded Chromium-CDP via the `chromedp` Go library OR stays out-of-scope and is deferred — the planner picks. <br>• `shell` — sandboxed command via the existing `kenaz__bash` built-in: `{cmd, args[], cwd?, timeout_ms, env_allowlist[]}`. Output: `{stdout, stderr, exit_code}`. Cedar gate identical to bash's. <br>• `read_artifact` — fetch artifact bytes by id: `{artifact_id_ref}`. Output: `{bytes, mime_type, title}`. <br>• `write_artifact` — formerly `artifact_save`; renamed for symmetry with read_artifact (FR-006). <br>• `transform` — pure-Go data transform between steps: `{kind: "jq" \| "jsonpath" \| "regex" \| "template", expr}`. Output: typed per the input. No I/O. Critical for plumbing one step's output into another's input shape. <br>• `conditional` — `{predicate: {step_ref, op: "matches" \| "equals" \| "exists" \| "gt" \| "lt", value}, then[], else[]}`. Predicate evaluates against a prior step's output. <br>• `invoke_workflow` — chain another workflow as a substep (FR-019). Out of v1 scope per FR-019; flagged here for catalog completeness. <br>• `parallel` — fan-out N steps concurrently, re-converge with combined outputs. **OUT OF v1 SCOPE** (Q2 in §9); listed for catalog awareness only. <br>• `agentic_loop` — let a `model_turn`-class step autonomously decide which other steps in the workflow to invoke next, up to a step-count cap. **OUT OF v1 SCOPE**; this is the most agentic primitive and warrants its own follow-up mission. | proposed |
| FR-004 | `model_turn` step: `{provider_profile_id?, model?, system_prompt?, user_prompt, max_turns?, allow_tools[]}`. Defaults: provider/model = workflow's declared default, falls back to user's active. `allow_tools[]` restricts which tools the model can call in this step. Output: `{text, tool_calls?, usage}`. | proposed |
| FR-005 | `tool_call` step: `{tool_name, args}`. Args may reference inputs/prior-outputs via `${input.name}` / `${step.<step_name>.output}`. Cedar policy still gates the call. Output: `{result, is_error, duration_ms}`. | proposed |
| FR-006 | `write_artifact` step (formerly `artifact_save`): `{title, content_ref, mime_type?}`. `content_ref` references a prior step's output (typically a model_turn or http_request body). Pipes into existing `coreart.Manager.Capture` with `Source: SourceWorkflowOutput` (new). Output: `{artifact_id}`. | proposed |
| FR-006a | **Step composition rules**: every step has a `name`; outputs accessible as `${step.<name>.output}` or specific subfields like `${step.<name>.body_json.fieldX}`. Type validation at YAML load: a step that references `${step.X.output}` as a string must come after step X, and X's output type must be string-compatible (text | text-extracted-from-json | etc.). The validator surfaces type mismatches at workflow-save time, not run time. | proposed |
| FR-006b | **`inline_run: bool` field on workflow** — drives Q28.2 hybrid model. `true` (default for single-step `model_turn` workflows) = workflow output appears as the next assistant turn in the calling chat session. `false` (default for multi-step workflows) = workflow spawns a new `workflow_run` session; the calling chat session gets a single inserted "Workflow X ran → see <link>" message with the final output summary. | proposed |
| FR-006c | **`rerun_policy` field on workflow** — applies only when `inline_run: false`. Values: `"fresh"` (default — every re-run spawns a new session), `"continue"` (re-run picks up the most-recent prior run session and continues there), `"ask"` (modal at re-run time: "Continue from <date> or start fresh?"). For `inline_run: true` workflows, re-run trivially runs again as a fresh assistant turn. | proposed |
| FR-007 | New `core/workflows/` package with `Workflow`, `Step`, `Input`, `Engine`. `Engine.Run(ctx, workflow, inputValues, opts) (*Run, error)` drives the steps sequentially. The Engine constructs a kernel-graph run under the hood — workflows compile to graphs, they don't replace them. | proposed |
| FR-008 | Workflow execution behavior depends on `inline_run` (FR-006b): <br>• `inline_run: true` → workflow's first `model_turn` step becomes the next turn in the current session. Tool/http/etc. steps execute "above" that turn but don't create distinct messages — their outputs feed into the model_turn. The chat surface shows a small "Workflow `summarize` running..." indicator inline. <br>• `inline_run: false` → workflow execution produces a new session row (`session.kind = "workflow_run"`, `session.metadata.workflow_id`, `session.metadata.workflow_version`, `session.metadata.parent_session_id?`). The new session renders with the workflow name + step-by-step progress; each step appears as a synthetic turn (system-role) with the step name, kind, and output. | proposed |
| FR-008a | **Sidebar UX for workflow runs**: `inline_run: false` workflow runs appear in a dedicated "Workflow runs" section in the sidebar, grouped by workflow id (so all `release_notes` runs cluster together). When a session is `continue`-resumed (rerun_policy), the existing row updates rather than spawning a duplicate. `inline_run: true` invocations are invisible in the sidebar — they're just chat turns. | proposed |
| FR-009 | RPC: `Workflows.List(ctx) []WorkflowSummary`, `Workflows.Get(ctx, id) Workflow`, `Workflows.Save(ctx, w) error`, `Workflows.Delete(ctx, id) error`, `Workflows.Run(ctx, id, inputs) (sessionID, error)`. | proposed |
| FR-010 | Frontend `WorkflowsView.vue` panel: list of workflows + "New workflow" + per-workflow "Run" button (opens an input-form modal). | proposed |
| FR-011 | Workflow author UI: `WorkflowEditor.vue` — visual step list, drag to reorder, type-validated input declarations, monaco-style YAML preview. Power users can also edit YAML directly. | proposed |
| FR-012 | Slash command integration: typing `/workflow <name>` in the chat composer opens the input form for that workflow; submission runs it as the current session OR a new workflow_run session (configurable per workflow via `inline_run: bool`). | proposed |
| FR-013 | Workflow runs are auditable: every step emits a `workflow.step_started` / `workflow.step_completed` / `workflow.step_failed` audit event with step name, duration, and outcome. | proposed |
| FR-014 | Cedar policy gate: workflows declared as `requires_policy: <policy_name>` in YAML; the engine checks the policy before each step that has a Cedar-gated kind (tool_call, model_turn that allows tools). User can author Cedar policies that constrain which workflows can run with which tool grants. | proposed |
| FR-015 | Workflow exports / imports: a "Share" affordance copies the workflow YAML to clipboard; an "Import from clipboard" affordance pastes a YAML and validates before saving. No upload to remote services. | proposed |
| FR-016 | Workflow versioning: each save bumps `version` (sequential int). Old versions kept under `<DataDir>/workflows/_history/<id>/v<n>.yaml` for 90 days; user can roll back from the editor. | proposed |
| FR-017 | Built-in starter workflows shipped: `summarize_url`, `code_review_diff`, `triage_issues`, `release_notes_from_commits`. Embedded via `//go:embed core/workflows/builtin/*.yaml`. | proposed |
| FR-018 | Workflow input `kind: file` integrates with `multimodal-io-01KQ8TDF`'s file picker; `kind: artifact_ref` integrates with the artifacts panel; `kind: project_ref` integrates with the project picker. | proposed |
| FR-019 | Workflows can be **chained** — a step's `kind: invoke_workflow` runs another workflow as a substep, threading inputs and aggregating outputs. Limited recursion depth (default 5, configurable). | proposed |

## 4. Non-functional requirements

| ID | Requirement | Threshold |
|---|---|---|
| NFR-001 | Workflow execution overhead vs hand-typed equivalent | < 100ms total dispatch overhead |
| NFR-002 | YAML schema validation on save | All errors surfaced inline in the editor; no silent rejections |
| NFR-003 | Workflow file size cap | 256 KiB per workflow YAML (large enough for ~50 steps; rejects accidental code-paste) |
| NFR-004 | Run session metadata size | Workflow context fits in `sessions.metadata` JSON (16 KiB cap maintained) |

## 5. Constraints

| ID | Constraint |
|---|---|
| C-001 | Local-first: workflows stored in `<DataDir>/workflows/`; never synced to a server. |
| C-002 | DIRECTIVE_001: frontend talks to core only via `core/rpc`; workflow YAML lives backend-side, frontend gets a serialized view. |
| C-003 | Cedar policy gate is non-bypassable: a workflow cannot escalate beyond the user's existing policy grants. Workflows can ONLY restrict, not expand, the active session's tool grants. |
| C-004 | No conditional logic Turing-completeness: `conditional` steps support simple `if <step_output> matches <regex>` predicates only. No loops in v1 (deferred). No arbitrary expression evaluation. |
| C-005 | Built-in workflows ship as data, not code — operator can override by placing a same-name YAML in `<DataDir>/workflows/`. |

## 6. Success criteria

- A new user can install harness, click "Workflows" → "Run summarize_url" → paste a URL → get a saved artifact within 30 seconds.
- A user can author a 5-step workflow without touching YAML directly (visual editor).
- Power users can edit the YAML and the editor renders the change without re-saving (live YAML edit).
- A workflow run renders in the sessions list with a distinct icon + step-progress badge.
- Workflow YAMLs round-trip through clipboard share → import → identical execution.
- Cedar policy gate refuses a workflow whose `requires_policy` references a missing policy with a clear error.

## 7. Scope split (recommended)

This spec is large. Recommended split into three missions when planning:

**Mission A — Workflow engine + storage + RPC (FR-001 through FR-009, FR-013, FR-014)**
Backend-only. The engine that compiles workflow YAML into a kernel-graph run + the storage + RPC surface. No UI.

**Mission B — Workflow authoring + run UX (FR-010, FR-011, FR-012, FR-015, FR-016, FR-018)**
Frontend. WorkflowsView, WorkflowEditor, slash-command integration, share/import.

**Mission C — Built-in starter workflows + chaining (FR-017, FR-019)**
Data + recursion support. Depends on A + B.

## 8. Out of scope (v1)

- Loops / iteration in workflows (`for-each`, `while`).
- Cron-style scheduled workflow runs.
- Workflow runs that span sessions / projects (e.g., "every Monday, run X across all my projects").
- Branching / conditional based on LLM judgment ("if the model says yes, do X else Y") — deferred; users can write two workflows.
- Workflow templates from a remote registry (matches local-first posture).
- A11y review for the visual editor (covered by `accessibility-audit` mission).
- Workflow run cost projection BEFORE execution — interesting but separate from this scope; piggybacks on token-cost-telemetry.

## 9. Open questions

- **Q1**: Workflow name uniqueness — global per harness, or per-project? Lean global; project scoping is a tag, not a namespace.
- **Q2**: `parallel` step kind — should workflows be able to fan out concurrent steps that re-converge? Lean **no for v1** — adds significant engine complexity (cancellation, partial-failure, output-merge semantics) without proven user need. Sequential-only first.
- **Q3**: Should the visual editor expose Cedar policy authoring inline, or just reference existing policies? Lean reference-only — Cedar editor mission owns that surface.
- **Q4**: Built-in workflow set — do we ship `release_notes_from_commits` and `triage_issues` (which assume Git/issue-tracker MCPs) or stick to provider-agnostic ones (`summarize_url`, `extract_facts_from_text`)? Lean MCP-agnostic for v1; advanced workflows can ship as a "starter pack" YAML download in docs.
