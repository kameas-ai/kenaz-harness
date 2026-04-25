# Feature Specification: Workflow Engine — Declarative DAG-of-Steps for Agentic Flows

**Feature Branch**: `feat/workflow-engine-01KQ309F`
**Created**: 2026-04-25
**Status**: Draft
**Input**: User direction (earlier this session): "We want to be able to configure agent workflows … workflows are first-class harness concepts (declared in bundles as DAGs of steps) that *use* MCP tools and ACP-reachable agents as nodes." Configuration-first; declarative-primary, code-escape-hatch for the long tail.

## Why this mission exists

The harness's value proposition is not "another LLM SDK wrapper" — it's "configuration-first agentic flows where bundles compose LLM calls + MCP tools + A2A peers + scheduled triggers into reproducible, replayable workflows." Without a workflow engine, agents are one-shot prompts; the harness becomes a glorified chat. Without configuration-first declarative workflows, every flow is bespoke code; the bundle ecosystem can't compose.

This mission defines the DAG-of-steps engine: how a bundle declares a workflow, how the engine executes it, how nodes route to LLM calls / MCP tools / A2A peers / scheduled triggers / shell commands, how state flows between nodes, how errors propagate, and how every execution is replayable from the event log.

## Dependencies and relationships

- **Depends on**: `bundle-format-resolver` (workflows are a registered artifact kind), `event-log` (per-step audit), `policy-engine` (per-node policy gates), `secrets-keychain` (auth refs at the node level), `storage-foundations` (workflow run state persistence), `llm-connector` (LLM call nodes), `mcp-client` (tool-call nodes), `acp-orchestration` (A2A peer-call nodes), `scheduler` (scheduled triggers).
- **Enables**: `workflow-authoring-ui` (declarative editor), `chat session UI` (sessions can attach a workflow), `memory-rag` (workflows can read/write memory), enterprise scenarios (org-published workflows that operators run on demand).
- **Coordinates with**: `python-daemon` + `langchain-workflows` follow-up — LangChain-hosted workflows expose themselves as A2A peers and are invoked from this engine through A2A peer-call nodes; we never embed Python directly.
- **Does not cover**: workflow authoring UI (separate UI mission); LangChain runtime; arbitrary code execution outside the declared node kinds (sandbox follow-up).

## User Scenarios & Testing *(mandatory)*

### User Story 1 — A bundle author declares a multi-step workflow and any operator can run it (Priority: P1)

A bundle author writes a workflow as a YAML DAG: nodes (each with kind + config), edges (with optional conditions), inputs (external arguments), outputs. The bundle resolver activates the workflow as part of the bundle. Any operator with the bundle installed can run the workflow by name, supply the inputs, and receive the outputs (or stream the in-progress state). The engine routes each node through the appropriate consumer (LLM connector / MCP client / A2A client / shell). Every run is auditable end-to-end.

**Why this priority**: This is the central value proposition. Without it, the harness is config-driven LLM chat; with it, it's a configurable agent fabric.

**Independent Test**: A test bundle declares a 3-node workflow: (1) LLM call to summarize input → (2) MCP tool call to format → (3) emit output. An operator runs it; outputs match expected for a known input set; the event log shows all 3 nodes' starts, completions, and the dataflow between them.

**Acceptance Scenarios**:

1. **Given** a bundle declaring a workflow `summarize-and-format`, **When** an operator runs it with valid inputs, **Then** all nodes execute in dependency order and the outputs are returned.
2. **Given** the same workflow with inputs missing the required `text` field, **When** an operator runs it, **Then** the engine refuses before any node fires and surfaces a typed validation error naming the missing input.
3. **Given** the same operator runs the workflow twice with identical inputs, **When** comparing event-log entries, **Then** both runs produced the same node sequence (modulo provider non-determinism on LLM nodes, which is recorded per the LLM connector audit pattern).

---

### User Story 2 — Nodes are typed, swappable, and policy-gated per kind (Priority: P1)

The engine ships with a fixed set of v1 node kinds: `llm` (LLM connector call), `mcp_tool` (MCP client tool call), `mcp_resource` (MCP client resource read), `a2a` (A2A peer call), `transform` (in-process pure-data shaping with templated expressions), `branch` (conditional routing), `parallel` (fan-out + join), `wait` (scheduled delay), `manual_input` (operator-supplied prompt). Each node kind has a stable schema and a policy gate evaluated before each invocation. New node kinds are added by registering a kind handler — no core engine changes required.

**Why this priority**: Without kind plurality, workflows can only do one thing. Without policy gates per kind, an org can't trust workflow authors not to wire in a denied LLM provider or a banned MCP server.

