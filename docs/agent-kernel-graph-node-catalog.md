# Agent kernel graph — manifest-driven node catalog

This document is the authoring reference for the manifest-driven node
catalog that backs the agent-kernel-graph runtime. It distils the
mission spec at `kitty-specs/agent-kernel-graph-node-catalog-01KQ7JDZ/`
into a working guide for harness maintainers and graph authors.

The catalog lives at `core/agentgraph/nodes/`. Manifests ship under
`core/agentgraph/nodes/manifests/*.yaml` and are embedded into the
binary; user overrides drop into `<DataDir>/agent_graph/nodes/*.yaml`.
Codegen produces `core/agentgraph/attrs_gen.go` and
`core/agentgraph/wire_gen.go` from the manifest set.

## Architecture overview

The catalog has three load-bearing layers:

1. **Manifest** — one YAML file per archetype or per kind. Declares the
   id, category, parent (`extends:`), attributes (with types, ranges,
   enums, defaults), ports (inputs / outputs), budget signature,
   executor reference, and deprecation aliases. Schema is fixed by the
   `Manifest` struct in `core/agentgraph/nodes/manifest.go`.
2. **Resolved manifest** — the deep-merged result of walking the
   `extends:` chain root-to-leaf. Carries field-by-field provenance
   (`Provenance map[string]string`) so the frontend can render
   inheritance tooltips and the validator can attribute errors.
3. **Generated Go shape** — `attrs_gen.go` defines one
   `<KindPascal>Attrs` struct per callable kind plus a manifest-driven
   `Validate()` method; `wire_gen.go` defines the `NodeKind*` constants,
   `AllNodeKinds()`, the alias map, the `defaultAttrsFor` /
   `defaultPortsFor` dispatch tables, and the `decodeAttrs` JSON
   switch. Both files are regenerated from the manifest set on every
   `go generate ./core/agentgraph/...`.

```
┌──────────────────────────────┐
│ core/agentgraph/nodes/       │
│  manifests/<id>.yaml         │  shipped manifests (//go:embed)
│  manifests/_archetype.<id>   │  archetype manifests
└──────────────┬───────────────┘
               │
               ▼
┌──────────────────────────────┐
│ <DataDir>/agent_graph/nodes/ │  user overrides (allowed-field merge)
└──────────────┬───────────────┘
               │
               ▼
┌──────────────────────────────┐  ┌──────────────────────────────┐
│ LoadCatalog(opts) → Catalog  │─▶│ codegen (text/template):     │
│  Manifests parsed + resolved │  │  attrs_gen.go + wire_gen.go  │
│  Provenance + Chain tracked  │  └──────────────────────────────┘
└──────────────┬───────────────┘
               │
               ▼
┌──────────────────────────────┐  ┌──────────────────────────────┐
│ validator.go (FR-013/015/016)│  │ kernel.go: dispatch via      │
│  reads ResolvedManifests     │  │  nodes.Lookup(executor) on   │
└──────────────────────────────┘  │  each node fire              │
                                  └──────────────────────────────┘
```

The `core/agentgraph/seams.go` file declares the `ManifestCatalog`
interface so `core/agentgraph/` consumes the resolved catalog without
importing back into `core/agentgraph/nodes/`. Charter directive 001
(no cyclic imports) holds.

## Categories, archetypes, kinds

The taxonomy has three load-bearing tiers:

- **Category** (`compute`, `control`, `state`) — organisational only;
  never callable, never declared in `extends:`. Used by the frontend
  palette grouping.
- **Archetype** — abstract contract. Declares `callable: false`; defines
  required ports, common attrs, budget signature. Children inherit via
  `extends:`. Six in v1: `compute`, `control`, `state`, `read`
  (extends `state`), `write` (extends `state`), `marker` (extends
  `state`).
- **Kind** — concrete callable node. Declares `callable: true`,
  `extends: <archetype>`, and `executor: <Go-symbol-id>`.

