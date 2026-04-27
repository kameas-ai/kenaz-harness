# Tasks: Agent kernel graph — manifest-driven node catalog

**Mission ID**: `agent-kernel-graph-node-catalog-01KQ7JDZ`
**Spec**: `kitty-specs/agent-kernel-graph-node-catalog-01KQ7JDZ/spec.md`
**Plan**: `kitty-specs/agent-kernel-graph-node-catalog-01KQ7JDZ/plan.md`

Conventions:
- **Complexity**: S (≤ half-day) | M (½ – 1 day) | L (1 – 2 days). Anything over L should be split.
- **Deps**: other WP/task IDs that must land first.
- **AC**: acceptance criteria (1–2 bullets).
- **Tests**: unit / integration / frontend, per-task.

---

## Bundle A — Manifests + codegen + validator + reconciliation

### WP01 — Manifest schema + loader + inheritance resolver

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP01-T1 | Define `Manifest`, `AttrSpec`, `PortSpec`, `ResolvedManifest` Go types in `core/agentgraph/nodes/manifest.go`. | S | — | Types compile; YAML round-trip on a hand-written archetype manifest fixture. | unit (round-trip) |
| WP01-T2 | Embed `core/agentgraph/nodes/manifests/*.yaml` via `//go:embed`; ship the **5 archetype manifests** (`compute`, `control`, `read`, `write`, `marker`). | S | WP01-T1 | Each archetype YAML loads + parses through `Manifest`. | unit (load fixture) |
| WP01-T3 | `core/agentgraph/nodes/manifest_validator.go`: meta-schema validation (FR-002 / FR-003). Catches missing `id`, bad enum values, type-bound mismatches. | M | WP01-T1 | Hand-crafted bad manifests fail with field-level errors. | unit |
| WP01-T4 | `core/agentgraph/nodes/resolver.go`: extends-chain resolver with deep-merge + provenance tracking (FR-004, FR-005). Single-inheritance only. | M | WP01-T1, WP01-T3 | Fixture: archetype `compute` + child `model` (kind manifest); resolved manifest's provenance map traces each field to its layer. | unit |
| WP01-T5 | Cycle and multi-extends rejection in resolver (A18, A19, FR-004 cycle). | S | WP01-T4 | Cyclic fixture rejected; multi-extends fixture rejected with clear message. | unit |
| WP01-T6 | `core/agentgraph/nodes/loader.go`: `LoadCatalog(opts)` mirrors `core/agentgraph/activities/loader.go` — embedded only in this WP; user dir is WP07. | M | WP01-T2, WP01-T4 | Catalog loaded; `Get(archetype_id)` returns resolved manifest. | unit |
| WP01-T7 | `core/agentgraph/nodes/catalog.go`: thread-safe in-memory map; `Get(NodeKind)`, `List()`, `Archetypes()`, `Kinds(category)`. | S | WP01-T6 | Concurrent reads safe; lookup returns deterministic order. | unit |
| WP01-T8 | `core/agentgraph/seams.go`: declare `ManifestCatalog` interface to break the import cycle between kernel and `nodes` package. | S | WP01-T7 | Interface compiles; `nodes.Catalog` satisfies it. | unit |
| WP01-T9 | `core/agentgraph/nodes/executors.go`: `Register(name, fn) / Lookup(name)` registry stub (populated in WP04). | S | — | Registry round-trips; concurrent `Register`s safe. | unit |
| WP01-T10 | Charter pre-flight: `gocyclo` / import-cycle scan confirms `nodes` does not import `agentgraph`'s kernel-internal packages. | S | WP01-T8 | Charter check passes. | static |

