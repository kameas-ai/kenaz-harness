# Spec: Agent kernel graph — manifest-driven node catalog with archetype inheritance

**Mission ID**: `agent-kernel-graph-node-catalog-01KQ7JDZ`
**Status**: draft
**Owner**: alecfeeman
**Planning base**: `main`
**Merge target**: `main`
**Parent mission**: `agent-kernel-graph-01KQ6391` (feature-complete on `main`)

---

## 1. Vision & motivation

The parent mission shipped 23 concrete node-kinds across 3 categories (compute / control / state) using a **four-file-per-kind pattern**:

1. An enum constant in `core/agentgraph/spec.go` (e.g., `NodeKindLLM`).
2. A typed `*Attrs` struct in `core/agentgraph/attrs.go` with hand-written `Validate()`.
3. A switch-arm in `core/agentgraph/wire.go` decoding the raw map into the typed struct.
4. An executor function (split across `exec_compute.go`, `exec_control.go`, `exec_state.go`) plus per-kind validator rules in `validator.go`.

This works, but it has four problems we discovered while shipping the parent mission:

- **Drift between Go and YAML**: tunable defaults (`MaxIterations` default 3, `pre_call_threshold` default 0.85, `tool_result_max_bytes` default 16 KiB) live both in Go literal constants and in author-facing docs. They diverge.
- **Boilerplate per kind**: adding a kind means touching four files plus tests in roughly the same shape every time.
- **No clear taxonomy**: the parent spec talked about Read / Write / Decision / Compute archetypes but the implementation only encodes a flat enum. The taxonomy lives only in the spec's narrative, never as load-bearing data.
- **Inconsistent naming**: the parent mission's research started with archetype-aligned names (Read/Write/Decision) but ended with kind names that conflate two patterns: `BranchNode` (predicate router) and `ForkNode` (sub-graph spawn) both express different forms of "branching"; `MemoryNode` straddles Read and Write via a `mode` discriminator while `CorpusReadNode` and `CorpusWriteNode` are split.

This mission introduces a **manifest-as-truth declarative system** with **archetype inheritance**:

- Each node-kind ships as a YAML manifest under `core/agentgraph/nodes/manifests/`.
- A manifest declares: `id`, `category`, optional `extends:` (archetype chain), `attrs:` schema, `ports:` declaration, `budget:` signature, `executor:` Go-symbol reference, deprecation aliases, and shipped defaults.
- Three taxonomy layers are first-class: **Category** (Compute / Control / State — organizational only) → **Archetype** (abstract contract; e.g. Read, Write, Decision) → **Kind** (concrete callable).
- An inheritance resolver merges the `extends:` chain at load time. Children override parent fields; never vice versa.
- The validator drives off resolved manifests — no hand-coded per-kind rules for required fields, ranges, port counts, or port types.
- `attrs.go` per-kind structs and `wire.go`'s decoder switch are **generated** from manifests by `go generate ./...` in CI; CI fails if generated files drift.
- Executors stay in Go (referenced by manifest); v1 does not introduce WASM or plugin runtimes.
- **User overrides**: drop a YAML at `<DataDir>/agent_graph/nodes/<id>.yaml` to deep-merge over a shipped manifest (same pattern as activities subsystem at `core/agentgraph/activities/loader.go`).
- The frontend node palette renders the Category → Archetype → Kind tree; the per-node attribute editor is rendered from the manifest schema.
- The taxonomy is reconciled: `LLMNode` becomes `model`, `BranchNode` becomes `decision`, `ForkNode` becomes `branch`, `MemoryNode` is split into archetype membership for read/write modes, three new kinds (`compact`, `approval`, `artifact`) are added.

The parent mission's design intent — "Compute / Control / State as primitives" — survives unchanged. What changes is **how we author and validate** them.

### 1.1 Why archetypes matter

Today a future kind like `compact` (a first-class compaction primitive emitting a `BranchResult`) would have to redeclare every attribute that overlaps with `LLMNode` (provider, model, system_prompt, max_tokens, json_schema). With archetypes, `compact` declares `extends: compute` and inherits the LLM contract; only the divergent fields are repeated.

Mid-level archetypes also let the validator answer "can a Write port flow into a Read port?" structurally instead of by hand-coded rule per kind pair.

### 1.2 Why generate Go from YAML and not the reverse

Three reasons:

1. **Author ergonomics**: adding a kind should be one YAML file, not a code change. Users may eventually author kinds without touching Go (deferred to v2 — see §10).
2. **Single source of defaults**: the YAML literal `max_iterations: 3` is the authoritative default. The Go struct's zero-value is _not_ the default; the manifest's default is.
3. **CI-enforced consistency**: a generator that produces Go from YAML is straightforward; a Go AST walker that scrapes defaults out of struct literals into YAML is fragile. Pick the easier direction.

### 1.3 Why executors stay in Go (v1)

Custom user kinds in v1 are limited to:

- **Composition path**: an `ActivityNode` referencing a sub-graph (already shipped). No Go required.
- **Override path**: drop a manifest at `<DataDir>/agent_graph/nodes/<id>.yaml` to retune defaults, ports, or budget for an existing kind.

Adding a brand-new kind with a brand-new Go executor still requires a code change. This is acceptable for v1 because the harness is single-user, local-only, and a power-user can rebuild from source. WASM/plugin executors are explicitly out of scope (see §9). The manifest's `executor:` field is a Go symbol reference resolved through a registry-pattern lookup table; a future v2 could add a `wasm: <path>` discriminator without breaking v1 manifests.

### 1.4 Why archetypes are NOT callable in v1

An archetype like `read` is an abstract contract: required ports, common attrs, budget signature. It has no executor and cannot be instantiated as a `kind: read` node. Concrete kinds (`history_read`, `corpus_read`, `memory` in read mode, `attachment`) extend it.

This is a deliberate v1 simplification. Promoting an archetype to callable would require an attr-dispatch executor (e.g., `read` with `source: history|corpus|memory|attachment` discriminator) that picks the concrete behavior at runtime. We can add that in v2 by simply adding an `executor:` field to the archetype manifest plus a dispatch table; no breaking spec change.

### 1.5 Local-only, GUI-only, single-user (inherits parent)

All constraints from the parent mission apply unchanged: state under `<DataDir>`, no CLI, single user per install, no cloud sync.

---

## 2. Glossary