### Kind catalog (29 callable + 6 archetypes)

The table is grouped by category → archetype. Each row links the
manifest YAML (relative to repo root) and the Go executor symbol.

#### Compute (archetype `compute`)

| Kind | Manifest | Executor | Description |
|---|---|---|---|
| `model` | [`core/agentgraph/nodes/manifests/model.yaml`](../core/agentgraph/nodes/manifests/model.yaml) | `agentgraph.ExecModel` | LLM-powered reasoning / generation. Alias: `llm`. |
| `transform` | [`transform.yaml`](../core/agentgraph/nodes/manifests/transform.yaml) | `agentgraph.ExecTransform` | Pure-Go transform (concat, json_extract, truncate_tokens, uppercase). Budget: `none`. |
| `activity` | [`activity.yaml`](../core/agentgraph/nodes/manifests/activity.yaml) | `agentgraph.ExecActivity` | Sub-graph reference. Budget: `inherit`. |
| `reflect` | [`reflect.yaml`](../core/agentgraph/nodes/manifests/reflect.yaml) | `agentgraph.ExecReflect` | Self-reflection on prior trace. |
| `review` | [`review.yaml`](../core/agentgraph/nodes/manifests/review.yaml) | `agentgraph.ExecReview` | Critic loop with explicit cap (`max_iterations`). |
| `planner` | [`planner.yaml`](../core/agentgraph/nodes/manifests/planner.yaml) | `agentgraph.ExecPlanner` | Plan generation with verbosity dial. Alias: `plan`. |
| `ask` | [`ask.yaml`](../core/agentgraph/nodes/manifests/ask.yaml) | `agentgraph.ExecAsk` | Free-form user question (pause + resume). Budget: `none`. |
| `escalate` | [`escalate.yaml`](../core/agentgraph/nodes/manifests/escalate.yaml) | `agentgraph.ExecEscalate` | Escalate to a stronger model with confidence floor. |
| `compact` | [`compact.yaml`](../core/agentgraph/nodes/manifests/compact.yaml) | `agentgraph.ExecCompact` | First-class compaction primitive (FR-039). Coordinates with `core/agentgraph/compaction/`. |

#### Compute / Tool (archetype `tool`, extends `compute`)

Builtin-tool node kinds. `_archetype.tool.yaml` is the abstract
contract (`args`/`result` ports, `budget: tool`, the `kenaz__<kind>`
naming contract that fixes which tool a kind dispatches — see the file
itself for the full rationale). A callable kind declares only its args
schema and gets a registered executor for free: `core/agentgraph/
executor.go`'s `builtinToolExecutors()` derives one `builtinToolExecutor`
per resolved manifest whose `extends:` chain contains `tool`, so there
is no second hand-maintained dispatch table to keep in sync. The old
generic `tool` kind (`name:` attr picking the tool at author time) is
gone — a kind IS the unit of authorisation and accounting now.

| Kind | Manifest | Executor | Description |
|---|---|---|---|
| `sleep` | [`sleep.yaml`](../core/agentgraph/nodes/manifests/sleep.yaml) | `agentgraph.ExecSleep` | Yield N seconds without consuming an iteration slot (FR-010). Budget: `none`. |
| `subagent_dispatch` | [`subagent_dispatch.yaml`](../core/agentgraph/nodes/manifests/subagent_dispatch.yaml) | `agentgraph.ExecSubagentDispatch` | Spawn a sub-agent branch from a named profile. |

#### Control (archetype `control`)

