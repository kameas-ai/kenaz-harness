# Implementation Plan: Agent kernel graph — manifest-driven node catalog with archetype inheritance

**Branch**: `agent-kernel-graph-node-catalog-01KQ7JDZ` (lane allocated at WP-implement time)
**Date**: 2026-04-27
**Spec**: `kitty-specs/agent-kernel-graph-node-catalog-01KQ7JDZ/spec.md`
**Parent**: `kitty-specs/agent-kernel-graph-01KQ6391/` (feature-complete on `main`)

---

## Summary

Replace the parent mission's four-file-per-kind pattern (enum constant + Go *Attrs struct + wire decoder switch + per-kind hand-coded validator rules + executor function) with a **manifest-as-truth declarative system** rooted in `core/agentgraph/nodes/manifests/*.yaml`.

Three taxonomy layers become first-class:

- **Category** (compute / control / state) — organizational only; never callable.
- **Archetype** (compute, control, read, write, marker) — abstract contracts; non-callable in v1.
- **Kind** (model, tool, decision, branch, ...) — concrete callable nodes with `executor:` references.

A loader merges `extends:` chains at startup. A codegen tool emits `attrs_gen.go` and `wire_gen.go` from the resolved manifests in CI. The validator no longer hand-codes per-kind rules; it reads the resolved manifest. User overrides at `<DataDir>/agent_graph/nodes/*.yaml` deep-merge over shipped manifests, mirroring the activities subsystem pattern at `core/agentgraph/activities/loader.go`.