- **Manifest** — a YAML document describing one archetype or one kind. Lives at `core/agentgraph/nodes/manifests/<id>.yaml` (shipped) or `<DataDir>/agent_graph/nodes/<id>.yaml` (user override). Each manifest is one file.
- **Category** — top-level taxonomy: `compute`, `control`, `state`. **Organizational only** — never callable, never a kind itself, never appears in `extends:`. Used by the frontend palette grouping and by the validator's "category-cross talk" warnings.
- **Archetype** — abstract contract intermediate between Category and Kind. Examples: `read`, `write`, `decision`, `compute` (the archetype, not the category). Declares `callable: false` in v1. Defines required ports, common attrs, budget signature. Children inherit via `extends:`.
- **Kind** — concrete callable node-kind. Declares `callable: true` (default), `extends: <archetype>`, and an `executor:` Go-symbol reference. Examples: `model` (extends `compute`), `decision` (extends `control`), `corpus_read` (extends `read`).
- **Inheritance chain** — the ordered list of manifests assembled by following `extends:` from a kind up to its root archetype. The resolver deep-merges each level; later levels (closer to the kind) override earlier levels.
- **Resolved manifest** — the merged result of an inheritance chain. Every kind has exactly one resolved manifest after the loader runs.
- **Executor reference** — a string identifier (`"agentgraph.ExecModel"`) the kernel resolves through a registry-pattern lookup table at startup. The registry is populated by `init()` in each executor's source file.
- **User override** — a manifest at `<DataDir>/agent_graph/nodes/<id>.yaml` that deep-merges over the shipped manifest of the same `id`. Deep merge means scalars replace, lists replace whole, maps merge key-by-key recursively. User cannot change `category` or `extends` (would break invariants); other fields are tunable.
- **Alias** — a deprecated kind name that maps to a current kind. Declared in the current kind's manifest under `aliases:`. The loader resolves aliases at YAML load time and emits a deprecation warning to the trace + structured log.
- **Codegen** — the `go generate ./...` step that produces `attrs.go` per-kind structs and the `wire.go` decoder switch from the resolved manifests. Idempotent: running it twice produces no diff.
- **Manifest schema** — the meta-schema describing what fields a manifest may have (the schema-of-schemas). Embedded as a Go struct at `core/agentgraph/nodes/manifest.go`; YAML on disk validates against it.
- **Node palette** — the frontend GraphEditor's node-type tree, rendered Category → Archetype → Kind, with each leaf carrying a manifest-driven attribute editor.

---

## 3. User stories

- **US1 — Power-user retunes a default**: User wants `review` to default to 5 iterations instead of 3. They drop `<DataDir>/agent_graph/nodes/review.yaml` containing `attrs: { max_iterations: 5 }`. On next session start, the resolved manifest reflects the override; existing graphs use the new default for unspecified `max_iterations`.
- **US2 — Renames are non-breaking**: User had a graph YAML referencing `kind: BranchNode` (predicate router). After upgrading, the loader resolves `BranchNode` → `decision` via the alias map, fires a deprecation warning, runs the graph correctly. User has one minor version to migrate before the alias is removed.
- **US3 — Codegen pre-commit gate**: Maintainer adds a new manifest `core/agentgraph/nodes/manifests/translate.yaml`. Running `go generate ./...` regenerates `attrs.go` + `wire.go` to include `TranslateAttrs` + the decoder switch arm. CI's "go generate clean" check passes only if the maintainer committed both the manifest and the generated diff.
- **US4 — Frontend palette renders correctly**: User opens the GraphEditor's node-type picker. They see "Compute" expandable to "Compute / Read / Write / Decision" archetypes (with archetypes greyed out / non-droppable), each expandable to concrete kinds. Selecting `model` opens an attribute editor with fields driven by the manifest (Provider dropdown, Model text, Max-Tokens int, etc.).
- **US5 — Adds `compact` as a first-class kind**: Maintainer defines `compact` extending `compute`, declaring it emits a `BranchResult` port. The compaction subsystem (Bundle D from the parent mission) now references `compact` as a callable primitive instead of a free-floating helper. No rebuild of compaction internals — only the manifest plus a generated executor stub.
- **US6 — Approval gate**: A new `approval` kind extending `control` requires a human button click before the run continues. Distinct from `ask` (which elicits free-form text). `approval` reuses the existing pending-event pattern from `ask` but its manifest declares a different attrs shape (`approver_role`, `policy_label`, `auto_approve_window_seconds`).
- **US7 — Artifact terminal output**: A `state` kind `artifact` represents a terminal output (message, file, change report). Manifests include `mime_type`, `output_target` (`session_message | file_path | report`), `attachment_ref`. Replaces ad-hoc end-of-graph dumping.
- **US8 — User adds a custom kind via composition**: User defines an `ActivityNode` referencing a sub-graph that does what they want. No Go change required. (Already supported by the parent mission; this mission does not regress it.)
- **US9 — Validator drives off manifest**: Author writes a graph with `kind: model` and a missing `model:` field. Validator reads the resolved manifest, sees `model` is `required: true`, emits a clear error pointing at the offending node ID and the manifest line that declares the requirement.
- **US10 — Hot reload during dev**: While running `wails dev`, the maintainer edits `core/agentgraph/nodes/manifests/model.yaml` to bump the default `max_tokens` from 4096 to 8192. The harness picks up the change without restart (nice-to-have, not required for v1).

---

## 4. Functional requirements

### 4.1 Manifest schema

