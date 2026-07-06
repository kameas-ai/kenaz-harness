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
import. The chassis ships with `cedar.AllowAll{}` as the boot-stage
default; production wiring swaps in a real `Engine` once a policy
bundle has loaded.

| Site                                             | Helper                  | Wired in              |
| ------------------------------------------------ | ----------------------- | --------------------- |
| LLM model selection (registry pipeline)          | `cedar.LLMPolicyGuard`  | `core/rpc/api.go` (newLLMStack) |
| Tool dispatch (MCP + built-in)                   | `cedar.CheckTool`       | toolloop's permission resolver (perms layer) |
| Memory writes                                    | `cedar.CheckMemoryWrite` | `core/rpc/api.go` (`memoryGateAdapter` → `memory.Store.SetGate`) |
| Network requests (websearch fetch)               | `cedar.CheckNetwork`    | `core/tools/websearch.Fetcher` (`PolicyGate` opt) |
| Filesystem reads/writes (kernel state nodes)     | `cedar.CheckFile{Read,Write}` | `agentgraph.PolicyGate` (`env_deps_policy.go`) |
| Finer-grained state actions                      | `cedar.CheckState{Read,Write}` | `agentgraph.PolicyGate` |

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
