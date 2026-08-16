# Production wiring of the agent-graph kernel

This doc maps every `core/agentgraph` seam onto the production manager
that satisfies it. The kernel's `applyEnvDefaults` installs nil-stubs
when an `Env` field is unset; in production those nil-stubs are
**fallback only** — the chassis (`core/rpc/api.go`) builds adapters
from each manager and threads them into `Env` via the agent-graph
view's `WithEnvDeps` option.

## The seams

The seams live in `core/agentgraph/seams.go` and `branch_seam.go`.
Each is a narrow interface; the kernel never imports the manager
directly.

| Seam               | Interface          | Production wrapper (in `core/rpc/views/agentgraph/`) | Production manager (chassis) | Nil fallback                    |
| ------------------ | ------------------ | ---------------------------------------------------- | ---------------------------- | ------------------------------- |
| `BranchSeam`       | Fork / Merge       | `BranchSeamAdapter` (`env_deps_branch.go`)           | `core/conversation.Manager`  | `nilBranchSeam` ⇒ `ErrNoBranchSeam` |
| `CorpusBackend`    | `Reader + Writer`  | `CorpusBackendAdapter` (`env_deps_corpus.go`)        | `core/corpus.Manager`        | `nilCorpus` ⇒ `ErrNoCorpus`     |
| `MemoryStore`      | `Read` / `Write`   | `MemoryStoreAdapter` (`env_deps_memory.go`)          | `core/memory.Store` + `Embedder` | `nilMemory` ⇒ `ErrNoMemory`     |
| `BashOutputStore`  | `Get(runID)`       | `BashOutputStoreAdapter` (`env_deps_bash.go`)        | `core/tools/bash.Store`      | `nilBashOutput` ⇒ `ErrNoBashOutput` |
| `PolicyGate`       | File / state checks| `PolicyGateAdapter` (`env_deps_policy.go`)           | `core/policy/cedar.Gate`     | `allowAllPolicy` ⇒ every check passes |

## Construction site

`core/rpc/api.go` constructs every manager up-front, then passes them
into `newGraphManagerWithDeps`:

```go
a.graphMgr = newGraphManagerWithDeps(c, a.convMgr, a.corpusMgr, memStore, embedder, a_bashStore)
```

`newGraphManagerWithDeps` builds the adapters and threads them through
the manager's option:

```go
deps := graphview.EnvDeps{
    Branch:     graphview.NewBranchSeamAdapter(convMgr, sessionManagerOrNil(c)),
    Corpus:     graphview.NewCorpusBackendAdapter(corpusMgr),
    Memory:     graphview.NewMemoryStoreAdapter(memStore, embedder),
    BashOutput: graphview.NewBashOutputStoreAdapter(bashStore),
    Policy:     graphview.NewPolicyGateAdapter(cedar.AllowAll{}),
}
mgr, _ := graphview.NewManager(graphview.WithDataDir(dataDir), graphview.WithEnvDeps(deps))
```

The view's `Manager.startRun` copies `EnvDeps` onto every per-run
`Env` before spawning the kernel goroutine. `applyEnvDefaults` runs
**after** that copy and only fills in seams the chassis didn't wire,
so test harnesses (which leave `EnvDeps` empty) still work.

## Built-in tool wiring

The toolloop discovers MCP-provided tools from a stdio pool. The
in-binary tools (`core/tools/websearch`, `core/tools/bash`) live next
to the chassis; they reach the LLM through:

1. **`core/toolloop.BuiltinRegistry`** — process-local map of
   `name → BuiltinTool` (websearch, bash). Populated at chassis boot
   in `core/rpc/builtins_wiring.go`.

2. **`core/toolloop.EnabledFilter`** — wraps the registry with the
   `Settings.WebSearchEnabled` / `Settings.BashEnabled` predicate so a
   user-side toggle takes effect on the next chat turn.

3. **`core/toolloop.BuiltinPool`** — wraps the MCP pool. Built-in
   tools surface via `Tools()` under the synthetic server prefix
   `"kenaz"`; `Call(server="kenaz", tool="bash", …)` dispatches to
   the in-binary tool without going through MCP.

4. **`core/rpc/views/llm.NewMCPToolDiscovererWithBuiltins`** —
   constructs the LLM-side `ToolDiscoverer` that publishes both MCP
   tools AND built-in tools onto every `GenerationRequest.Tools`.

Net effect: when the user toggles "Web search" ON in Settings, the
LLM's tool catalog gains a `kenaz__web_search` entry on the next
turn, and a tool-call against that name dispatches to
`core/tools/websearch.Tool.Call` directly.

## Cedar gate-hook fire sites

`core/policy/cedar.Gate` is the small interface gate-hook callers
import. There is **no deferred swap-in path**: whatever gate a wiring
site passes at construction is what enforces for the process lifetime.
Every production site therefore passes `buildCedarGate(c.DataDir())`
(or the `*cedar.Engine` itself) directly. `cedar.AllowAll{}` appears
only where there is no `DataDir` to load policy from — the nil-core
test chassis — and `buildCedarGate` returns it for an empty `dataDir`
so the fallback is one branch in one function rather than a literal at
each call.

Three sites used to pass `cedar.AllowAll{}` unconditionally under
comments promising a future swap that was never written, and two view
`Config`s omitted their `Cedar` field entirely (nil gate ⇒
`Allow("no engine wired (default-allow)")`). A user could author a
Cedar policy, watch the editor validate it, save it, see it listed as
loaded — and none of these five sites consulted it. Fixed 2026-08-16;
see `docs/dead-code-audit-2026-08-16.md` findings A1 and A2.