### WP02 — Codegen of `attrs_gen.go` + `wire_gen.go`

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP02-T1 | New tool at `core/agentgraph/nodes/cmd/gen/main.go`. Reads catalog (using `LoadCatalog`); resolves all manifests; emits two files via `text/template`. | L | WP01 | Tool runs from CLI; produces `attrs_gen.go` + `wire_gen.go` in `core/agentgraph/`. | unit + smoke |
| WP02-T2 | Template `attrs.tmpl`: per-kind `<KindPascal>Attrs` struct + manifest-driven `Validate()` body. | M | WP02-T1 | Generated struct matches hand-written equivalent on a fixture; `Validate()` enforces required/min/max/enum from manifest. | unit (gen + compile + invoke) |
| WP02-T3 | Template `wire.tmpl`: `NodeKind*` constants, `AllNodeKinds()`, `defaultAttrsFor`, `decodeAttrs` switch (delegating to `defaultAttrsFor` for empty-attrs path). | M | WP02-T1 | Generated decoder round-trips JSON for every kind; equivalent to hand-written switch in `wire.go` (which moves to `wire_gen.go`). | unit |
| WP02-T4 | Add `//go:generate go run ./core/agentgraph/nodes/cmd/gen` directive to `core/agentgraph/spec.go`. | S | WP02-T1 | `go generate ./core/agentgraph/...` runs successfully. | smoke |
| WP02-T5 | **Delete** the hand-written `core/agentgraph/attrs.go` (its content is now generated). Move per-kind switch out of `wire.go` into `wire_gen.go`. Keep `wireGraph`/`wireNode`/`graphToWire`/`wireToGraph`/`cloneMap` in `wire.go`. | M | WP02-T2, WP02-T3 | All existing parent-mission tests under `core/agentgraph/*_test.go` still pass after the deletion. | regression (existing tests) |
| WP02-T6 | Idempotency test: `core/agentgraph/nodes/cmd/gen/main_test.go` runs `gen`, captures output bytes; runs `gen` again; asserts byte-equal. | S | WP02-T1 | Test passes locally; LF-only line endings. | unit |
| WP02-T7 | CI gate: add `make generate` target (or extend `make lint`) that runs `go generate ./...` then `git diff --exit-code`. | S | WP02-T6 | A hand-edited `*_gen.go` file fails CI. | CI |
| WP02-T8 | Generated-file lint exclusion: update `.golangci.yml` to skip style checks on `*_gen.go` (still runs vet + race). | S | WP02-T1 | `golangci-lint run` does not flag generated files for cosmetic issues. | static |