Three new kinds ship: `compact` (first-class compaction primitive coordinating with parent mission's Bundle D), `approval` (binary human-in-the-loop gate distinct from `ask`), `artifact` (terminal output).

Four renames retain aliases for backward compat: `llm → model`, `plan → planner`, `branch (predicate) → decision`, `fork → branch (sub-graph)`. Aliases sunset in the next minor release.

The frontend's GraphEditor renders a Category → Archetype → Kind palette tree; per-node attribute editors are manifest-driven. No new SQLite migrations.

The mission ships in **two bundles + polish**: Bundle A (manifests + codegen + validator + taxonomy reconciliation + new kinds + user override) and Bundle B (frontend palette + integration tests + docs).

---

## Technical Context

- **Language/Version**: Go 1.22+; TypeScript 5.x (frontend manifest store + Vue components).
- **Primary dependencies (in-tree)**: `core/agentgraph` (parent mission); `core/agentgraph/activities/` (loader pattern reference); `core/agentgraph/compaction/` (`compact` kind coordination); `core/policy` (existing Cedar gate; `approval` reuses); existing frontend `GraphSpecEditor`.
- **Vendored**: `gopkg.in/yaml.v3` (already in tree).
- **No new third-party deps**: codegen uses stdlib `text/template` + `go/format`. No `tree-sitter`, no `protobuf`-like generators.
- **Storage**:
  - Shipped manifests at `core/agentgraph/nodes/manifests/*.yaml`, embedded via `//go:embed`.
  - User override directory: `<DataDir>/agent_graph/nodes/*.yaml`.
  - Generated Go: `core/agentgraph/attrs_gen.go` and `core/agentgraph/wire_gen.go` (committed to repo; CI regenerates and asserts byte-equal).
  - **No SQLite migrations**.
- **Testing**: Go `-race -count=1 -short`; vitest for frontend. Codegen has its own idempotency test fixture set under `core/agentgraph/nodes/cmd/gen/testdata/`.
- **Performance**: NFR-011 manifest loading O(N) in <50 ms on M-series.
- **Constraints**: NFR-003 backward compat via aliases; NFR-004 codegen idempotency; NFR-005 single source of truth; NFR-008 no new third-party deps; NFR-009 no cyclic imports.

---

## Charter Check

- **DIRECTIVE_001 (no cyclic imports)**: `core/agentgraph/nodes/` imports `core/agentgraph` (for `NodeAttrs`, `Port`, `PortType`, `NodeKind` types). The reverse is _not_ allowed. To break the potential cycle: the manifest types live in `core/agentgraph/nodes/manifest.go` and the kernel reads the resolved catalog through a small interface (`type ManifestCatalog interface { Get(NodeKind) *ResolvedManifest }`) declared inside `core/agentgraph/seams.go` and satisfied by `nodes.Catalog`. **Pass** (verify in WP01 review).
- **C-001 (no third-party SDK in `core/`)**: stdlib + vendored YAML only. **Pass.**
- **Privacy CI invariants**: manifests + user overrides under `<DataDir>` only; no network calls. **Pass.**
- **Single-user / GUI-only**: no CLI surface added. The `go generate` codegen runs at build time, not runtime; not a CLI surface. **Pass.**
- **Cedar policy gating**: unchanged; `approval` kind reuses the existing policy infrastructure. **Pass.**
- **Bounded execution**: unchanged; the manifest's `required: true` on `max_iterations` / `max_attempts` is the surfaced expression of NFR-004 from the parent mission. **Pass.**

---

## Project Structure

(Mirrors spec.md §7. Generated files marked `*_gen.go` are committed to the repo; CI regenerates and diffs.)

---

## Phase 0 — Research summary

- **Manifest schema**: a Go struct `Manifest` lives at `core/agentgraph/nodes/manifest.go`. YAML on disk validates against it via a meta-schema check at load time. Mirrors the way `core/agentgraph/spec.go` validates the `Graph` struct.
- **Override pattern**: identical to `core/agentgraph/activities/loader.go` — `//go:embed manifests/*.yaml` → `<DataDir>/agent_graph/nodes/*.yaml` → deep-merge with override winning, but **forbidden fields** (`id`, `category`, `extends`, `executor`) are rejected at load time.
- **Codegen**: `core/agentgraph/nodes/cmd/gen/main.go` reads every manifest, resolves inheritance, emits two files via `text/template`. Stdlib `go/format` formats the output. Idempotency is verified by running gen twice and byte-comparing.
- **Inheritance resolver**: walks `extends:` from kind → archetype, deep-merges field-by-field. Provenance is tracked in a `map[string]string` (field path → contributing layer name). Single inheritance only in v1; multiple inheritance rejected at parse time.
- **Validator refactor**: per-kind hand-coded rules in `attrs.go`'s `Validate()` methods are replaced by a manifest-driven `validateAttrs(kind, attrs)` function in the validator. Hand-coded rules that remain: cycle-outside-loop-body detection, orphan-node detection, edge-endpoint validity, dial-reference resolution, activity-reference resolution. The `loop`/`retry`/`review` mandatory-cap rules become `attrs.max_iterations.required: true` in the manifest.
- **Executor binding**: a small registry pattern. `core/agentgraph/nodes/executors.go` exposes `Register(name, fn)` + `Lookup(name)`. Each executor file (`exec_compute.go`, `exec_control.go`, `exec_state.go`) has an `init()` that calls `Register`. The kernel's executor dispatch in `kernel.go` swaps from a `switch kind` to `Lookup(manifest.Executor)`.
- **Taxonomy reconciliation**: the alias map is implemented inside the loader. After parsing each manifest, the loader builds a `map[string]NodeKind` (alias → canonical kind). On graph load, the wire decoder consults the alias map first, emits a deprecation event, then delegates to the canonical kind's decoder.
- **Compaction coordination**: `compact` kind is the first-class graph primitive. The existing `core/agentgraph/compaction/` invocation sites (pre-call, post-tool, manual) continue to call into the strategy directly; this mission does not refactor them. A graph author who wants explicit compaction control uses the new `compact` kind.
- **Frontend**: a `Nodes_Catalog` RPC returns the resolved manifest list (with provenance) at session start. A new `useNodeManifest(id)` Vue composable feeds the attribute editor; the palette tree component reads `category` + `extends` to build the hierarchy.

---

## Phases (mapped to FR set)

- **Phase 1 — Manifest schema + loader + resolver** (FR-001..FR-007).
- **Phase 2 — Codegen** (FR-008..FR-012).
- **Phase 3 — Validator refactor** (FR-013..FR-016).
- **Phase 4 — Executor registry + binding** (FR-017..FR-019).
- **Phase 5 — Taxonomy reconciliation + alias map** (FR-029..FR-058 renames; backward compat NFR-003).
- **Phase 6 — New kinds: compact, approval, artifact** (FR-039, FR-048, FR-058).
- **Phase 7 — User override** (FR-020..FR-024).
- **Phase 8 — Frontend palette + attribute editor** (FR-025..FR-028).
- **Phase 9 — Polish: deprecation surfacing + docs + integration tests** (NFR-006 + acceptance walkthroughs).

---

## Work-package breakdown (2 bundles + polish, ~8 WPs)

### Bundle A — Manifests + codegen + validator + reconciliation (foundational; gates Bundle B)

#### WP01 — Manifest schema + loader + inheritance resolver

**Phases**: 1.

**Scope**:
- New package `core/agentgraph/nodes/`: `manifest.go` (types), `loader.go` (embedded + DataDir), `resolver.go` (extends-chain merge with provenance), `catalog.go` (thread-safe map), `manifest_validator.go` (meta-schema check), `executors.go` (registry stub — populated in WP04).
- Embed `core/agentgraph/nodes/manifests/` directory; ship the **archetype manifests only** in this WP (compute, control, read, write, marker). Kind manifests land in WP04.
- Loader returns `Catalog` satisfying a small interface `core/agentgraph/seams.go: ManifestCatalog`.
- Resolver is read-only at this WP; no codegen yet, no taxonomy reconciliation.

**Acceptance**:
- A1 loads.
- Round-trip: `LoadCatalog → Get(archetypeID).Provenance` returns expected layer attribution.
- A18 (multi-extends rejection), A19 (cycle rejection) pass.
- Charter pre-flight: no cyclic imports.

#### WP02 — Codegen of `attrs_gen.go` + `wire_gen.go`

**Phases**: 2.

**Scope**:
- New tool at `core/agentgraph/nodes/cmd/gen/main.go` with `go:generate` directive in `core/agentgraph/spec.go`.
- Templates `attrs.tmpl` + `wire.tmpl`.
- Generated files: `core/agentgraph/attrs_gen.go` (per-kind structs + manifest-driven `Validate()`), `core/agentgraph/wire_gen.go` (NodeKind constants + decoder switch + `AllNodeKinds` + `defaultAttrsFor`).
- **Delete** `core/agentgraph/attrs.go` (its content is replaced by generated). Keep `wire.go` for the unchanged `wireGraph`/`wireNode`/`graphToWire`/`wireToGraph` helpers; move only the per-kind switch into `wire_gen.go`.
- CI gate: `make generate` (or `go generate ./core/agentgraph/nodes/...`) followed by `git diff --exit-code`.

**Acceptance**:
- A2 round-trip idempotency.
- A3 drift fails CI.
- All existing `core/agentgraph/*_test.go` tests still pass after `attrs.go` is replaced by `attrs_gen.go` (the generated file declares the same struct names + fields preserving backward compat for in-tree consumers).

**Dependencies**: WP01 (resolver supplies the input).

#### WP03 — Validator refactor

**Phases**: 3.

**Scope**:
- Rewrite `core/agentgraph/validator.go` to drive per-kind rules off `nodes.Catalog().Get(kind).ResolvedManifest`.
- Keep cycle-outside-loop-body, orphan-node, edge-endpoint, dial-ref, activity-ref rules (graph-level concerns).
- Add archetype-not-callable rule (FR-016).
- Add executor-registered rule (FR-019 enforcement: validator iterates kinds, asserts every `executor:` resolves via `nodes.Lookup`).
- Update `validator_test.go` accordingly: many existing rule tests get reframed as manifest-driven (the tests' expected error strings change).

**Acceptance**:
- A4, A5, A6, A20 pass.
- Existing parent-mission tests that hit per-kind rules pass with manifest-driven equivalents.

**Dependencies**: WP01 (Catalog), WP02 (generated NodeKind constants drive AllNodeKinds()).

#### WP04 — Taxonomy reconciliation + alias map

**Phases**: 5.

**Scope**:
- Author the **kind manifests** under `core/agentgraph/nodes/manifests/`: `model.yaml`, `tool.yaml`, `transform.yaml`, `activity.yaml`, `reflect.yaml`, `review.yaml`, `planner.yaml`, `ask.yaml`, `escalate.yaml`, `decision.yaml`, `branch.yaml` (NEW NAME for old fork), `parallel.yaml`, `join.yaml`, `merge.yaml`, `loop.yaml`, `retry.yaml`, `memory.yaml`, `corpus_read.yaml`, `corpus_write.yaml`, `attachment.yaml`, `history_read.yaml`, `trace_write.yaml`, `checkpoint.yaml`. (Note: `compact`/`approval`/`artifact` arrive in WP05.)
- Rename existing executor functions: `ExecLLM → ExecModel`, `ExecBranch (predicate) → ExecDecision`, `ExecFork → ExecBranch`, `ExecPlan → ExecPlanner`. Each `init()` registers under the new name.
- Each renamed kind's manifest declares `aliases: [old_name]`.
- Alias map implementation in `core/agentgraph/nodes/loader.go`: at parse time, the loader builds `aliasIndex map[string]NodeKind`; the wire decoder consults it before falling through to the canonical-name path.
- Deprecation event: `kind_alias_resolved {old, new, removal_in}` emitted to the trace and the structured log.
- Update parent-mission YAML fixtures (e.g., `library/toolloop_default.yaml`) to use the new names; old-name fixtures retained under `core/agentgraph/testdata/aliases/` to exercise the alias path.
- Re-run `go generate ./...` to regenerate `attrs_gen.go` + `wire_gen.go` with the new kinds.

**Acceptance**:
- A7 (`llm` resolves to `model`), A8 (`fork` → `branch`, old `branch` → `decision`) pass.
- All parent-mission test fixtures pass with renamed kinds.
- Deprecation warnings appear in trace.

**Dependencies**: WP01, WP02, WP03.

#### WP05 — New kinds: `compact`, `approval`, `artifact`

**Phases**: 6.

**Scope**:
- Author manifests `compact.yaml`, `approval.yaml`, `artifact.yaml`.
- Implement executors `ExecCompact`, `ExecApproval`, `ExecArtifact` in the appropriate `exec_*.go` files; register in `init()`.
- `ExecCompact` coordinates with `core/agentgraph/compaction/`: it invokes the configured strategy and emits a `BranchResult` port.
- `ExecApproval` reuses the `pending_ask` pattern from the parent mission's `ExecAsk` (rename the underlying primitive to `pendingHumanGate` so it can serve both `ask` and `approval`). Persist `pending_approval` event; survives restarts.
- `ExecArtifact` writes terminal output: session message, file path under `<DataDir>/artifacts/<run_id>/`, or report.
- Re-run `go generate ./...`.
- Tests: per-kind unit tests + integration test for `compact` driving the existing compaction strategies.

**Acceptance**:
- A11 (compact emits BranchResult, kicks under cap).
- A12 (approval pauses + auto-approve window).
- A13 (artifact writes session message + file path).

**Dependencies**: WP01, WP02, WP03, WP04.

#### WP07 — User override (`<DataDir>/agent_graph/nodes/*.yaml`)

**Phases**: 7.

**Scope**:
- Extend `core/agentgraph/nodes/loader.go` with the `UserDir` option (mirrors `activities/loader.go: LoadOptions.UserDir`).
- Forbidden-field rejection (FR-021): `id`, `category`, `extends`, `executor` cannot differ between layers.
- Allowed-field deep-merge (FR-022).
- Logging at INFO (NFR-006): kind ID + JSON-pointer paths whose effective value differs from the shipped manifest.
- Doctor RPC `Nodes_ReloadCatalog` (NFR-007 fallback if hot-reload not shipped).
- Tests: override changes default; override changes range; override forbidden-field rejected; missing user dir is silent no-op.

**Acceptance**:
- A9 (`review.yaml` defaults max_iterations to 5).
- A10 (forbidden field rejected).
- A17 (provenance reflects override).

**Dependencies**: WP01.

(WP06 is in Bundle B below.)

### Bundle B — Frontend + polish

#### WP06 — Frontend palette tree + manifest-driven attribute editor

**Phases**: 8.

**Scope**:
- New RPC `Nodes_Catalog`: returns the resolved manifest list (with provenance) at session start. RPC view at `core/rpc/views/agentgraph/api.go` + `impl.go`. Wails bindings regen.
- New RPC `Nodes_GetManifest(id)`: full single-manifest fetch.
- New RPC `Nodes_ReloadCatalog`: doctor-style manual reload (paired with WP07).
- Frontend types: `frontend/src/lib/types.ts` adds `ResolvedManifest`, `AttrSpec`, `PortSpec`. `harnessClient.ts` wraps the new RPCs.
- New components:
  - `frontend/src/views/graphs/NodePaletteTree.vue` — Category → Archetype → Kind tree; archetypes greyed out with tooltip.
  - `frontend/src/views/graphs/NodeAttributeEditor.vue` — manifest-driven form; switches widget by `AttrSpec.type`.
  - `frontend/src/views/graphs/NodeInheritanceTooltip.vue` — provenance display ("default 5 (user-override)").
- Modify `GraphSpecEditor.vue` to consume the new components.
- Vitest tests for each new component.

**Acceptance**:
- A14 palette tree renders.
- A15 attr editor types render correctly.
- A17 provenance tooltip shows correct attribution.

**Dependencies**: WP01..WP05 (manifests must exist), WP07 (override → provenance).

#### WP08 — Polish: deprecation surfacing + docs + integration tests + hot-reload (optional)

**Phases**: 9.

**Scope**:
- Frontend banner on graph load when alias resolved (NFR-003 surfacing #3).
- `docs/agent-kernel-graph-node-catalog.md` — manifest authoring guide, override guide, alias migration table, codegen runbook.
- Integration tests for A1..A20 (driven via existing `core/agentgraph/testdata/` fixture infrastructure).
- **Optional**: `fsnotify`-based hot reload behind a `--enable-manifest-hot-reload` dev flag (NFR-007). Ship if scope permits; otherwise the doctor RPC from WP07 covers the gap.
- Performance smoke: 30-manifest load + resolve <50 ms (NFR-011).
- Lint sweep on generated files (golangci-lint exclusion config).

**Acceptance**:
- A1..A20 all pass as integration fixtures.
- Doc renders + matches reality.
- Banner shows on alias-loaded graphs.

**Dependencies**: WP01..WP07.

---

## Bundle DAG and WP sequencing

```
Bundle A (foundational)                   Bundle B (depends on A)

WP01 (manifest+loader+resolver)
  │
  ├──→ WP02 (codegen)
  │      │
  │      └──→ WP03 (validator refactor)
  │             │
  │             └──→ WP04 (taxonomy reconciliation + aliases)
  │                    │
  │                    └──→ WP05 (new kinds: compact, approval, artifact)
  │                             │
  └──→ WP07 (user override)     │
                                ↓
                          WP06 (frontend palette + attr editor)
                                │
                                └──→ WP08 (polish + docs + integration + hot-reload)
```

**Recommended sequencing**:

1. **WP01** first — foundational; no other WP starts until manifests can be loaded and resolved.
2. **WP02 + WP03** can begin in lockstep once WP01 lands. WP02 provides the generated NodeKind constants; WP03 needs them to drive validator's iteration. In practice WP02 → WP03.
3. **WP07** (user override) is **independent of WP02..WP05** — it can start in parallel with WP02 once the loader exists. Sequence: WP01 → WP07 in parallel with WP02.
4. **WP04** (rename + aliases) requires WP02 + WP03 because it ships kind manifests that drive codegen and exercises the validator-driven rules.
5. **WP05** (new kinds) requires WP04 because it ships more kind manifests on top of the reconciled set.
6. **WP06** (frontend) can be drafted against WP01's data shape early but cannot land until WP04 exposes the full kind set.
7. **WP08** is last; integration tests need the full set live.

**Independent ship boundaries**:
- Bundle A's WP01..WP05 + WP07 ship as a unit (they alter Go code under `core/agentgraph` and depend on the regenerated files being in lock-step). Splitting them risks half-applied renames.
- Bundle B (WP06 + WP08) ships as a unit afterward.

---

## Risk register

| Risk | Phase / WP | Mitigation |
|---|---|---|
| **Codegen drift between hand-edits and generated files** | 2 / WP02 | CI gate `git diff --exit-code` after `go generate`; doc clearly marks generated files; lint excludes them. |
| **Manifest-meta-schema lock-in** (we ship a schema and authors depend on it; later changes break manifests) | 1 / WP01 | Treat the meta-schema as a public surface; bump a `manifest_schema_version` field at the top of each manifest; loader reads it and rejects unknown major versions. Single-user / local-only means we can iterate aggressively if needed. |
| **Alias map drift** (renames not reflected in all fixtures) | 5 / WP04 | A test asserts every YAML fixture under `core/agentgraph/testdata/` either uses the new name or is explicitly marked as an "alias-path test". Automation: `grep -r 'kind: llm\|kind: fork\|kind: plan'` in non-alias-test YAML fails CI. |
| **Validator rule expressivity gap** (a hand-coded rule has nuance the manifest schema can't capture) | 3 / WP03 | Keep an "extension hook" in the validator: if a kind's manifest declares `extra_validate: true`, fall through to a hand-coded rule registered in a small map. Use sparingly; document each use. |
| **Multiple inheritance creep** (someone wants `memory` to extend both `read` and `write`) | 1 / WP01 | Spec mandates single inheritance; the `mode` discriminator pattern is the v1 answer. If pressure mounts, follow-up mission can introduce mixin manifests. |
| **Compact-kind double-firing** (graph author places `compact` AND the kernel's pre-call site fires) | 6 / WP05 | Document the semantic: the `compact` kind is for graph-author-explicit control; pre-call/post-tool sites continue to fire automatically. The double-fire is benign (idempotent compaction). Add a guard in the kernel: skip pre-call compaction for the next-firing if a `compact` kind precedes the LLM node. |
| **Approval auto-approve race**: window expires while the user is mid-click | 6 / WP05 | The auto-approve event timestamp is captured at fire-time, not at resume-time; if the user clicks "Approve" before the timestamp, the click wins. |
| **Artifact file-path traversal** (user writes `output_target: file_path, attachment_ref: "../../etc/passwd"`) | 6 / WP05 | Validator rejects file_path containing `..` or absolute paths. All artifact files land under `<DataDir>/artifacts/<run_id>/`. Cedar policy gate on file write. |
| **Frontend manifest-store staleness** (the user edits a manifest while a session is open) | 8 / WP06 | The session uses the manifest version it captured at start; if a manifest changed, surface a "stale catalog — restart to pick up" toast. Hot-reload (NFR-007) optional. |
| **Codegen test-flakiness on Windows** (line-ending differences) | 2 / WP02 | Generator emits LF only; codegen test normalizes line endings before byte-comparing. |
| **Order-dependent manifest loading** (a kind's `extends:` references an archetype that loads later) | 1 / WP01 | Two-pass loader: pass 1 parses all files; pass 2 resolves `extends:`. Already implicit in the resolver but call out in tests. |
| **Cyclic import during refactor**: `nodes` package needs `agentgraph` types; `agentgraph` validator needs `nodes` catalog | 1 / WP01 | Invert via interface: `core/agentgraph/seams.go` declares `ManifestCatalog` interface; `core/agentgraph/nodes/Catalog` satisfies it. The kernel/validator depends on the interface. |
| **User-override overwrites a critical default and breaks the harness** (e.g., `loop.max_iterations` default removed) | 7 / WP07 | Forbidden-field list (FR-021) prevents the worst cases (executor, extends, category). For tunable fields: log the override at INFO; expose in UI; doctor RPC can dump the resolved manifest. |
| **Sunset window too short for users to migrate** | 11 / WP08 | One-minor-version sunset announced in the deprecation warning; doc lists migration steps; alias removal is its own follow-up mission. |

---

## Migration path

- **Existing graph YAML** (e.g., `library/toolloop_default.yaml`, custom user library files) referencing old names auto-resolves via the alias map. Deprecation warnings appear in the trace.
- **Existing parent-mission test fixtures** are mass-updated in WP04. A small set of "alias-path" fixtures retains old names to exercise the deprecation code.
- **Existing in-tree call sites** (`agentgraph.NodeKindLLM`, etc.) — the constants in `wire_gen.go` map old names to new via aliases, so call sites compile unchanged. A separate follow-up mission flips call sites to the new constants.
- **No SQLite migrations**.
- **Saved checkpoints** referencing old kind names: alias resolver runs at load; checkpoint re-serializes with new names on next save.
- **Sunset**: aliases ship in v1 of this mission. Removal is announced in deprecation warnings + this mission's docs. The follow-up alias-sunset mission drops them at the next minor release.

---

## Open questions (carried from spec.md §10)

1. **Promote mid-level archetypes (`read`, `write`) to callable in v2?** Default: defer.
2. **Plugin executor sandboxing model when v2 lands**. Default: WASI WASM is the leading candidate; out of scope here.
3. **Memory kind: single manifest with `mode` enum vs split**? Default: single with `mode`.
4. **`compact` kind coordination with the compaction subsystem** — replace or complement? Default: complement.
5. **Should `tool` move under a `gateway` archetype** with `approval` and `escalate`? Default: keep under `compute`.
6. **Hot reload in v1 or defer**? Default: ship behind a dev flag, not on by default.
7. **Should `tool_ref` / `model_ref` resolve at validation time or runtime**? Default: validation time (catches typos earlier).
8. **Object/map/array attr type rendering**: JSON textarea v1; richer editors deferred.

---

## Counts

- **8 WPs** across 2 bundles + polish.
- **Bundle A**: WP01–WP05 + WP07 (6 WPs).
- **Bundle B**: WP06, WP08 (2 WPs).
- **FRs**: 58 (FR-001..FR-058).
- **NFRs**: 12 (NFR-001..NFR-012).
- **Acceptance walkthroughs**: 20 (A1..A20).
- **User stories**: 10 (US1..US10).
- **Open questions**: 8.