**Independent Test**: A workflow with all 9 day-one node kinds executes end-to-end. An org policy denying one MCP server prevents activation of any workflow containing an `mcp_tool` node pointing at that server, with a typed denial.

**Acceptance Scenarios**:

1. **Given** a workflow exercising every day-one node kind, **When** activated, **Then** every node kind dispatches to its handler.
2. **Given** an org policy `mcp_server_allowlist` denies server `X`, **When** a workflow contains a node `mcp_tool: server: X`, **Then** activation fails with a typed policy denial referencing the policy artifact and the offending node.
3. **Given** a contributor adds a new node kind `vector_search`, **When** they register a handler, **Then** workflows declaring that kind execute without any change to the engine package.

---

### User Story 3 — State flows between nodes through a typed dataflow surface (Priority: P1)

Each node declares its inputs (references to upstream node outputs, workflow inputs, or constants) and produces typed outputs. The engine resolves the data dependency graph, validates types at workflow-load time (fail fast), and at runtime hands each node its resolved inputs. A failure to resolve an input (upstream node failed, type mismatch, etc.) is a typed engine error.

**Why this priority**: Without typed dataflow, workflows are just sequence; agents need to compose, route, transform.

**Independent Test**: A 4-node workflow where node B consumes node A's output, node C consumes node B's output, and node D consumes both A and C in parallel: all run; data routes correctly; a load-time type mismatch fails the activation.

**Acceptance Scenarios**:

1. **Given** a workflow where node B's input type is `string` but node A's output is `int`, **When** loaded, **Then** validation fails with a typed dataflow error before any node runs.
2. **Given** a workflow where node A fails at runtime, **When** node B (downstream) is reached, **Then** the engine marks B as `skipped_unreachable` (or `failed_dependency` per the configured policy) and records the cause.
3. **Given** a workflow with branch + parallel nodes, **When** executed, **Then** the engine sequentializes correctly per the dependency graph (no premature execution; no missed parallelism).

---

### User Story 4 — Every run is auditable and replayable from the event log (Priority: P1)

Every workflow activation, every node start, every node completion, every input resolution, every output emission, every retry, every cancellation is recorded in the harness append-only event log under the `workflow/` emitter namespace. Given a run id, an operator (or the audit log viewer) can reconstruct the full execution trace and, for runs with provider-deterministic LLM responses, replay the whole run.

**Why this priority**: Replay is one of the harness's three first-class promises (replay / branch / audit). Workflows are the most complex execution unit and the most valuable to replay.

**Independent Test**: A workflow runs to completion. An operator queries the event log for the run id and reconstructs every node's inputs, outputs, latency, and outcome. Replay produces the same node sequence.

**Acceptance Scenarios**:

1. **Given** a completed run, **When** the event log is queried by run id, **Then** entries exist for `workflow/started`, `workflow/node_started` × N, `workflow/node_completed` × N, `workflow/completed` (or `workflow/failed`), and dataflow events.
2. **Given** any node fails mid-run, **When** the run terminates, **Then** the event log contains `workflow/node_failed` with the typed cause, and downstream-skipped nodes have their `workflow/node_skipped` reasons recorded.
3. **Given** a run was cancelled, **When** the log is queried, **Then** the cancel event is recorded with the in-flight nodes' partial state, and any nodes still in-flight at cancel time are recorded as `workflow/node_cancelled`.

---

### User Story 5 — Errors, retries, and timeouts are first-class per node (Priority: P2)

Each node declares its retry policy (none, fixed, exponential-with-jitter), its timeout (per-attempt and total), and its on-error behavior (`fail_workflow`, `skip_downstream`, `route_to_branch`). The engine respects these without authors writing imperative code. Sensitive-node failures (e.g., a tool call returning a content-policy refusal) are first-class errors that don't trigger retry.

**Why this priority**: Half of any production workflow is error handling. Without first-class retries/timeouts/branches, every workflow re-implements them in transform nodes, badly.

**Independent Test**: A workflow with a flaky tool node (fails 1×, succeeds 2×) configured for retry: the engine retries and the run succeeds. Without retry: the run fails after 1 attempt with a clear error.

**Acceptance Scenarios**:

1. **Given** a node configured `retry: { strategy: exponential, attempts: 3, base_ms: 200 }`, **When** a transient failure happens, **Then** the engine retries up to 3 times with backoff and emits one event per attempt.
2. **Given** a node hitting its per-attempt timeout, **When** the timeout fires, **Then** the engine cancels the in-flight invocation, emits `workflow/node_timed_out`, and applies the on-error policy.
3. **Given** an LLM connector returns a content-policy refusal, **When** the node receives it, **Then** the engine treats it as non-retryable and surfaces it via the on-error route.