| Kind | Manifest | Executor | Description |
|---|---|---|---|
| `decision` | [`decision.yaml`](../core/agentgraph/nodes/manifests/decision.yaml) | `agentgraph.ExecDecision` | Predicate router (`condition` → `next_true` / `next_false`). Alias: `branch` (legacy). |
| `branch` | [`branch.yaml`](../core/agentgraph/nodes/manifests/branch.yaml) | `agentgraph.ExecBranch` | Sub-graph spawn (formerly `fork`). Alias: `fork`. |
| `parallel` | [`parallel.yaml`](../core/agentgraph/nodes/manifests/parallel.yaml) | `agentgraph.ExecParallel` | Fan-out across targets. |
| `join` | [`join.yaml`](../core/agentgraph/nodes/manifests/join.yaml) | `agentgraph.ExecJoin` | Synchronisation point. |
| `merge` | [`merge.yaml`](../core/agentgraph/nodes/manifests/merge.yaml) | `agentgraph.ExecMerge` | Branch-output merge with `mode` discriminator. |
| `loop` | [`loop.yaml`](../core/agentgraph/nodes/manifests/loop.yaml) | `agentgraph.ExecLoop` | Bounded loop body. `max_iterations` required. |
| `retry` | [`retry.yaml`](../core/agentgraph/nodes/manifests/retry.yaml) | `agentgraph.ExecRetry` | Retry-with-backoff. `max_attempts` required. |
| `approval` | [`approval.yaml`](../core/agentgraph/nodes/manifests/approval.yaml) | `agentgraph.ExecApproval` | Binary HITL gate (FR-048). |

#### State / Read (archetype chain `state → read`)

| Kind | Manifest | Executor | Description |
|---|---|---|---|
| `history_read` | [`history_read.yaml`](../core/agentgraph/nodes/manifests/history_read.yaml) | `agentgraph.ExecHistoryRead` | Session-history slice. |
| `corpus_read` | [`corpus_read.yaml`](../core/agentgraph/nodes/manifests/corpus_read.yaml) | `agentgraph.ExecCorpusRead` | Embedding-search retrieval over registered corpora. |
| `memory` | [`memory.yaml`](../core/agentgraph/nodes/manifests/memory.yaml) | `agentgraph.ExecMemory` | Memory store ops; `mode` enum drives `read` / `write` / `upsert`. |
| `attachment` | [`attachment.yaml`](../core/agentgraph/nodes/manifests/attachment.yaml) | `agentgraph.ExecAttachment` | Resolve an attachment id to a content block. |
| `read_file` | [`read_file.yaml`](../core/agentgraph/nodes/manifests/read_file.yaml) | `agentgraph.ExecReadFile` | Context-tracked file read with provenance (FR-057a). |
| `read_bash_output` | [`read_bash_output.yaml`](../core/agentgraph/nodes/manifests/read_bash_output.yaml) | `agentgraph.ExecReadBashOutput` | Cached bash-tool output as a context-tracked artifact (FR-057b). |

#### State / Write (archetype chain `state → write`)

| Kind | Manifest | Executor | Description |
|---|---|---|---|
| `corpus_write` | [`corpus_write.yaml`](../core/agentgraph/nodes/manifests/corpus_write.yaml) | `agentgraph.ExecCorpusWrite` | Persist a chunk into a corpus. |
| `trace_write` | [`trace_write.yaml`](../core/agentgraph/nodes/manifests/trace_write.yaml) | `agentgraph.ExecTraceWrite` | Emit a structured trace event. |
| `write_file` | [`write_file.yaml`](../core/agentgraph/nodes/manifests/write_file.yaml) | `agentgraph.ExecWriteFile` | Policy-gated, provenance-tracked file write (FR-057c). |
| `artifact` | [`artifact.yaml`](../core/agentgraph/nodes/manifests/artifact.yaml) | `agentgraph.ExecArtifact` | Terminal output. `output_target` ∈ `session_message` / `file_path` / `report`. |

#### State / Marker (archetype chain `state → marker`)

| Kind | Manifest | Executor | Description |
|---|---|---|---|
| `checkpoint` | [`checkpoint.yaml`](../core/agentgraph/nodes/manifests/checkpoint.yaml) | `agentgraph.ExecCheckpoint` | Kernel-control marker; fires the greedy memory hook. |

### Archetype manifests (7, `callable: false`)

