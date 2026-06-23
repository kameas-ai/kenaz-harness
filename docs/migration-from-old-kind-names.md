# Migrating from old node-kind names

The `agent-kernel-graph-node-catalog` mission renamed four node kinds
to align names with archetypes. Old graphs continue to load through an
alias map; this guide lists the renames, the sunset window, and how to
find graphs in your DataDir that still reference the old names.

## The four renames

| Old kind | New kind | Notes |
|---|---|---|
| `llm` | `model` | Compute archetype member; identical attrs. |
| `plan` | `planner` | Compute archetype member; identical attrs (`verbosity`, `planner_model`, `threshold_input`). |
| `branch` (predicate router) | `decision` | Control archetype member. **The name `branch` is reused** for the new sub-graph spawn kind below — alias resolution is a one-shot rewrite at graph-load time. |
| `fork` | `branch` (sub-graph spawn) | Control archetype member. The conceptually load-bearing user gesture "spawn a child branch" is now naturally named `branch`. |

### The `branch` double-meaning callout

`branch` is BOTH a current canonical kind (sub-graph spawn) AND a
deprecated alias for `decision` (the v0 predicate router). The alias
resolver always honours canonical kinds first: a graph YAML naming
`kind: branch` runs the sub-graph-spawn executor; only when the input
fails canonical lookup does the alias map kick in.

If you have a **v0 predicate-router graph** using `kind: branch`,
the alias resolver rewrites it to `kind: decision` at load time — the
conditional-routing semantics are preserved.

If you have a **v1 sub-graph-spawn graph** using `kind: branch`, the
canonical interpretation wins and the executor runs the spawn flow.

The runtime never confuses the two — `lookupAlias` short-circuits when
the input matches a live canonical kind.

## Sample diff: legacy → canonical YAML

Before:

```yaml
# my-old-graph.yaml
spec_version: "1"
id: legacy_workflow
entrypoints: [start]
nodes:
  - id: start
    kind: llm                      # ← alias for `model`
    attrs:
      model: claude-sonnet
  - id: choose
    kind: branch                   # ← alias for `decision` (predicate router)
    attrs:
      condition: "ok"
      next_true:  go_ahead
      next_false: stop
  - id: spawn
    kind: fork                     # ← alias for `branch` (sub-graph spawn)
    attrs:
      title: child_run
  - id: planning
    kind: plan                     # ← alias for `planner`
    attrs:
      verbosity: terse
```

After:

```yaml
# my-old-graph.yaml — migrated
spec_version: "1"
id: legacy_workflow
entrypoints: [start]
nodes:
  - id: start
    kind: model
    attrs:
      model: claude-sonnet
  - id: choose
    kind: decision
    attrs:
      condition: "ok"
      next_true:  go_ahead
      next_false: stop
  - id: spawn
    kind: branch                   # canonical sub-graph-spawn name
    attrs:
      title: child_run
  - id: planning
    kind: planner
    attrs:
      verbosity: terse
```

## Sunset window

The aliases ship in v1 of this mission. Removal is announced in
deprecation warnings every time the loader resolves an alias:

```
WARN agentgraph: deprecated node kind alias resolved
     old=llm new=model removal=next-minor
```

`removal=next-minor` corresponds to the package constant
`agentgraph.AliasSunsetVersion`. After the next minor release the
aliases will not resolve; the loader rejects unknown kinds with a
clear error pointing at the offending node ID.

The follow-up mission `agent-kernel-graph-alias-sunset` (TBD ID) will
flip the constant and remove the alias entries from the manifest set.
Until then, the alias surface is stable.

## Audit log surface

When the kernel runs a graph that contained one or more alias
resolutions, it emits one `kind_alias_resolved` event per (old → new)
pair at run-start. The event payload carries the alias tuple and the
graph id:

```json
{
  "old": "llm",
  "new": "model",
  "removal_in": "next-minor",
  "graph_id": "legacy_workflow"
}
```

These events appear in the run trace and in the frontend's audit
panel. Run a few of your existing graphs and check the audit panel for
`kind_alias_resolved` events — every entry is one node you should
migrate.

## Finding aliased graphs in your DataDir

The `<DataDir>/agent_graph/library/` directory holds your saved user
graphs. A simple `grep` finds every legacy reference:

```sh
grep -lE 'kind: (llm|plan|branch|fork)$' <DataDir>/agent_graph/library/*.yaml
```

(On macOS substitute `<DataDir>` with
`~/Library/Application Support/kenaz-harness/`. On Linux it lives at
`~/.local/share/kenaz-harness/`. On Windows,
`%APPDATA%\kenaz-harness\`.)

The `branch` match is intentional — `grep` cannot disambiguate the
predicate-router (alias) usage from the sub-graph-spawn (canonical)
usage. Open each match and inspect the surrounding `attrs:` block:

- `condition:` + `next_true:` + `next_false:` → it's the v0 predicate
  router; rename to `decision`.
- `title:` + `parent_leaf:` (or `model_override:`) → it's the v1
  sub-graph spawn; the canonical name is already `branch` and no
  rewrite is needed.

## What does NOT change

- `tool`, `transform`, `activity`, `reflect`, `review`, `ask`,
  `escalate`, `parallel`, `join`, `merge`, `loop`, `retry`,
  `memory`, `corpus_read`, `corpus_write`, `attachment`,
  `history_read`, `trace_write`, `checkpoint` — same names, same
  attrs, no alias resolution.
- `compact`, `approval`, `artifact`, `read_file`, `read_bash_output`,
  `write_file` — these are net-new in this mission. No legacy graph
  referenced them.
- The Graph YAML schema (`spec_version: "1"`, `entrypoints`, `nodes`,
  `edges`, `budget`) is unchanged. Only the per-node `kind:` discriminator
  for the four renamed kinds was retired.

## Cross-references

- Catalog overview: [`docs/agent-kernel-graph-node-catalog.md`](./agent-kernel-graph-node-catalog.md)
- Spec source: `kitty-specs/agent-kernel-graph-node-catalog-01KQ7JDZ/spec.md`
- Alias resolver: `core/agentgraph/aliases.go`
- Per-process warning state: tracked in `aliasWarned` (one slog.WARN
  per alias per process; observers via `RegisterAliasObserver`).