**Posture on construction failure is fail-OPEN, deliberately.**
`buildCedarGate` logs a warning and returns `AllowAll` when
`cedar.NewEngine` errors, and `Engine.Reload` keeps the prior
`PolicySet` when a file fails to parse, so a typo in a user policy
cannot brick the app. `Options.DefaultDeny` is `false` for the same
reason — an unmatched action is `NotApplicable`, which every gate hook
treats as allow. Flipping that default is a product decision, not a
wiring one.

| Site                                             | Helper                  | Wired in              |
| ------------------------------------------------ | ----------------------- | --------------------- |
| LLM model selection (registry pipeline)          | `cedar.LLMPolicyGuard`  | `core/rpc/api.go` (`newLLMStack`, `llmregistry.Options.Policy` — the registry has no setter, so this is the only door) |
| Tool dispatch (MCP + built-in)                   | `cedar.CheckTool`       | toolloop's permission resolver (perms layer) |
| Memory writes                                    | `cedar.CheckMemoryWrite` | `core/rpc/api.go` (`memoryGateAdapter` → `memory.Store.SetGate`; the only non-test `SetGate` call) |
| Network requests (websearch page fetch)          | `cedar.CheckNetwork`    | `core/tools/websearch.Fetcher` (`PolicyGate` opt) |
| Network requests (websearch **query**)           | `cedar.CheckNetwork`    | `core/tools/websearch.{DuckDuckGo,Wikipedia}Backend` (`With*Gate` opt). Gating only the Fetcher left the leg carrying the user's search terms off-box ungated |
| Network requests (`kenaz__web_fetch`)            | `cedar.CheckNetwork`    | `core/tools/webfetch` (`Options.Gate`) |
| Workflow run / save / delete                     | `cedar.GateWorkflow*`   | `core/rpc/api.go` → `workflowsview.Config.Cedar` |
| Scheduled chat create / delete / execute         | `cedar.GateScheduledChat*` | `core/rpc/api.go` → `scheduledchatview.Config.Cedar` |
| Filesystem reads/writes (kernel state nodes)     | `cedar.CheckFile{Read,Write}` | `agentgraph.PolicyGate` (`env_deps_policy.go`) |
| Finer-grained state actions                      | `cedar.CheckState{Read,Write}` | `agentgraph.PolicyGate` |

`scripts/ci/check-cedar-gate-arguments.sh` is the gate that keeps this
table honest: it fails when a `cedar.Gate`-typed constructor argument
or view `Config` field is passed `AllowAll` unconditionally, or omitted,
at a production wiring site.

### The `mode` context attribute

`default_workflows_policy.cedar` branches on a `mode` context attr
(`"permissive"` | `"strict"`). Its producer is
`workflowsview.Config.CedarModeFn`, wired in `core/rpc/api.go` to the
`cedarStrictWorkflowMode` settings field and read live on every
run/save. Before 2026-08-16 there was no producer at all, so the
bundle's strict arm — which forbids saving a shell-bearing workflow —
shipped embedded in every engine and could never fire. There is still
no Settings-panel dial for it; that is tracked in
`docs/unwired-ledger.md`.

## Memory hook journal

The greedy memory hook journal (FR-058) was previously in-memory
only. Production wiring now binds a SQL writer (migration 0308 ships
the `memory_hook_journal` table):

```go
hm.SetJournalWriter(agentgraph.NewSQLJournalWriter(db))
```

The writer is fire-and-forget: a write failure does not block the
kernel; the in-memory journal still records the fire and the EventLog
still emits the audit event.

## Compaction subsystem on-disk YAML resolver

`core/agentgraph/compaction.YAMLResolver` is the disk-backed cascading
config resolver. It composes a `MemoryResolver` (in-process) with a
YAML file at `<DataDir>/config/compaction.yaml`. The file shape
mirrors the cascade layers:

```yaml
global:
  sites:
    pre_call:
      enabled: true
      pre_call_threshold: 0.85
projects:
  proj-X:
    sites:
      pre_call:
        enabled: false
```

`Set(layer, scopeID, cfg)` mutates the in-memory layer AND flushes
back to disk. Read errors at construction are non-fatal — the
resolver falls back to `SafeDefaults`.

## Decision tree: do I need a real seam in my test?

When writing a graph test that exercises a State / Compute / Control
node, ask:

| Node kind                           | Need a real seam?                               |
| ----------------------------------- | ----------------------------------------------- |
| `read_file` / `write_file`          | No — the kernel's `os` calls work in-process; the policy gate falls through to AllowAll |
| `read_bash_output`                  | Yes — without a `BashOutputStore` the executor returns `ErrNoBashOutput`; pass `corebash.NewStore()` and pre-Put a record |
| `corpus_read` / `corpus_write`      | Yes — without a `CorpusBackend` the executor returns `ErrNoCorpus`; pass `corecorpus.NewManager(...)` or a stub |
| `memory_write` (greedy hook)        | Yes for write-asserting tests — the kernel's `nilMemory` errors; pass a real `memory.Store` or `agentgraph.MemoryStoreFunc` |
| `branch` / `merge`                  | Yes — without a `BranchSeam` both nodes return `ErrNoBranchSeam`; use `agentgraph.NewFakeBranchSeam()` for unit tests, the real `BranchSeamAdapter` for integration |
| Tool dispatch (`tool` node)         | Use a `ToolRegistry` stub. The production tool path (toolloop) is exercised through the LLM stack tests, not the agent-graph tests |

In every case, leaving an `Env` field nil falls through to the
agent-graph nil-stub which surfaces a deterministic `ErrNo*` sentinel.
That makes "I forgot to wire X" a fast, obvious failure rather than a
silent no-op.