### WP03 — Validator refactor

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP03-T1 | Remove per-kind hand-coded rules from `validator.go` (those that mirror `attrs.go`'s `Validate()` methods — covered by manifest-driven validation in `attrs_gen.go`). | M | WP02 | `validator.go` shrinks; existing parent-mission tests retargeted. | regression |
| WP03-T2 | Add manifest-driven validate path: validator iterates nodes, calls `attrs.Validate()` on each (manifest-driven body in `attrs_gen.go`); collects per-node errors with manifest-attribution prefix (FR-015). | M | WP03-T1 | A1, A4, A5 produce errors with the exact format `node "x": manifest <kind>.attrs.<attr>.<rule>: got <v>, want <bound>`. | unit |
| WP03-T3 | New rule: archetype-not-callable (FR-016). | S | WP03-T2 | A6 passes. | unit |
| WP03-T4 | New rule: every kind's `executor:` reference resolves via `nodes.Lookup` (FR-019 enforcement at validation time). | S | WP03-T2, WP01-T9 | A20 passes. | unit |
| WP03-T5 | Keep cycle-outside-loop, orphan-node, edge-endpoint, dial-ref, activity-ref rules unchanged (FR-014). | S | — | Existing tests pass. | regression |
| WP03-T6 | Update `validator_test.go` for new error-message format; mass-rename test cases. | M | WP03-T2 | All validator tests green. | unit |

### WP04 — Taxonomy reconciliation + alias map

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP04-T1 | Author the **23 kind manifests** (per spec.md §4.8 reconciliation table; new kinds `compact`/`approval`/`artifact` defer to WP05): `model.yaml`, `tool.yaml`, `transform.yaml`, `activity.yaml`, `reflect.yaml`, `review.yaml`, `planner.yaml`, `ask.yaml`, `escalate.yaml`, `decision.yaml`, `branch.yaml`, `parallel.yaml`, `join.yaml`, `merge.yaml`, `loop.yaml`, `retry.yaml`, `memory.yaml`, `corpus_read.yaml`, `corpus_write.yaml`, `attachment.yaml`, `history_read.yaml`, `trace_write.yaml`, `checkpoint.yaml`. | L | WP01..WP03 | Each manifest meta-validates; resolved manifest matches hand-written `*Attrs` from parent mission for non-renamed kinds. | unit (per-manifest fixture) |
| WP04-T2 | Rename executor functions in `exec_compute.go`/`exec_control.go`/`exec_state.go`: `ExecLLM → ExecModel`, `ExecBranch → ExecDecision`, `ExecFork → ExecBranch`, `ExecPlan → ExecPlanner`. Each `init()` calls `nodes.Register(...)`. | M | WP04-T1 | Kernel run still works on a sample graph using new names. | unit + integration |
| WP04-T3 | Alias map in `loader.go`: build `aliasIndex map[string]NodeKind` after manifest parse; the wire decoder (in `wire_gen.go`) consults it before failing on unknown kinds. | M | WP04-T1 | A7, A8 pass: `kind: llm` resolves to `model`; `kind: fork` resolves to `branch`; old `kind: branch` resolves to `decision`. | unit |
| WP04-T4 | Deprecation event emission: `kind_alias_resolved {old, new, removal_in}` to trace + structured log; `removal_in` value reads a constant `AliasSunsetVersion = "<next-minor>"`. | S | WP04-T3 | Trace contains the event for each alias-loaded fixture. | unit |
| WP04-T5 | Update parent-mission YAML fixtures to new names (`library/toolloop_default.yaml`, activity YAMLs, test fixtures). Retain old-name fixtures under `core/agentgraph/testdata/aliases/` to exercise the alias path. | M | WP04-T2 | Parent-mission integration tests green; alias-path tests confirm warnings emitted. | regression + integration |
| WP04-T6 | Re-run `go generate ./core/agentgraph/...`; commit regenerated `attrs_gen.go` + `wire_gen.go` reflecting the 23 kinds. | S | WP04-T1 | CI codegen-drift gate passes. | CI |
| WP04-T7 | Documentation in spec.md §4.8 reflected in `docs/agent-kernel-graph-node-catalog.md` (skeleton — full doc in WP08). | S | WP04-T1 | Doc table matches manifest set. | doc-review |
| WP04-T8 | Alias-collision tests: two kinds declaring the same alias rejected at load. | S | WP04-T3 | Test fails the load with clear error. | unit |
| WP04-T9 | Lint/grep CI rule: non-alias-test YAMLs may not contain `kind: llm`, `kind: fork`, `kind: plan`, or old-meaning `kind: branch` (the predicate router). | S | WP04-T5 | `make lint` (or a dedicated script) catches a stray `kind: llm` in a fixture. | CI |

### WP05 — New kinds: `compact`, `approval`, `artifact`

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP05-T1 | Author manifest `compact.yaml`: extends `compute`; attrs `strategy`, `target_token_budget`, `custom_subgraph_id`; output port `branch_result: any`. | S | WP04 | Manifest meta-validates. | unit |
| WP05-T2 | Implement `ExecCompact` in `exec_compute.go`: dispatches on `strategy` to `core/agentgraph/compaction/strategies.go`'s existing strategy implementations; emits `BranchResult` port. | M | WP05-T1 | A11 passes — `compact` reduces context under cap. | unit + integration |
| WP05-T3 | Coordination guard: kernel skips its automatic pre-call compaction when a `compact` kind precedes the next-firing LLMNode (idempotency). | S | WP05-T2 | Test: graph with explicit `compact` does NOT double-compact. | integration |
| WP05-T4 | Author manifest `approval.yaml`: extends `control`; attrs `approver_role`, `policy_label`, `auto_approve_window_seconds`, `prompt`. | S | WP04 | Manifest meta-validates. | unit |
| WP05-T5 | Implement `ExecApproval` in `exec_control.go`: reuses the parent mission's `pending_ask` primitive; emits `pending_approval` event; persists across restarts. | M | WP05-T4 | A12 passes — approval pauses + auto-approve window respected. | unit + integration |
| WP05-T6 | Refactor `pending_ask` underlying primitive to `pendingHumanGate` so it serves both `ask` and `approval` (rename internal helper; preserve external event names). | S | WP05-T5 | Existing `ask` integration test green. | regression |
| WP05-T7 | Author manifest `artifact.yaml`: extends `write`; attrs `mime_type`, `output_target`, `attachment_ref`, `content`. | S | WP04 | Manifest meta-validates. | unit |
| WP05-T8 | Implement `ExecArtifact` in `exec_state.go`: dispatches on `output_target`: `session_message` (writes to chat surface), `file_path` (writes under `<DataDir>/artifacts/<run_id>/`), `report` (emits a structured event). | M | WP05-T7 | A13 passes — all three output_targets produce expected outputs. | unit + integration |
| WP05-T9 | Cedar policy gate on artifact file_path: block traversal (`..`), absolute paths, paths outside `<DataDir>/artifacts/<run_id>/`. | S | WP05-T8 | Policy unit test rejects malicious paths. | unit |
| WP05-T10 | Re-run codegen; verify CI passes. | S | WP05-T1, WP05-T4, WP05-T7 | `attrs_gen.go` and `wire_gen.go` reflect 26 total kinds. | CI |

### WP07 — User override (`<DataDir>/agent_graph/nodes/*.yaml`)

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP07-T1 | Extend `LoadCatalog(opts)` with `UserDir string` (mirror `activities/loader.go: LoadOptions.UserDir`). | M | WP01 | User-dir manifests load and override shipped manifests. | unit |
| WP07-T2 | Forbidden-field rejection (FR-021): user override may not change `id`, `category`, `extends`, `executor`, or `attrs.<name>.type`. Each rejection has a clear error pointing at the file path. | M | WP07-T1 | A10 passes; `attrs.<name>.type` change also rejected. | unit |
| WP07-T3 | Allowed-field deep-merge (FR-022). Lists replace; maps merge key-by-key recursively; scalars replace. | S | WP07-T1 | A9 passes — `review.yaml` override changes `defaults.max_iterations` to 5. | unit |
| WP07-T4 | INFO-level logging (NFR-006): kind ID + JSON-pointer path of differing fields, on startup. | S | WP07-T3 | Log output verified in fixture run. | unit |
| WP07-T5 | Doctor RPC `Nodes_ReloadCatalog` (paired with WP06's RPC view, but the kernel-side handler is here). | S | WP07-T1 | Calling the RPC re-resolves the catalog; runtime catalog instance updated atomically. | unit |
| WP07-T6 | Provenance preserved through user override: resolved manifest's `Provenance["max_iterations"]` returns `"user-override"` for fields the user changed. | S | WP07-T3 | A17 passes. | unit |
| WP07-T7 | Tests: missing user dir → silent no-op; empty user dir → silent no-op; invalid user manifest → load-time error with file path. | S | WP07-T1 | Tests green. | unit |

(WP06 is in Bundle B below.)

---

## Bundle B — Frontend + polish

### WP06 — Frontend palette tree + manifest-driven attribute editor

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP06-T1 | RPC view: `core/rpc/views/agentgraph/api.go` adds `Nodes_Catalog`, `Nodes_GetManifest(id)`, `Nodes_ReloadCatalog`. | M | WP01..WP05, WP07 | RPC round-trip: catalog returns full manifest list with provenance. | unit |
| WP06-T2 | `core/rpc/bindings.go` wiring + Wails bindings regen. | S | WP06-T1 | Frontend `harnessClient.ts` types regen succeed. | build |
| WP06-T3 | `frontend/src/lib/types.ts`: add `ResolvedManifest`, `AttrSpec`, `PortSpec` TS types matching Go shape. | S | WP06-T2 | Types compile; round-trip JSON parse on a fixture catalog. | unit |
| WP06-T4 | Vue composable `useNodeManifest(id)` that wraps a catalog store with manifest fetch + cache. | M | WP06-T3 | Composable returns the resolved manifest synchronously after the initial async load. | vitest |
| WP06-T5 | `frontend/src/views/graphs/NodePaletteTree.vue`: tree view Category → Archetype → Kind; archetypes greyed out + tooltip "abstract — not directly callable in v1". | M | WP06-T4 | A14 passes — palette renders correctly; archetypes non-droppable. | vitest + e2e |
| WP06-T6 | `frontend/src/views/graphs/NodeAttributeEditor.vue`: manifest-driven form; switch widget by `AttrSpec.type` (string / int / float / bool / enum / model_ref / tool_ref / activity_ref / corpus_ref / attachment_ref / node_id_ref / port_ref / messages_ref). | L | WP06-T4 | A15 passes — each type renders the correct widget; required fields enforce. | vitest |
| WP06-T7 | Object/map/array fallback: JSON textarea with parse-on-blur + error display. | S | WP06-T6 | Invalid JSON shows error inline; valid JSON updates the model. | vitest |
| WP06-T8 | `frontend/src/views/graphs/NodeInheritanceTooltip.vue`: surfaces provenance on hover (e.g., "max_iterations: 5 (user-override; shipped: 3)"). | S | WP06-T4 | A17 passes. | vitest |
| WP06-T9 | Modify `frontend/src/views/graphs/GraphSpecEditor.vue` to use the new components. | M | WP06-T5, WP06-T6, WP06-T8 | Existing graph editor flows still work; new node creation uses the manifest-driven path. | e2e |
| WP06-T10 | Reload doctor button in Settings → "Reload node catalog"; calls `Nodes_ReloadCatalog`. | S | WP06-T1 | Edit a manifest on disk, click reload, see updated default in editor. | manual |

### WP08 — Polish: deprecation surfacing + docs + integration tests + optional hot-reload

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP08-T1 | Frontend: yellow banner when a graph load resolved any aliases (lists old → new pairs and the removal version). | S | WP04, WP06 | A7 / A8 trigger banner; doc-link visible. | vitest + e2e |
| WP08-T2 | `docs/agent-kernel-graph-node-catalog.md`: full authoring guide (manifest schema, attr types, override pattern, alias migration table, codegen runbook, hot-reload doctor). | M | WP01..WP07 | Doc is internally consistent with code; lint-spell-checked. | doc-review |
| WP08-T3 | Integration tests for A1..A20: each acceptance walkthrough runs end-to-end as a Go test under `core/agentgraph/integration_test.go` (or split per area). | L | WP01..WP07 | All 20 walkthroughs pass. | integration |
| WP08-T4 | Performance smoke: 30-manifest load + resolve <50 ms (NFR-011). | S | WP01 | Benchmark target hit on local M-series. | bench |
| WP08-T5 | Lint sweep: golangci-lint clean; vitest lint clean; doc lint clean. | S | All | CI green. | CI |
| WP08-T6 | **Optional**: `fsnotify`-based hot reload behind `--enable-manifest-hot-reload` dev flag (NFR-007). Loader watches `core/agentgraph/nodes/manifests/` AND `<DataDir>/agent_graph/nodes/`; re-resolves on change; emits `manifest_catalog_reloaded` event. | M | WP07-T5 | Edit manifest while `wails dev` running; new default surfaces in editor without restart. | manual + integration |
| WP08-T7 | Update parent mission's `kitty-specs/agent-kernel-graph-01KQ6391/` README/links to point to this mission for taxonomy questions. | S | WP08-T2 | Cross-link visible. | doc-review |
| WP08-T8 | CHANGELOG entry: alias deprecations + removal version + new kinds. | S | WP04, WP05 | Entry follows existing CHANGELOG format. | doc-review |

---

## Cross-WP dependencies summary

```
WP01 ─┬─→ WP02 ─→ WP03 ─→ WP04 ─→ WP05 ─→ WP06 ─→ WP08
      │                                ↑
      └─→ WP07 ─────────────────────────
```

- WP07 can run in parallel with WP02..WP05 once WP01 lands (independent of codegen / taxonomy).
- WP06 and WP08 are Bundle B — they wait until Bundle A is fully shipped because they consume the full kind set.