---

### User Story 6 — Workflows can be triggered manually, via API, or by the scheduler (Priority: P2)

Three trigger surfaces: manual operator action (UI / CLI), programmatic via `core/rpc.Workflow.Run`, and `scheduler` cron / one-time triggers. The engine doesn't know which surface invoked it; the trigger metadata is recorded on the run for audit.

**Why this priority**: Scheduled and triggered workflows enable the "runs even when laptop is closed" charter promise. Manual + API + scheduled covers the surface space.

**Independent Test**: The same workflow runs successfully invoked from each of the three triggers; trigger metadata is recorded distinctly.

**Acceptance Scenarios**:

1. **Given** a workflow registered with the scheduler at `0 9 * * *`, **When** the scheduled time arrives, **Then** the engine launches it and the event log records `trigger: scheduled`.
2. **Given** the operator clicks "Run" in the UI, **When** the engine executes, **Then** the event log records `trigger: manual` plus the operator id.
3. **Given** another harness component calls `core/rpc.Workflow.Run`, **When** the engine executes, **Then** the event log records `trigger: api` plus the calling subsystem id.

---

### User Story 7 — A workflow can pause for operator input mid-flight (Priority: P3)

A `manual_input` node pauses the workflow and waits for an operator to supply input via the UI / API. While paused, the run state is persisted; the operator can return hours later (laptop closed in between, charter's missed-run posture) and the workflow resumes from the paused state. Pause has a configurable timeout (default 24 hours) after which the workflow fails.

**Why this priority**: Human-in-the-loop is the realistic shape of most enterprise agent flows. P3 because v1 can ship without it (workflows that need human input fail at the manual_input node and the operator restarts) — but it's a fast follow-up.

**Independent Test**: A workflow with a `manual_input` node: launches → pauses → operator returns later → supplies input → resumes → completes.

**Acceptance Scenarios**:

1. **Given** a workflow reaches a `manual_input` node, **When** the engine pauses, **Then** the run state is persisted; the operator-facing UI shows the pending prompt; subsequent input resumes execution.
2. **Given** the pause timeout expires, **When** no input arrives, **Then** the workflow fails with `workflow/timed_out_at_manual_input`.

---

### Edge Cases

- A workflow declares a cycle in its DAG: load-time validation rejects with cycle path.
- A node references an upstream output that doesn't exist (typo in the dataflow ref): rejected at load.
- A `parallel` node with one branch failing: the configured `fan_in` policy decides whether to fail-fast, await-others, or treat-as-best-effort. Default: fail-fast.
- An `llm` node's chosen provider isn't activated for the operator: the policy engine produces a typed denial; the workflow fails before the call.
- A workflow's resolved input includes binary data exceeding a configurable size budget: truncated with a warning event; not silently dropped.
- A workflow run is mid-flight when the harness is shut down (laptop closes): the run state is persisted; on resume, the engine consults the recovery policy (resume / restart / abandon) per the workflow declaration.
- A node's tool-call returns a result that doesn't match its declared output schema: the engine treats it as a node failure with `output_schema_violation`.

## Requirements *(mandatory)*

### Functional Requirements

| ID | Title | User Story | Priority | Status |
|----|-------|------------|----------|--------|
| FR-001 | Workflow bundle artifact kind | As an author, I want to declare workflows as a registered bundle artifact kind via `bundle-format-resolver`. | High | Open |
| FR-002 | DAG validation at load | As an author, I want the engine to validate the DAG (no cycles, no dangling refs, type-coherent dataflow) at workflow load before any run. | High | Open |
| FR-003 | Day-one node kinds | As an author, I want 9 node kinds shipped at v1: llm, mcp_tool, mcp_resource, a2a, transform, branch, parallel, wait, manual_input. | High | Open |
| FR-004 | Node-kind extensibility | As a contributor, I want a stable node-kind contract so new kinds (e.g., vector_search) are addable without engine changes. | High | Open |
| FR-005 | Typed dataflow resolution | As an author, I want node inputs to reference upstream outputs / workflow inputs / constants with type-checked binding. | High | Open |
| FR-006 | Policy gate per node | As an operator, I want every node invocation gated by `policy-engine.Evaluate` against the appropriate control kind (LLM provider, MCP server, A2A peer, network tier) before the node fires. | High | Open |
| FR-007 | Audit per node | As an operator, I want every node lifecycle event recorded under the `workflow/` event-log namespace with redacted I/O. | High | Open |
| FR-008 | Retry policy per node | As an author, I want per-node retry configuration (none, fixed, exponential-with-jitter); transient errors retry, non-transient (auth, content-policy, schema-violation) do not. | High | Open |
| FR-009 | Timeout per node | As an author, I want per-attempt and total-time timeouts per node; timeouts cancel in-flight invocations cleanly. | High | Open |
| FR-010 | On-error route | As an author, I want each node to declare on-error behaviour: `fail_workflow`, `skip_downstream`, `route_to: <branch_id>`. | High | Open |
| FR-011 | Three trigger surfaces | As an operator, I want workflows triggered by manual UI action, by API call, or by the scheduler — same engine, distinct trigger metadata. | High | Open |
| FR-012 | Run state persistence | As an operator, I want every run's state persisted so a workflow can resume across harness restarts (charter's missed-run posture). | High | Open |
| FR-013 | Cancellation semantics | As an operator, I want to cancel an in-flight run; the engine propagates cancellation to in-flight nodes via context, awaits node-level cleanup up to a budget, then records terminal state. | High | Open |
| FR-014 | Manual-input pause | As an author, I want a `manual_input` node that pauses execution until an operator supplies the requested data, with a configurable pause timeout. | Medium | Open |
| FR-015 | Replay primitive | As an operator, I want a workflow run to be replayable from event log entries (modulo provider non-determinism on LLM nodes). | Medium | Open |
| FR-016 | Parallel + fan-in | As an author, I want `parallel` nodes with configurable fan-in policy (fail-fast / await-all / best-effort). | High | Open |
| FR-017 | Branch routing | As an author, I want `branch` nodes that route execution by templated condition over upstream outputs. | High | Open |
| FR-018 | Workflow inputs validation | As an author, I want declared workflow input schemas validated before any node runs; missing/typo inputs fail fast with operator-readable cause. | High | Open |
| FR-019 | Workflow outputs | As an author, I want workflows to declare outputs (named projections of node outputs) returned to the trigger surface on completion. | High | Open |
| FR-020 | Recovery policy | As an author, I want each workflow to declare a recovery policy (`resume_from_checkpoint`, `restart_from_beginning`, `abandon`) for runs interrupted by harness shutdown. | Medium | Open |
| FR-021 | Versioning | As an operator, I want workflow runs pinned to the workflow version that started them; bundle updates do not retroactively change in-flight runs. | High | Open |