`compute`, `control`, `state`, `read` (extends `state`),
`write` (extends `state`), `marker` (extends `state`),
`tool` (extends `compute`).

## Filesystem access is one mechanism

`read_file` / `write_file` / `read_bash_output` are State kinds because
a graph node's job is putting content into the run's *tracked* context:
provenance recorded to the `EventLog` (`path`, `sha256`, `mtime`),
greedy-memory hooks firing post-read/write, compaction eligibility.
`kenaz__read_file` / `kenaz__write_file` (`core/tools/fsbuiltins`) are
the same filesystem operations reached through the tool-call surface a
model dispatches on its own initiative, gated by the interactive
permission flow in `core/tools/fs.Gate` rather than the node-level
Cedar `Read::"file"` / `Write::"file"` pair (FR-058b) — a real
difference, kept because retargeting either surface's dispatch would
silently change which permission UX gates an existing graph.

What used to be a doctrine (§4.9, deleted — "pick a kind based on
whether you want it remembered") is now moot: both surfaces record the
same provenance. `appendFSToolProvenance` in
`core/agentgraph/tool_invocation.go` — the single call site every tool
invocation already passes through, node-dispatched or
model-dispatched — emits the identical `file_read` / `file_write`
EventLog record for `kenaz__read_file` / `kenaz__write_file` calls that
the State-kind executors emit for `read_file` / `write_file` nodes.
There is no authoring choice left to arbitrate: a graph author places a
`read_file` node when they're authoring the graph; a model calls
`kenaz__read_file` when it decides to read something mid-run. Neither
one is the "remembered" path anymore — both are. `read_bash_output` has
no competing tool surface at all (the cache it reads is kernel-internal
to the bash tool), so it was never actually in doctrine's scope.

## Multi-layer inheritance: the `corpus_read` example

The chain `state → read → corpus_read` resolves at load time. Each
layer contributes some attributes; the resolved manifest tracks
provenance per field.

`_archetype.state.yaml` (root):

```yaml
schema_version: "1"
archetype: state
category: state
description: "Reads / writes durable harness state."
callable: false
attrs:
  provenance:
    type: bool
    default: true
    description: "When true, surface a (path, hash, scope) triple to the EventLog."
budget: none
```

`_archetype.read.yaml` (extends `state`):

```yaml
schema_version: "1"
archetype: read
extends: state
description: "Reads from durable harness state into the active context."
callable: false
ports:
  inputs:  [{ name: query,  type: any }]
  outputs: [{ name: result, type: messages }]
attrs:
  source:
    type: enum
    enum: [history, corpus, memory, attachment, file, bash_output]
budget: none
```

`corpus_read.yaml` (extends `read`):

```yaml
schema_version: "1"
id: corpus_read
extends: read
display_name: Corpus Read
executor: agentgraph.ExecCorpusRead
defaults:
  source: corpus
attrs:
  corpus_ids:
    type: "[]string"
    required: true
    min_length: 1
  top_k:
    type: int
    min: 0
budget: none
```

The resolved manifest, surfaced via `Catalog.Get("corpus_read")`,
carries:

- `Manifest.Category = "state"` (inherited from `state`)
- `Manifest.Attrs["provenance"]` (from `state`)
- `Manifest.Attrs["source"]` (from `read`)
- `Manifest.Attrs["corpus_ids"]`, `top_k`, etc. (from `corpus_read`)
- `Chain = ["state", "read", "corpus_read"]`
- `Provenance["attrs.provenance"] = "state"`,
  `Provenance["attrs.source"] = "read"`,
  `Provenance["attrs.corpus_ids"] = "corpus_read"`

The Go layer sees only the flat resolved manifest — no Go-side
inheritance, no struct embedding. The frontend palette renders the
chain so the inheritance tooltip can attribute each effective default
to a layer (FR-005, FR-024).

## User override workflow

Drop a manifest at `<DataDir>/agent_graph/nodes/<id>.yaml`. The loader
deep-merges it over the shipped manifest of the same `id`.

**What you can change:**

- `display_name`, `description`
- `defaults.<key>` (any default value)
- `attrs.<name>.default`, `attrs.<name>.min`, `attrs.<name>.max`,
  `attrs.<name>.enum`, `attrs.<name>.description`
- `aliases` (additive only — cannot remove shipped aliases)
- `version`

**What you cannot change** (load-time error with the offending file
path):

- `id`
- `category`
- `extends`
- `executor`
- `attrs.<name>.type`

**Example** — bump the `review` kind's default `max_iterations` from 3
to 5:

```yaml
# <DataDir>/agent_graph/nodes/review.yaml
id: review
extends: compute
defaults:
  max_iterations: 5
```

After the override loads, `Catalog.Get("review").Provenance` reports
`"max_iterations": "user-override"` and the frontend "show
inheritance" tooltip surfaces "5 (user-override; shipped: 3)".

The frontend's NodesView debug panel calls `Nodes_Doctor` to render a
one-shot summary of catalog health (shipped count, user override
count, last reload time, per-file parse errors). Use it to debug
override-not-applied issues without grep'ing the structured log.

## Hot reload (dev flag)

The chassis-level `--enable-manifest-hot-reload` flag arms a polling
watcher (`core/agentgraph/nodes/hot_reload.go`) that re-reads
`<DataDir>/agent_graph/nodes/` every 2 seconds and atomically swaps
the live catalog when a content change is detected.

Polling cadence is fixed at `DefaultHotReloadInterval = 2 * time.Second`.
Tests can override it via `WatcherConfig.Interval`.

**Gotchas:**

- Hot reload is **dev-only**. It does not run when the chassis boots
  without the flag. A production install runs with a stable manifest
  set; an in-flight run holding a stale catalog will not see the new
  defaults until the next graph load.
- The watcher only picks up changes under `<DataDir>/agent_graph/nodes/`.
  Edits to the shipped manifests at `core/agentgraph/nodes/manifests/*.yaml`
  require a rebuild — the embedded FS is baked at compile time.
- A malformed YAML on disk does NOT crash the catalog; the watcher
  logs an error and keeps the prior catalog live until the next valid
  edit.
- The same effect is reachable on demand via the `Nodes_ReloadOverrides`
  RPC — the WP06 Settings page exposes a "Reload node catalog"
  doctor button.

## Codegen flow

```
$ go generate ./core/agentgraph/...
```

Drives `core/agentgraph/nodes/cmd/gen/main.go`, which:

1. Loads every shipped manifest (no user overrides).
2. Resolves every `extends:` chain.
3. Emits `core/agentgraph/attrs_gen.go` from `attrs.tmpl`.
4. Emits `core/agentgraph/wire_gen.go` from `wire.tmpl`.

The output is **byte-equal idempotent**: running the command twice
produces no diff. CI enforces this via:

```
$ scripts/ci/check-codegen.sh
```

which runs `go generate ./...` and `git diff --exit-code` over
`core/agentgraph/*_gen.go`. A drifted generated file fails the gate
with a guided fix message.

**Adding a new kind** is a single-yaml change plus the generated diff:

1. Author `core/agentgraph/nodes/manifests/<new_id>.yaml`.
2. Add the executor function in the appropriate `exec_*.go`.
3. Register it in the `init()` of the same file.
4. Run `go generate ./core/agentgraph/...`.
5. Commit the manifest + the regenerated `*_gen.go` files together.

## Cross-references

- Parent mission: `kitty-specs/agent-kernel-graph-01KQ6391/spec.md`
  (full kernel + run-control spec).
- Migration guide for legacy kind names:
  [`docs/migration-from-old-kind-names.md`](./migration-from-old-kind-names.md).
- Spec source: `kitty-specs/agent-kernel-graph-node-catalog-01KQ7JDZ/spec.md`.