- **FR-001** New directory `core/agentgraph/nodes/manifests/` holds one YAML file per archetype and per kind, named `<id>.yaml`. The directory is embedded into the binary via `//go:embed`. The loader is at `core/agentgraph/nodes/loader.go`.
- **FR-002** Each manifest YAML conforms to a fixed schema (the **manifest schema**) defined as a Go struct `Manifest` in `core/agentgraph/nodes/manifest.go`:
  - `id: string` (required, kebab-case, matches filename stem)
  - `kind_name: string` (the on-the-wire NodeKind value; defaults to `id`)
  - `display_name: string` (frontend palette label)
  - `description: string` (frontend tooltip + generated Go doc-comment)
  - `category: enum(compute,control,state)` (required at archetype layer; inherited at kind layer)
  - `extends: string` (optional; references another manifest's `id`. Empty at archetype layer that has no parent archetype.)
  - `callable: bool` (default `true` for kind, `false` for archetype)
  - `attrs: { <name>: AttrSpec }` (map; ordered preserved for codegen; see FR-003)
  - `ports: { inputs: [PortSpec], outputs: [PortSpec] }` (PortSpec mirrors the existing `Port` Go type plus a `default_for: <archetype>` flag)
  - `budget: BudgetSignature` (declares which kernel-budget dials this kind consumes; one of `llm | tool | none | inherit`)
  - `executor: string` (required for callable kinds; references a Go symbol registered in the executor registry; ignored at archetype layer)
  - `aliases: [string]` (deprecated names that resolve to this kind; loader emits warning when used)
  - `defaults: { <attr>: any }` (per-attr default values; merged into the resolved manifest)
  - `version: string` (manifest revision; mirrors the activity loader's version tag)
- **FR-003** `AttrSpec` type:
  - `type: enum(string,int,float,bool,duration,enum,object,array,map,model_ref,tool_ref,activity_ref,corpus_ref,attachment_ref,node_id_ref,port_ref,messages_ref)`
  - `required: bool` (default `false`)
  - `default: any` (typed-checked against `type`)
  - `enum: [string]` (only valid when `type: enum`)
  - `min: number` / `max: number` (only valid for numeric types)
  - `min_length: int` / `max_length: int` (only valid for string and array types)
  - `description: string` (becomes Go doc-comment on the generated struct field; surfaced in frontend tooltip)
  - `json_field: string` (the on-the-wire JSON field name; defaults to lowercase of attr name)
  - `go_field: string` (the Go struct field name; defaults to PascalCase)
  - `deprecated_alias_of: string` (maps a renamed attr to its new name during YAML load)

### 4.2 Inheritance resolver

- **FR-004** Loader resolves `extends:` chains at load time. For each kind manifest:
  1. Parse the kind's YAML.
  2. If `extends:` is set, recursively resolve the parent.
  3. Deep-merge: scalars at the kind level override the parent; list fields (`aliases`, `inputs`, `outputs`) replace whole; the `attrs` map merges key-by-key (attr-level deep merge); `defaults` map merges key-by-key.
  4. Cycle detection: an `extends:` cycle is a load-time error.
  5. Multiple inheritance is **not** supported in v1 (single-parent only). A kind declares zero or one `extends:`.
- **FR-005** The resolved manifest carries provenance: each merged field tracks whether it came from the kind, archetype, or grandparent archetype. Surfaced in `nodes.ResolvedManifest.Provenance` map for debugging and for the frontend "show inheritance" tooltip.
- **FR-006** Only one archetype per category may be the "root" (the archetype with no `extends:`). Validation: each kind's archetype chain must terminate at a root archetype matching its category.
- **FR-007** Archetypes are NOT callable. A kind manifest with `callable: true` (the default) but referencing an archetype as its own `id` is a validation error. A kind manifest with `executor: ""` is a validation error. An archetype manifest with `executor: <anything>` is a validation error.

### 4.3 Codegen

- **FR-008** A new tool at `core/agentgraph/nodes/cmd/gen/main.go` (run via `go generate ./core/agentgraph/nodes/...`) reads every manifest, resolves inheritance, and emits two files:
  - `core/agentgraph/attrs_gen.go` — one `<KindPascal>Attrs` struct per callable kind, implementing `NodeAttrs` (zero-arg `nodeAttrsMarker()` + manifest-driven `Validate()`). Replaces the hand-written content of `core/agentgraph/attrs.go`.
  - `core/agentgraph/wire_gen.go` — replaces the hand-written switch in `decodeAttrs` plus `defaultAttrsFor` plus `AllNodeKinds()` and the `NodeKind*` constants.
- **FR-009** Generator is **idempotent**: running `go generate ./...` twice produces the same byte-equal output. CI runs `go generate ./...` and fails if `git diff --exit-code` shows changes.
- **FR-010** Generated files carry a `// Code generated by agentgraph/nodes/cmd/gen; DO NOT EDIT.` header. `golangci-lint` excludes them from style checks where appropriate. Tests do not depend on hand-edited values in `_gen.go` files.
- **FR-011** Generator emits validator helpers: a `func (a XxxAttrs) Validate() error` whose body is derived from the manifest's per-attr `required`/`min`/`max`/`enum` constraints. The hand-written `Validate()` methods on each *Attrs struct are removed.
- **FR-012** Generator emits a manifest registry: `var ResolvedManifests = map[NodeKind]*nodes.ResolvedManifest{...}` populated at init time. The validator and the kernel both read it.

### 4.4 Validator refactor

- **FR-013** `core/agentgraph/validator.go` no longer hand-codes per-kind rules for required attrs, port counts, port types, or attr ranges. Instead it consults the resolved manifest's declared schema.
- **FR-014** Cycle detection (the loop-body cycle exception), orphan-node detection, edge endpoint validity, dial-reference resolution, and activity-reference resolution remain hand-coded — they are graph-level concerns not derived from per-kind schemas. The mandatory-cap rule for `loop` / `retry` / `review` (NFR-004) is expressed in the manifest as `required: true` on the `max_iterations` / `max_attempts` attrs and is therefore manifest-driven.
- **FR-015** Validator emits errors that point at the offending node ID **and** the manifest constraint that fired (e.g., `node "n3" violates manifest model.attrs.max_tokens.min: got -1, want >= 1`).
- **FR-016** Validator gains an "archetype non-callable" rule: a graph that declares `kind: read` (an archetype id) fails to load with a clear "archetype 'read' is not directly callable in v1; use a concrete kind: history_read | corpus_read | memory | attachment" message.

### 4.5 Executor binding

- **FR-017** A new package `core/agentgraph/nodes/executors.go` holds the executor registry: `func Register(name string, fn ExecutorFunc)` and `func Lookup(name string) (ExecutorFunc, bool)`.
- **FR-018** Each executor source file (`exec_compute.go`, `exec_control.go`, `exec_state.go`) calls `Register("agentgraph.ExecModel", ExecModel)` from a package-level `init()`. Existing executors are renamed to match the new manifest-declared symbols (`ExecLLM` → `ExecModel`, `ExecBranch` (the predicate router) → `ExecDecision`, `ExecFork` → `ExecBranch`, etc.).
- **FR-019** The kernel resolves the executor at node-fire time via `Lookup(manifest.Executor)`. Lookup miss is a hard error at graph validation time (validator iterates kinds and checks every executor reference resolves).

### 4.6 User override

- **FR-020** Loader resolution order: shipped manifests at `core/agentgraph/nodes/manifests/*.yaml` (embedded) → user manifests at `<DataDir>/agent_graph/nodes/*.yaml`. User overrides deep-merge over shipped manifests of the same `id`. Mirrors the activities loader's pattern (see `core/agentgraph/activities/loader.go`).
- **FR-021** Forbidden user-override fields: `id`, `category`, `extends`, `executor`. Attempting to override them triggers a load-time error with the offending file path. (Rationale: changing executor would silently rewire a kind to different Go code.)
- **FR-022** Allowed user-override fields: `display_name`, `description`, `attrs.<name>.default`, `attrs.<name>.min`/`max`/`enum`, `defaults.*`, `aliases` (additive only — cannot remove shipped aliases), `version`.
- **FR-023** User overrides are loaded at session start. A nice-to-have hot-reload during `wails dev` watches the directory and re-resolves on change (see NFR-007). v1 may ship without hot-reload; the doctor command surfaces resolved-manifest provenance.
- **FR-024** Frontend exposes resolved manifest with provenance: each attr shows its effective default and the layer that contributed it (shipped vs user override).

### 4.7 Frontend palette + attribute editor

- **FR-025** GraphEditor's node-type picker renders a tree: Category → Archetype → Kind. Archetypes are visible but greyed out / non-droppable (with a "v1: archetypes are abstract" tooltip). Concrete kinds are draggable.
- **FR-026** Selecting a placed node opens an attribute editor whose form fields derive from the resolved manifest's `attrs:` map. Each field's input type is chosen by `AttrSpec.type`:
  - `string` → text input
  - `int`/`float` → number input with `min`/`max` bounds
  - `bool` → checkbox
  - `enum` → dropdown
  - `model_ref` → model picker (existing component)
  - `tool_ref` → tool picker
  - `activity_ref` → activity picker
  - `messages_ref` / `node_id_ref` / `port_ref` → graph-aware reference picker
  - `object` / `map` / `array` → JSON textarea fallback (v1; richer editors are future)
- **FR-027** Frontend ships a `ManifestStore` (loaded once at startup via a new `Nodes_Catalog` RPC) and a `useNodeManifest(id)` Vue composable.
- **FR-028** Each archetype's own attrs (defined at archetype layer) appear above the kind's own attrs in the editor, separated by a divider with the archetype label.

### 4.8 Taxonomy reconciliation

The current 23 kinds collapse + rename as follows. **New names are the load-bearing on-the-wire NodeKind values; old names are aliases.**

#### Compute category — archetype `compute`

- **FR-029** Archetype `compute`: declares one input port `input: messages` (default), one output port `output: messages`, budget signature `llm`. Attrs common: `provider`, `model`, `system_prompt`, `tool_allowlist`, `max_tokens`, `temperature`, `json_schema`, `stream_to_chat`.
- **FR-030** Kind `model` (was `LLMNode`) — extends `compute`. Alias: `llm`. Default executor: `agentgraph.ExecModel`. No new attrs beyond the compute archetype's.
- **FR-031** Kind `planner` (was `PlanNode`) — extends `compute`. Alias: `plan`. Adds `verbosity: enum(terse|standard|verbose)`, `planner_model: model_ref`, `threshold_input: string`. Executor: `agentgraph.ExecPlanner`.
- **FR-032** Kind `tool` — extends `compute` (because the kernel's policy gate, telemetry, and budget-LLM-equivalent are similar even though no LLM call occurs; budget signature is `tool` not `llm` — overrides the archetype). Alias: none (the on-the-wire name was always `tool`). Executor: `agentgraph.ExecTool`. Attrs: `name: string` (required), `args: object`.
  - **Note**: alternatively `tool` could be its own archetype root. We pick membership in `compute` because the tool node has a "compute-shaped" surface (input → output, telemetry, budget). Cross-cutting concerns (policy, telemetry) live at the `compute` archetype layer.
- **FR-033** Kind `transform` — extends `compute` with `budget: none` override. Alias: none. Executor: `agentgraph.ExecTransform`. Attrs: `name: string` (required), `params: object`.
- **FR-034** Kind `activity` — extends `compute` with `budget: inherit` override. Alias: none. Executor: `agentgraph.ExecActivity`. Attrs: `activity_id: activity_ref`, `version: string`, `inputs: object`.
- **FR-035** Kind `reflect` — extends `compute`. Alias: none. Executor: `agentgraph.ExecReflect`. Adds `severity_threshold: enum(low|medium|high)`, `include_trace: bool`, `max_iterations: int` (required when in a loop body — manifest declares `required_in_loop_body: true`, validator-driven).
- **FR-036** Kind `review` — extends `compute`. Alias: none. Executor: `agentgraph.ExecReview`. Adds `upstream_node: node_id_ref` (required), `max_iterations: int` (required, default 3, min 1), `on_cap_hit: enum(escalate|halt)`.
- **FR-037** Kind `ask` — extends `compute` with `budget: none` override (no LLM call; user-input wait). Alias: none. Executor: `agentgraph.ExecAsk`. Attrs: `question: string` (required).
- **FR-038** Kind `escalate` — extends `compute`. Alias: none. Executor: `agentgraph.ExecEscalate`. Adds `target_model: model_ref` (required), `upstream_node: node_id_ref` (required), `confidence_floor: float` (range `[0,1]`), `one_escalation_only: bool`.
- **FR-039** Kind `compact` — **NEW** — extends `compute`. Alias: none. Executor: `agentgraph.ExecCompact`. First-class compaction primitive that emits a `BranchResult` port (existing port type from the parent mission's compaction subsystem). Attrs: `strategy: enum(summary|drop_oldest|semantic_cluster|custom_subgraph)` (required), `target_token_budget: int`, `custom_subgraph_id: activity_ref` (required when `strategy=custom_subgraph`). **Coordinates with Bundle D** of the parent mission: the existing `core/agentgraph/compaction/` subsystem now invokes a `compact` node when fired from any of the three invocation sites (pre-call, post-tool, manual). Existing direct calls to compaction strategies continue to work; the manifest is the surfaced primitive for graph authors.

#### Control category — archetype `control`

- **FR-040** Archetype `control`: declares input port `signal: any` (default), output port `next: any`. Budget signature `none`. Common attrs: none at archetype layer.
- **FR-041** Kind `decision` (was `BranchNode` predicate router) — extends `control`. **Alias: `branch` (the OLD name)**. Executor: `agentgraph.ExecDecision`. Attrs: `condition: string` (required, expression), `next_true: node_id_ref` (required), `next_false: node_id_ref` (required).
- **FR-042** Kind `branch` (was `ForkNode` sub-graph spawn) — extends `control`. **Alias: `fork`**. Executor: `agentgraph.ExecBranch`. Attrs: `title: string` (required), `parent_leaf: node_id_ref`, `model_override: model_ref`, `tool_allowlist: [string]`, `message_subset: [node_id_ref]`. **Naming rationale**: the user-facing concept "branch" maps to the sub-graph spawn (US2 in parent spec). A predicate router is more naturally named "decision". The migration cost is one alias-resolved load.
- **FR-043** Kind `parallel` — extends `control`. Alias: none. Executor: `agentgraph.ExecParallel`. Attrs: `fan_out: int`, `targets: [node_id_ref]` (required, min_length 1), `max_concurrency: int` (default 4).
- **FR-044** Kind `join` — extends `control`. Alias: none. Executor: `agentgraph.ExecJoin`. Attrs: `from: [node_id_ref]` (required, min_length 1), `order: enum(declared|first_done)`.
- **FR-045** Kind `merge` — extends `control`. Alias: none. Executor: `agentgraph.ExecMerge`. Attrs: `mode: enum(append|summarize_append|replace_last_turn)` (default `summarize_append`), `target_branch: node_id_ref`.
- **FR-046** Kind `loop` — extends `control`. Alias: none. Executor: `agentgraph.ExecLoop`. Attrs: `max_iterations: int` (required, min 1), `condition: string`, `body: [node_id_ref]` (required, min_length 1).
- **FR-047** Kind `retry` — extends `control`. Alias: none. Executor: `agentgraph.ExecRetry`. Attrs: `max_attempts: int` (required, min 1), `backoff_base_ms: int`, `backoff_max_ms: int`, `body: [node_id_ref]` (required, min_length 1).
- **FR-048** Kind `approval` — **NEW** — extends `control`. Alias: none. Executor: `agentgraph.ExecApproval`. Human-in-the-loop policy gate. Attrs: `approver_role: string` (default `"user"`), `policy_label: string`, `auto_approve_window_seconds: int` (0 = no auto-approve), `prompt: string`. Distinct from `ask` (free-form text elicitation) — `approval` is a binary yes/no gate. Persists a `pending_approval` event mirroring `pending_ask`.

#### State category — archetype tree `state → {read, write, marker}`

State uses a **three-layer inheritance chain** (`state → read → <kind>`) rather than the flat sibling layout used in Compute/Control. Rationale: read/write/marker share enough invariants (no LLM budget, optional provenance, hooks-on-write) that a common ancestor is justified. The kind manifest declares its parent via `extends:`; the resolver walks the full chain at load time per FR-004 (single-parent, N-deep).

- **FR-048a** Archetype `state` (extends parent: none). Category `state`. Declares no required ports. Budget signature `none`. Common attrs: `provenance: bool` (default true) — whether the operation surfaces a `(path, hash, scope)` triple to the EventLog. `callable: false`.
- **FR-049** Archetype `read` (extends `state`). Declares input port `query: any`, output port `result: messages`. Budget signature inherits `none` from `state`. Common attrs: `source: enum(history|corpus|memory|attachment|file|bash_output)` (required) — discriminator. `callable: false`.
- **FR-050** Archetype `write` (extends `state`). Declares input port `payload: any`, output port `ack: bool`. Budget signature inherits `none`. Common attrs: `target: enum(memory|corpus|trace|file|artifact)` (required). `callable: false`.
- **FR-051** Kind `history_read` (was `HistoryReadNode`) — extends `read`. Alias: none. Executor: `agentgraph.ExecHistoryRead`. Defaults `source: history`. Attrs: `branch_id: string`, `last_n: int` (default 20).
- **FR-052** Kind `corpus_read` (was `CorpusReadNode`) — extends `read`. Alias: none. Executor: `agentgraph.ExecCorpusRead`. Defaults `source: corpus`. Attrs: `corpus_ids: [corpus_ref]`, `top_k: int` (default 10), `score_threshold: float`, `source_path_prefix: string`, `mime_types: [string]`.
- **FR-053** Kind `memory` — extends `read` **AND `write`** via dual archetype membership? **No — single inheritance only (FR-004).** Resolution: `memory` extends `read` and the `write` flavor is implemented through a `mode: enum(read|write|upsert)` attr in the manifest. The executor branches on `mode`. Alternative considered (one kind per mode: `memory_read`, `memory_write`) was rejected because the existing `MemoryNode` already supports all three modes via a single executor and breaking it apart would churn callers needlessly.
  - **Pragma**: in the manifest, `memory` declares `extends: read` (because read is the more common case), but its `attrs.mode` enum drives the runtime behavior. The validator emits a warning when `mode: write` is used on a `memory` node downstream of a port typed `messages` (because writes don't produce messages).
- **FR-054** Kind `corpus_write` (was `CorpusWriteNode`) — extends `write`. Alias: none. Executor: `agentgraph.ExecCorpusWrite`. Defaults `target: corpus`. Attrs: `corpus_id: corpus_ref` (required), `source_path: string`, `chunker: string`.
- **FR-055** Kind `attachment` (was `AttachmentNode`) — extends `read`. Alias: none. Executor: `agentgraph.ExecAttachment`. Defaults `source: attachment`. Attrs: `attachment_id: attachment_ref` (required), `as_content_block: bool` (default true).
- **FR-056** Kind `trace_write` (was `TraceWriteNode`) — extends `write`. Alias: none. Executor: `agentgraph.ExecTraceWrite`. Defaults `target: trace`. Attrs: `severity: enum(debug|info|warn|error)` (default `info`), `message: string` (required), `attrs: object`.
- **FR-057** Kind `checkpoint` (was `CheckpointNode`) — extends `marker` archetype (extends `state`). Resolution: archetype `marker` (extends `state`, no required attrs, `callable: false`) groups kernel-control state markers. `checkpoint` is its first concrete kind. Executor: `agentgraph.ExecCheckpoint`. Attrs: `label: string`.
- **FR-057a** Kind `read_file` — **NEW** — extends `read`. Defaults `source: file`. Executor: `agentgraph.ExecReadFile`. Attrs: `path: string` (required), `encoding: enum(utf8|base64)` (default `utf8`), `as_attachment: bool` (default false — when true, registers the file content in attachments and emits an `attachment_ref` instead of inline messages). **Distinct from `tool` filesystem reads** — see §4.9 State-vs-Tool framing. File contents are tracked in the EventLog with provenance (`path`, `sha256`, `mtime`), participate in greedy memory hooks per FR-027 of the parent mission, and are eligible for the configurable compaction subsystem.
- **FR-057b** Kind `read_bash_output` — **NEW** — extends `read`. Defaults `source: bash_output`. Executor: `agentgraph.ExecReadBashOutput`. Attrs: `bash_run_id: string` (required), `tail_bytes: int` (default 0 = full), `include_stderr: bool` (default true). Reads cached output from a prior bash tool run, surfacing it as context-tracked artifact rather than ephemeral tool return.
- **FR-057c** Kind `write_file` — **NEW** — extends `write`. Defaults `target: file`. Executor: `agentgraph.ExecWriteFile`. Attrs: `path: string` (required), `content: messages_ref` (required), `mode: enum(create|append|replace)` (default `create`), `policy_label: string`. **Distinct from filesystem-MCP tool writes** (see §4.9): write_file participates in policy gating + EventLog provenance + the artifacts subsystem; tool-side writes are one-shot side effects.
- **FR-058** Kind `artifact` — **NEW** — extends `write`. Alias: none. Executor: `agentgraph.ExecArtifact`. Terminal output. Attrs: `mime_type: string` (required), `output_target: enum(session_message|file_path|report)` (required, default `session_message`), `attachment_ref: attachment_ref`, `content: messages_ref`. Replaces ad-hoc end-of-graph "dump the final answer to chat" patterns.

#### Inheritance chain example (3-layer)

The chain `state → read → corpus_read` resolves at load time as:

```
state (callable=false, provenance attr)
└── read (callable=false, adds source enum, ports)
    └── corpus_read (callable=true, source=corpus default, adds corpus_ids/top_k)
```

The resolved manifest for `corpus_read` carries every attr/port from all three layers with provenance tags. The Go layer sees only the flat resolved manifest — no Go-side inheritance, no struct embedding.

### 4.9 State-vs-Tool framing decision

Two of the new kinds (`read_file`, `read_bash_output`, `write_file`) overlap conceptually with existing **filesystem-MCP** tool dispatches. The framing rule:

- **State (Read/Write archetype)** — the operation produces / consumes content the graph wants to **track in context**. Provenance recorded in EventLog (`path`, `sha256`, `mtime`, `scope`). Greedy memory hooks fire. Eligible for compaction. Output participates in the context graph as a first-class artifact.
- **Tool (Compute archetype, `tool` kind)** — the operation is a **one-shot side effect**. Output is ephemeral (tool return), policy-gated via Cedar `Tool::"<server>__<tool>"`, but not retained in context unless the graph author explicitly persists it via a `write` kind downstream.

**Decision rule for graph authors**: if you want the result to *be remembered and reasoned about later*, use a State kind (`read_file`, `read_bash_output`, `corpus_read`, etc.). If it's a fire-and-forget action whose return value is consumed once and discarded, use `tool`.

**FR-058a** The migration **does not** remove filesystem-MCP tool dispatches. Both surfaces coexist:
- Filesystem MCP tool: backward compat for existing graphs + tool-shaped UX (single tool call returns content, no context tracking).
- `read_file` / `write_file` state kinds: new context-aware path; preferred for any graph that wants greedy memory + provenance + compaction eligibility.

**FR-058b** Cedar policy gating is consistent across both surfaces: filesystem accesses go through `Filesystem::"<path>"` regardless of which kind initiated them. The State kinds add a `Read::"<source>"` / `Write::"<target>"` action UID layer for finer-grained gating (e.g., "permit read_file from `~/Documents/projects/**` but not from `~/.ssh`").

#### Reconciliation summary

| Old kind | New kind | Archetype | Alias retained? |
|---|---|---|---|
| `llm` | `model` | `compute` | yes (`llm` → `model`) |
| `tool` | `tool` | `compute` | n/a (no rename) |
| `transform` | `transform` | `compute` | n/a |
| `activity` | `activity` | `compute` | n/a |
| `reflect` | `reflect` | `compute` | n/a |
| `review` | `review` | `compute` | n/a |
| `plan` | `planner` | `compute` | yes (`plan` → `planner`) |
| `ask` | `ask` | `compute` | n/a |
| `escalate` | `escalate` | `compute` | n/a |
| (NEW) | `compact` | `compute` | n/a |
| `branch` (predicate) | `decision` | `control` | yes (`branch` → `decision`) |
| `fork` (sub-graph) | `branch` | `control` | yes (`fork` → `branch`) |
| `parallel` | `parallel` | `control` | n/a |
| `join` | `join` | `control` | n/a |
| `merge` | `merge` | `control` | n/a |
| `loop` | `loop` | `control` | n/a |
| `retry` | `retry` | `control` | n/a |
| (NEW) | `approval` | `control` | n/a |
| `memory` | `memory` | `read` (with `mode` attr) | n/a |
| `corpus_read` | `corpus_read` | `read` | n/a |
| `corpus_write` | `corpus_write` | `write` | n/a |
| `attachment` | `attachment` | `read` | n/a |
| `history_read` | `history_read` | `read` | n/a |
| `trace_write` | `trace_write` | `write` | n/a |
| `checkpoint` | `checkpoint` | `marker` (extends `state`) | n/a |
| (NEW) | `artifact` | `write` (extends `state`) | n/a |
| (NEW) | `read_file` | `read` (extends `state`) | n/a |
| (NEW) | `read_bash_output` | `read` (extends `state`) | n/a |
| (NEW) | `write_file` | `write` (extends `state`) | n/a |

**Net**: 23 old → 29 new (6 added: `compact`, `approval`, `artifact`, `read_file`, `read_bash_output`, `write_file`). 4 renames with retained aliases (`llm`, `plan`, `branch`, `fork`).

**Archetype inventory (8)**: `compute`, `control`, `state`, `read` (extends `state`), `write` (extends `state`), `marker` (extends `state`). All `callable: false` in v1.

---

## 5. Non-functional requirements

- **NFR-001** `go test -race -count=1 -short ./core/...` passes; new packages add tests; codegen tests verify byte-equal idempotency.
- **NFR-002** Frontend tests + build clean.
- **NFR-003** **Backward compat**: existing graphs (referencing old kind names) load with a deprecation warning + automatic alias resolution. The deprecation warning is emitted to:
  1. The trace (as a `kind_alias_resolved` event with old + new names + manifest version).
  2. The structured log at `WARN` level.
  3. The frontend graph editor (a yellow banner on the loaded graph).
  Alias removal is announced in the deprecation warning ("removal in next minor version after this lands"). v1 ships with all four aliases active.
- **NFR-004** **Codegen idempotency**: `go generate ./...` followed by `git diff --exit-code` exits 0 in CI.
- **NFR-005** **Single source of truth**: a manifest's declared default value flows to (a) the generated Go struct's zero-value-via-init or default helper, (b) the validator's "unset → default" pass, (c) the frontend attribute editor's placeholder. Tests assert all three derive from the manifest.
- **NFR-006** **No silent override**: any user-override at `<DataDir>/agent_graph/nodes/*.yaml` is logged at startup with the kind ID and the diff (key paths) at INFO level. Frontend "show inheritance" tooltip surfaces the same.
- **NFR-007** **Hot reload (nice-to-have, not gating)**: while `wails dev` is running, the loader re-resolves manifests when the manifest directory changes. v1 may ship without this; if absent, the doctor RPC `Nodes_ReloadCatalog` provides a manual trigger.
- **NFR-008** **No new third-party dependencies**: stdlib + already-in-tree `gopkg.in/yaml.v3` only. No code-gen library outside stdlib `go/ast` + `text/template`.
- **NFR-009** **Charter DIRECTIVE_001 (no cyclic imports)**: `core/agentgraph/nodes/` consumes nothing from `core/agentgraph/` other than the existing public types (`NodeAttrs`, `Port`, `PortType`, `NodeKind`). The dependency arrow is `agentgraph → agentgraph/nodes` (loader) and `agentgraph/nodes/cmd/gen → agentgraph/nodes` (codegen reads loader). No reverse imports.
- **NFR-010** **GUI-only, single-user, privacy** invariants from the parent mission carry over unchanged.
- **NFR-011** **Manifest count bound**: v1 ships ≤ 30 manifests (4 archetypes + 26 kinds). The loader and resolver are O(N) in manifest count and run in <50 ms for v1's set on a Mac M-series.
- **NFR-012** **Removal sunset**: the four shipped aliases (`llm`, `plan`, `branch`, `fork`) are documented for removal in the *next minor* version after this mission lands. The loader's deprecation warning includes the removal version.

---

## 6. Acceptance walkthroughs

- **A1** **Manifest loads and resolves**: place `core/agentgraph/nodes/manifests/compute.yaml` (archetype) and `model.yaml` (kind extends compute). Loader builds resolved manifest with merged attrs. `nodes.Catalog().Get("model").Provenance["max_tokens"]` returns `"compute"` (inherited).
- **A2** **Codegen round-trip**: run `go generate ./core/agentgraph/nodes/...`. Diff `attrs_gen.go` and `wire_gen.go`. Re-run `go generate`. `git diff --exit-code` exits 0.
- **A3** **Codegen drift fails CI**: hand-edit `attrs_gen.go` to remove a field. `go generate ./...` regenerates. `git diff --exit-code` exits 1. CI gate fires.
- **A4** **Validator drives off manifest**: graph YAML with `kind: model` and missing `model:` attr fails validation with error `node "n1": manifest model.attrs.model: required field not set`.
- **A5** **Validator out-of-range from manifest**: graph YAML with `kind: model, attrs: { temperature: 5 }` fails with `node "n1": manifest model.attrs.temperature.max: got 5.000, want <= 2.000`.
- **A6** **Archetype not callable**: graph YAML with `kind: read` fails with `archetype 'read' is not directly callable in v1; use a concrete kind: history_read | corpus_read | memory | attachment`.
- **A7** **Old name still works (alias)**: graph YAML with `kind: llm` loads, runs, and emits a deprecation warning event `kind_alias_resolved {old: llm, new: model, removal_in: <next-minor>}`.
- **A8** **Old fork → new branch alias**: graph YAML with `kind: fork` loads, resolves to `branch`, emits deprecation warning. `kind: branch` (predicate router under the OLD scheme) loads, resolves to `decision`, emits deprecation warning. Both run correctly.
- **A9** **User override re-tunes default**: drop `<DataDir>/agent_graph/nodes/review.yaml` with `defaults: { max_iterations: 5 }`. Restart harness. A new `review` node with no explicit `max_iterations` defaults to 5. Frontend "show inheritance" surfaces "5 (user-override)".
- **A10** **User override forbidden field rejected**: drop `<DataDir>/agent_graph/nodes/model.yaml` with `extends: control`. Loader fails to start with `user override for "model": forbidden field "extends"; user overrides may not change inheritance`.
- **A11** **New kind `compact`**: an LLMNode emits a long context, downstream `compact` kind (manifest declares `BranchResult` port and four-strategy enum) compacts and emits `BranchResult`. The compaction subsystem's pre-call invocation (Bundle D from parent mission) calls into the same executor. Result: token budget under cap.
- **A12** **New kind `approval`**: graph fires `approval` node mid-run; harness pauses, surfaces a yes/no modal in the UI; user clicks "Approve"; run continues. Auto-approve window of 0 means it never auto-approves.
- **A13** **New kind `artifact`**: graph terminates with `artifact` node `output_target: session_message`. The terminal output appears as a normal chat message rather than via ad-hoc dump. Same graph with `output_target: file_path` writes to disk under `<DataDir>/artifacts/<run_id>/<artifact_id>.<ext>`.
- **A14** **Frontend palette tree**: open GraphEditor, click "Add node". See Compute → (compute archetype greyed) → (model, planner, tool, transform, activity, reflect, review, ask, escalate, compact). Drag `model`. Attribute editor opens with manifest-driven fields.
- **A15** **Frontend attribute editor types**: each `AttrSpec.type` renders the right widget — int has number input with bounds, enum has dropdown, model_ref has model picker.
- **A16** **Hot reload (if shipped)**: with `wails dev` running, edit `core/agentgraph/nodes/manifests/model.yaml` to bump `max_tokens` default to 8192. New nodes use new default without restart. (If hot reload is not shipped in v1, the doctor RPC `Nodes_ReloadCatalog` produces equivalent behavior on demand.)
- **A17** **Inheritance provenance**: `Nodes_GetManifest("model")` returns a `ResolvedManifest` whose `Provenance["temperature"]` is `"compute"` (archetype) and `Provenance["model"]` is `"model"` (kind level — `model` itself sets `required: true` for the `model` attr).
- **A18** **Multi-level extends rejected**: a manifest declaring `extends: read, extends: write` (multiple) fails with `manifest "memory": multiple extends not supported in v1`.
- **A19** **Cycle in extends rejected**: manifest `a.yaml: extends: b`, `b.yaml: extends: a` fails with `manifest "a": extends cycle a → b → a`.
- **A20** **Executor lookup miss fails at validation, not run**: a manifest declaring `executor: agentgraph.ExecBogus` fails graph validation with `manifest "x": executor "agentgraph.ExecBogus" not registered`. Runs do not start.

---

## 7. Architecture

```
core/agentgraph/
├── spec.go                          # MODIFIED: NodeKind type stays; constants move to wire_gen.go
├── attrs.go                         # REPLACED by attrs_gen.go (this file deleted)
├── attrs_gen.go                     # NEW (generated): per-kind *Attrs structs
├── wire.go                          # MODIFIED: keeps wireGraph/wireNode + helpers; decoder switch moves to wire_gen.go
├── wire_gen.go                      # NEW (generated): NodeKind constants + decodeAttrs switch + AllNodeKinds + defaultAttrsFor
├── validator.go                     # MODIFIED: drops per-kind hand-coded rules; reads ResolvedManifests
├── exec_compute.go                  # MODIFIED: rename ExecLLM → ExecModel; init() registers
├── exec_control.go                  # MODIFIED: rename ExecBranch (predicate) → ExecDecision; ExecFork → ExecBranch
├── exec_state.go                    # MODIFIED: split or extend for `mode`-dispatched memory; init() registers
├── nodes/                           # NEW PACKAGE
│   ├── doc.go
│   ├── manifest.go                  # Manifest + AttrSpec + PortSpec Go types
│   ├── loader.go                    # Embedded + DataDir-override loading; mirrors activities/loader.go
│   ├── catalog.go                   # In-memory ResolvedManifests map; thread-safe
│   ├── resolver.go                  # extends-chain merge with provenance tracking
│   ├── executors.go                 # Executor registry: Register/Lookup
│   ├── manifest_validator.go        # Validates manifest YAML against the meta-schema
│   ├── catalog_test.go
│   ├── loader_test.go
│   ├── resolver_test.go
│   ├── manifests/                   # Embedded shipped manifests (//go:embed manifests/*.yaml)
│   │   ├── compute.yaml             # archetype
│   │   ├── control.yaml             # archetype
│   │   ├── read.yaml                # archetype
│   │   ├── write.yaml               # archetype
│   │   ├── marker.yaml              # archetype
│   │   ├── model.yaml               # kind
│   │   ├── tool.yaml
│   │   ├── transform.yaml
│   │   ├── activity.yaml
│   │   ├── reflect.yaml
│   │   ├── review.yaml
│   │   ├── planner.yaml
│   │   ├── ask.yaml
│   │   ├── escalate.yaml
│   │   ├── compact.yaml             # NEW
│   │   ├── decision.yaml
│   │   ├── branch.yaml              # NEW NAME for old fork
│   │   ├── parallel.yaml
│   │   ├── join.yaml
│   │   ├── merge.yaml
│   │   ├── loop.yaml
│   │   ├── retry.yaml
│   │   ├── approval.yaml            # NEW
│   │   ├── memory.yaml
│   │   ├── corpus_read.yaml
│   │   ├── corpus_write.yaml
│   │   ├── attachment.yaml
│   │   ├── history_read.yaml
│   │   ├── trace_write.yaml
│   │   ├── checkpoint.yaml
│   │   └── artifact.yaml            # NEW
│   └── cmd/
│       └── gen/
│           ├── main.go              # codegen entrypoint (`go run ./core/agentgraph/nodes/cmd/gen`)
│           ├── attrs.tmpl           # attrs_gen.go template
│           ├── wire.tmpl            # wire_gen.go template
│           └── main_test.go         # asserts byte-equal idempotency on a fixture manifest set

core/rpc/views/agentgraph/
├── api.go                           # MODIFIED: add Nodes_Catalog, Nodes_GetManifest, Nodes_ReloadCatalog
└── impl.go                          # MODIFIED

core/rpc/api.go                      # MODIFIED: wire nodes-catalog accessor
core/rpc/bindings.go                 # MODIFIED

frontend/src/lib/types.ts            # MODIFIED: ResolvedManifest, AttrSpec types
frontend/src/lib/harnessClient.ts    # MODIFIED: Nodes_Catalog, Nodes_GetManifest
frontend/src/views/graphs/
├── GraphSpecEditor.vue              # MODIFIED: palette tree from manifests
├── NodeAttributeEditor.vue          # NEW: manifest-driven attr form
├── NodePaletteTree.vue              # NEW: Category → Archetype → Kind tree
├── NodeInheritanceTooltip.vue       # NEW: provenance display
└── __tests__/

docs/agent-kernel-graph-node-catalog.md  # NEW (in this mission's polish WP)
```

---

## 8. Edge cases

1. **Manifest with no `extends:` at kind layer** — allowed only if the kind is its own archetype root (uncommon in v1; reserved for future extensibility). The loader treats it as a self-rooted kind; no inheritance merge.
2. **Manifest filename mismatches its `id` field** — load-time error.
3. **Two manifests with the same `id`** (e.g., both `corpus_read.yaml` and `corpus-read.yaml`) — load-time error.
4. **User override with no shipped counterpart** — load-time warning ("user manifest 'foo' has no shipped counterpart; treating as a new manifest"). v1 still loads it as a new kind iff it declares all required fields. Maintainers may decide to harden this to a hard error.
5. **Alias collision**: two kinds both declare `aliases: [legacy_name]` — load-time error.
6. **Alias points at a non-existent kind** — load-time error.
7. **Codegen with no manifests** — emits empty `attrs_gen.go` + `wire_gen.go` with a comment noting "no kinds present"; tests never let this state ship.
8. **Old kind name in a saved checkpoint** (graph spec persisted at run-start) — alias resolver runs at load; checkpoint re-serializes with the new name on next save.
9. **Manifest YAML has a typo in `type:` (e.g., `type: integer` instead of `int`)** — meta-schema validation fails loudly with the offending file path + line.
10. **`extends:` references a future kind not yet shipped** — load-time error ("manifest x extends 'y' which is not in the catalog"). Order-of-load doesn't matter; resolver runs after all manifests are parsed.
11. **Hot-reload picks up an invalid manifest** — log error, keep prior catalog, surface a "stale catalog" warning in the UI; next valid edit recovers.
12. **Frontend renders a manifest that uses an `AttrSpec.type` the frontend doesn't yet support** — fallback to JSON textarea with a warning; never crashes the editor.
13. **Codegen tool run in a checkout without YAML** (e.g., partial checkout) — fails with a clear error; never emits empty files silently.
14. **Override changes `attrs.<name>.type`** — load-time error (treated as a forbidden field change; only `default`, `min`/`max`, `enum`, `description` may differ between layers).
15. **Validator runs against a graph using a pre-rename name** — works via alias map; emits a warning event.

---

## 9. Out of scope (v1)

- **WASM / plugin executors**: a manifest's `executor:` is a Go-symbol reference only. WASM is a v2+ idea and would require a sandboxing model. Out of scope.
- **Multiple inheritance**: a kind extends one archetype only. Mixin-style multi-extend deferred.
- **Authoring brand-new kinds without a Go change**: in v1, a fully new kind requires a Go executor. Composition (`activity`) and override (override-only) cover the common cases.
- **Visual manifest editor in the frontend**: v1 is read-only display. Editing manifests is YAML-on-disk.
- **Promoting archetypes to callable**: `kind: read` with attr-dispatch is a v2 feature.
- **Cross-kind attr deduplication beyond inheritance**: e.g., a "policy_label" attr shared across `tool` and `approval`. v1 has each declare it; v2 could introduce attr-mixin manifests.
- **Manifest-driven RPC schema generation**: the Wails bindings remain hand-written. Generating them from manifests is interesting but out of scope.
- **Semantic-merge style features** for the new `merge` kind (rich semantic merging beyond `summarize_append` / `append` / `replace_last_turn`). Inherits from parent mission's §9 out-of-scope list.
- **Dial-aware archetypes**: archetypes do not yet declare which dials they consume. v1 keeps dial declaration at the kind level.
- **ARM-based codegen for a non-Go runtime**: only `attrs_gen.go` and `wire_gen.go` are produced. No TS codegen — the frontend reads manifests at runtime via RPC.
- **Loading manifests from arbitrary directories** beyond shipped + DataDir override.

---

## 10. Open questions

1. **Promote mid-level archetypes (`read`, `write`) to callable in v2?** Doing so would let a graph author write `kind: read, source: corpus` instead of `kind: corpus_read`. The runtime cost is a dispatch table; the spec cost is committing to attr-discriminator semantics. **Default**: defer to v2 once we see whether users prefer the discriminated form.
2. **Plugin executor sandboxing model when v2 lands**. Likely candidates: WASI WASM with capability-based filesystem/network. Out of scope here.
3. **Memory kind: single manifest with `mode` enum vs split into `memory_read` and `memory_write`?** v1 keeps the single-manifest with `mode` to avoid churning callers. If users find the discriminator awkward we can split in a follow-up (the alias system handles the migration).
4. **`compact` kind's coordination with the compaction subsystem**: the existing `core/agentgraph/compaction/` package is invoked from three sites (pre-call, post-tool, manual). Does `compact` *replace* those invocations, or *complement* them as a graph-author-visible primitive? **Default**: complement. The kernel-internal sites continue to call `compaction.Strategy.Apply()` directly; `compact` is the surfaced primitive when an author wants explicit control. A follow-up may unify them.
5. **Does the `tool` kind belong under the `compute` archetype**, or should we promote a `gateway` archetype (covering `tool`, `approval`, `escalate`)? The case for `gateway`: these three share the policy-gate + pause-not-kill + telemetry pattern. The case against: only three kinds and the cross-cutting concerns are equally well expressed at `compute` and `control` archetypes. **Default**: keep `tool` under `compute`. Revisit if a fourth gateway-shaped kind appears.
6. **Hot reload: ship in v1 or defer?** The implementation is small (`fsnotify` + re-resolve). The risk is in-flight runs holding stale manifests. **Default**: ship behind a `--enable-manifest-hot-reload` dev flag; not on by default in v1.
7. **Should the `tool_ref` and `model_ref` AttrSpec types resolve at validation time?** v1 says yes (the validator checks the model exists in the configured providers). But this couples the validator to the LLM/tool subsystems. Alternative: leave it to runtime (cleaner separation). **Default**: validator-time resolution because it catches typos earlier; the coupling is already there via dial validation.
8. **Frontend handling of object/map/array attr types**: v1 uses a JSON textarea. For complex object attrs (e.g., `args` on `tool`) this is awkward. Future work: structured form generation from a nested schema. Out of scope here.
9. **Executor registry: `init()` side-effects vs explicit `RegisterAll()`?** FR-018 currently uses `init()` calls in each `exec_*.go` source file (idiomatic stdlib pattern: `database/sql.Register`, image-format decoders, etc.). Modern Go style guides increasingly prefer explicit `RegisterAll(reg *Registry)` called once from `core/core.go` startup — same effect, no init-time side effects, easier to reason about test ordering and dependency graphs. **Default**: ship with `init()` per FR-018 to match the existing harness conventions; revisit if test isolation or import-ordering bugs surface. Trade-off: explicit `RegisterAll` adds one wiring line at chassis init but removes a class of "import-for-side-effects" footguns.
10. **Read/Write file vs filesystem-MCP coexistence**: §4.9 commits to coexistence in v1 (both surfaces shipped, framing rule documented). If users overwhelmingly migrate to State kinds, a follow-up mission could deprecate the filesystem-MCP recipe. Conversely if State kinds see no uptake, a follow-up could remove them. v1 is reversible by design.

---

## 11. Migration path

- **Old graph YAML files** (e.g., `kind: llm`, `kind: branch` predicate, `kind: fork`): load through the alias map. Deprecation warnings emit. Existing tests using old names continue to pass after the manifest+codegen lands.
- **Existing in-tree call sites** referencing `agentgraph.NodeKindLLM` / `NodeKindBranch` / `NodeKindFork`: these constants remain in `wire_gen.go` (with the old names mapped via aliases declared in their respective new manifests). A separate "rename in code" follow-up mission can flip call sites; this mission's WP04 makes the old constants thin wrappers around the new ones.
- **Hand-written `attrs.go`** is deleted at WP02 boundary. Anything that imported `agentgraph.LLMAttrs` continues to work because `attrs_gen.go` re-exposes the same name (manifest declares `go_field` to preserve the old PascalCase shape).
- **Existing executor functions** are renamed in WP04. The kernel kicks off via `nodes.Lookup(manifest.Executor)`, so the rename is a code-only change with no observable behavior change.
- **No SQLite migration** is required by this mission. The parent mission's migrations 0306–0309 are unaffected. Saved graph specs in `<DataDir>/agent_graph/library/` referencing old names auto-resolve.
- **Sunset window**: aliases ship in v1 of this mission. Removal is announced in deprecation warnings. The follow-up mission `agent-kernel-graph-alias-sunset` (TBD ID) drops them at the next minor release.

---

## 12. Mission shape

This mission is medium-sized. Scope groups into **two bundles**:

- **Bundle A — Manifests + codegen + validator + executor binding** (foundational; gates B). WP01 ships the loader and resolver; WP02 ships the codegen (replacing `attrs.go` and the decoder switch); WP03 refactors the validator; WP04 reconciles taxonomy with the alias map; WP05 adds the three new kinds (`compact`, `approval`, `artifact`); WP07 wires user override.
- **Bundle B — Frontend + polish** (depends on Bundle A). WP06 is the frontend palette + attribute editor; WP08 is docs + integration tests + deprecation surfacing.

Plan.md details bundle DAG and per-WP sequencing.

---

## 13. Out-of-band dependencies

- **Existing in-tree**: `core/agentgraph/` (parent mission), `core/agentgraph/activities/` (override-pattern reference), `core/agentgraph/compaction/` (Bundle D coordination for `compact` kind).
- **Vendored**: `gopkg.in/yaml.v3` (already in tree).
- **Stdlib only for codegen**: `text/template`, `go/format`, `go/ast` if needed.
- **No new third-party SDK**.
- **Frontend**: existing GraphEditor scaffolding from parent mission's WP09 / Bundle B.
- **No migrations** required; no schema changes to SQLite.