### Non-Functional Requirements

| ID | Title | Requirement | Category | Priority | Status |
|----|-------|-------------|----------|----------|--------|
| NFR-001 | Engine overhead per node | Engine-introduced overhead (excluding node handler time) under 5 ms p95. | Performance | High | Open |
| NFR-002 | Run-state persistence latency | Every node-completion checkpoint persists in under 5 ms p99. | Performance | High | Open |
| NFR-003 | Concurrent runs | Engine sustains ≥ 100 concurrent active runs without contention regression beyond budget. | Reliability | Medium | Open |
| NFR-004 | Audit completeness | 100 % of node lifecycle events produce append-only event-log entries. | Auditability | High | Open |
| NFR-005 | Replay determinism | A run with deterministic node handlers replays byte-identically from the event log. | Reliability | High | Open |
| NFR-006 | Recovery success | Runs interrupted mid-flight resume successfully (per their recovery policy) at least 99 % of the time across the test matrix. | Reliability | Medium | Open |
| NFR-007 | Validation completeness | 100 % of malformed workflows (cycles, type mismatches, dangling refs) are rejected at load. | Reliability | High | Open |
| NFR-008 | Cancellation responsiveness | Time from operator cancel to terminal state recorded: under 2 seconds p99. | Performance | High | Open |

### Constraints

| ID | Title | Constraint | Category | Priority | Status |
|----|-------|------------|----------|----------|--------|
| C-001 | Architectural integrity | Engine logic in `core/workflow/`; node kinds in `core/workflow/nodes/<kind>/`; no other `core/` package imports the engine internals. | Technical | High | Open |
| C-002 | Bundle-format compatibility | Workflow declarations live in the existing bundle artifact format. | Technical | High | Open |
| C-003 | Append-only event log + redaction | All workflow events are append-only with redaction. | Security | High | Open |
| C-004 | Policy gate before every consumer call | Every `llm` / `mcp_tool` / `mcp_resource` / `a2a` node MUST consult `policy-engine.Evaluate` before invocation. | Security | High | Open |
| C-005 | No arbitrary code execution | The v1 node-kind set is fixed; nodes do not execute arbitrary user code. Any `script` / `exec` / `eval` kind is out of scope (a sandbox follow-up). | Security | High | Open |
| C-006 | Run-state in storage-foundations | Workflow run state persists via storage migrations; no parallel persistence layer. | Technical | High | Open |
| C-007 | SOC 2 readiness | Audit, replay, recovery, and policy-gated execution produce evidence sufficient for SOC 2 audit per the charter. | Regulatory | High | Open |

### Key Entities

- **Workflow** — bundle artifact declaring nodes, edges, inputs, outputs, recovery policy, version. Identified by `(bundle, name, version)`.
- **Node** — typed unit of work with a kind, a config, declared inputs, declared outputs, retry/timeout/on-error policies.
- **Edge** — directed dataflow link from a producer node's output to a consumer node's input. Optional condition (templated expression on upstream values) that gates the edge.
- **Run** — runtime instance of a workflow execution. State: `pending`, `running`, `paused`, `cancelling`, `cancelled`, `completed`, `failed`. Persisted in storage-foundations; resumable across harness restarts.
- **NodeInvocation** — typed record per node attempt: inputs, outputs, latency, outcome, retry count.
- **WorkflowEvent** — append-only event-log entry kinds: `workflow/registered`, `workflow/started`, `workflow/node_started`, `workflow/node_completed`, `workflow/node_failed`, `workflow/node_timed_out`, `workflow/node_cancelled`, `workflow/node_skipped`, `workflow/node_retried`, `workflow/paused_for_input`, `workflow/resumed`, `workflow/completed`, `workflow/failed`, `workflow/cancelled`, `workflow/policy_denied`.
- **NodeKindHandler** — registered handler for one kind. Contracts: `validate(config, schema)`, `execute(ctx, inputs) → outputs`, `cancel(ctx)`, `policy_inputs() → policy.Action`.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A bundle author can declare a 3-node workflow and any operator running it gets the expected outputs end-to-end within 15 minutes from a clean clone.
- **SC-002**: All 9 day-one node kinds pass the full execution matrix in CI.
- **SC-003**: 100 % of malformed workflows (cycles, type mismatches, dangling refs) are rejected at load with operator-readable causes.
- **SC-004**: 100 % of node lifecycle events produce append-only event-log entries with the `workflow/` namespace and redacted I/O.
- **SC-005**: A run interrupted by harness shutdown resumes successfully per its recovery policy ≥ 99 % of test-matrix runs.
- **SC-006**: A new node kind is added end-to-end without changes to `core/workflow/` outside the new kind's own subpackage.
- **SC-007**: Engine-introduced per-node overhead stays under 5 ms p95 across the test matrix.
- **SC-008**: A run cancelled by the operator reaches terminal state in under 2 seconds p99.

## Assumptions

- The v1 node-kind set covers ~80 % of realistic agent flows; advanced cases use a sequence of `transform` + `manual_input` nodes or escape to a LangChain-hosted A2A peer (via the `a2a` node kind).
- Templated expressions in edge conditions and `transform` nodes use a small embedded expression language (e.g., CEL or a similar non-Turing-complete subset) — exact language is a planning decision.
- LLM-node non-determinism is acknowledged: replay reproduces the *node sequence* and *upstream inputs*; the LLM's exact text output is recorded but not regenerated on replay (matching the LLM connector audit pattern).
- Workflow versioning uses the same `(name, version)` semantic as bundles; the lockfile pins.
- The engine ships v1 as in-process Go; large-scale distributed execution (cross-host) is a v2 concern via A2A peer-call nodes.

## Open Questions

1. **[NEEDS CLARIFICATION]** Templated expression language — CEL (Google), Tengo, Expr, or a hand-rolled minimal subset? Default if unresolved: CEL — proven, Go-native, Kubernetes-blessed, non-Turing-complete (matches policy-engine's planned use).
2. **[NEEDS CLARIFICATION]** Run-state persistence shape — one row per node invocation (granular, write-heavy) vs one row per run with periodic checkpoints (coarser, lighter)? Default if unresolved: one row per node invocation; optimize via batched commits if NFR-002 misses budget. Granularity wins for replay/audit.
3. **[NEEDS CLARIFICATION]** `manual_input` pause persistence horizon — defaults at 24 hours; what's the longest reasonable pause we want to support before declaring the workflow abandoned? Default if unresolved: 7 days hard cap on pause; longer requires an explicit `long_running: true` flag on the workflow declaration.
