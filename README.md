<h1 align="center">Kenaz Harness</h1>

<p align="center">
  <strong>Local-first desktop AI agent harness with explicit graph topology.</strong><br />
  Run agents on your machine. Bring your own models. Your data stays put.
</p>

<p align="center">
  <a href="https://github.com/kameas-ai/kenaz-harness/discussions">Discussions</a> ·
  <a href="docs/agent-kernel-graph-node-catalog.md">Node Catalog</a> ·
  <a href="CONTRIBUTING.md">Contributing</a>
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License: Apache 2.0" /></a>
  <img src="https://img.shields.io/badge/Go-1.25+-00ADD8.svg" alt="Go 1.25+" />
  <img src="https://img.shields.io/badge/Wails-v2-FF0000.svg" alt="Wails v2" />
  <img src="https://img.shields.io/badge/Vue-3-4FC08D.svg" alt="Vue 3" />
  <img src="https://img.shields.io/badge/Privacy-Local_first-22C55E.svg" alt="Privacy: Local-first" />
</p>

---

Kenaz Harness is a desktop application for running multi-provider AI agents on
your own machine. It speaks to Anthropic, OpenAI, OpenRouter, and AWS Bedrock,
manages long-running tool-using conversations, and gives you an explicit graph
kernel — not an opaque loop — to compose how those agents think.

Everything runs locally. The harness ships as a single Wails binary; data lives
in a SQLite database under your data directory; secrets live in your OS
keychain. Nothing leaves your machine except the model calls you authorize.

> **Status:** pre-1.0. The core graph kernel, MCP tooling, multimodal context,
> branching, memory, and policy subsystems are functional. APIs may still
> change before 1.0.

## What's in the box

- **Agent kernel graph.** 29 explicit node-kinds across 6 archetypes
  (Compute / Control / State / Read / Write / Marker). Single-parent inheritance,
  manifest-as-truth (YAML on disk → Go via `go generate`), validator-driven by
  the resolved schema. See [docs/agent-kernel-graph-node-catalog.md](docs/agent-kernel-graph-node-catalog.md).
- **Multi-provider LLM** with credentials in OS keychain. Anthropic
  (Claude), OpenAI, OpenRouter, AWS Bedrock. Add providers from the GUI; switch
  models per session.
- **MCP tools** with stdio pool, recipe catalog, custom installer, and a
  Cedar-driven policy gate at the tool, network, file-write, and memory-write
  boundaries. Filesystem MCP, local-first web search (DuckDuckGo + Wikipedia +
  go-readability, no API key required), and a sandboxed bash tool ship out of
  the box.
- **First-class memory.** Greedy write hooks fire at every kernel boundary
  (post-LLM, post-tool, on-merge, on-pin). A background prune sweep handles
  selectivity later. Three scopes: global / project / session. Embedding-backed
  retrieval with content-hash dedup.
- **Branching.** Fork a chat into a child session running on a smaller or
  larger model with compacted context as its initial input; merge a summary
  back when the branch is done. The kernel suggests model size based on the
  task and merge timing based on terminal signals; the parent stays
  interactive while branches are alive.
- **Configurable compaction.** Three invocation sites (pre-call, post-tool,
  manual) × four strategies (summary, drop-oldest, semantic-cluster,
  custom-subgraph) × cascading config (global → project → session → run → node).
- **Corpora.** Bulk-ingest directories of files; chunk, embed, and retrieve
  with provenance. Atomic per-file ingestion; token-budget cap on retrieval.
- **Slash commands** in chat: `/help`, `/clear`, `/model`, `/memorize`,
  `/recall`, `/forget`, `/branch`. Routed to typed Go handlers before any LLM
  call.
- **Telemetry.** OpenTelemetry SDK with OTLP HTTP and local-SQLite exporters.
  Spans, metrics, logs. Verbose-attribute toggle gated by privacy.
- **Multimodal.** ContentBlock-based messages with images and PDFs, CAS
  storage at `<DataDir>/media/<sha256>`, attachments visible across global /
  project / session scope.
- **Conversation tree** with copy-on-write storage so forks are cheap and
  storage scales with divergence, not branch count.

## Architecture

```
              ┌────────────────────────────────────────────────────┐
              │             Vue 3 + Vite frontend                  │
              │   chat • graphs • corpora • memory • policy • dials│
              └──────────────┬─────────────────────────────────────┘
                             │  Wails bindings (typed RPC)
              ┌──────────────▼─────────────────────────────────────┐
              │                    core/rpc                         │
              │       view-scoped accessors • Wails surface         │
              └──────────────┬─────────────────────────────────────┘
                             │
   ┌──────────────────────┐  │   ┌─────────────────────────────────┐
   │   core/agentgraph    │  │   │  core/llm   core/mcp            │
   │   • 29 node-kinds    │  │   │   • providers + registry        │
   │   • Kernel + EventLog│  │   │   • stdio pool + recipes        │
   │   • greedy memory    │◄─┼──►│                                 │
   │     hooks            │  │   │  core/memory   core/corpus      │
   │   • compaction       │  │   │   • greedy writes + prune       │
   │   • branching        │  │   │   • vector retrieval            │
   │   • dials + budgets  │  │   │                                 │
   └──────────────────────┘  │   │  core/policy/cedar              │
                             │   │   • Cedar action gates          │
                             │   │                                 │
                             │   │  core/telemetry                 │
                             │   │   • OTel SDK + local sinks      │
                             │   │                                 │
                             │   │  core/session  core/conversation│
                             │   │   • SQLite WAL                  │
                             │   │   • copy-on-write branches      │
                             │   └─────────────────────────────────┘
```

**One process. SQLite WAL.** No CGo (pure Go via `modernc.org/sqlite`).
**Privacy CI invariants** keep credentials, raw chunk text, and PII off the
wire. **Charter directives** keep the import graph acyclic and ban third-party
SDKs from `core/` (configuration interpreters like Cedar carry an explicit
carve-out documented in [`docs/adr/`](docs/adr/)).

## Build from source

Requires Go 1.25+, Node 20+, and the Wails CLI.

```bash
# Wails CLI
go install github.com/wailsapp/wails/v2/cmd/wails@latest

# Clone + run
git clone https://github.com/kameas-ai/kenaz-harness.git
cd kenaz-harness
wails dev          # live-reload dev mode
# or
wails build        # produces a redistributable binary in build/bin/
```

First launch creates `<DataDir>/data.db` (the unified SQLite store) and
prompts you to add a provider. API keys go straight to your OS keychain
(`zalando/go-keyring`); the harness never writes them to disk in plaintext.

## Quick start

1. Launch the app (`wails dev` or the built binary).
2. **Settings → Providers → Add provider**: pick Anthropic / OpenAI /
   OpenRouter / Bedrock, paste a key, validate.
3. **New chat**: pick a model, type a message.
4. Try `/help` in the composer for the slash-command catalog.
5. Drop a file into the composer to attach it as multimodal context.
6. **Tools → Enable filesystem MCP** if you want the agent to read your
   working directory. The Cedar policy gate is default-allow with logging;
   set `DefaultDeny: true` to flip to fail-closed.

## Subsystem docs

The deep docs live under [`docs/`](docs/):

- [`agent-kernel-graph-node-catalog.md`](docs/agent-kernel-graph-node-catalog.md) — every node-kind, manifest schema, inheritance chains, codegen flow
- [`migration-from-old-kind-names.md`](docs/migration-from-old-kind-names.md) — alias guide for the kind renames (`llm`→`model`, `plan`→`planner`, `branch`/`fork` swap)
- [`adr/`](docs/adr/) — architecture decision records (Cedar policy carve-out, etc.)

## Tech stack

| Layer | Tech |
|---|---|
| Backend | Go 1.25+, stdlib + `gopkg.in/yaml.v3`, `modernc.org/sqlite` (CGo-free) |
| Frontend | Vue 3, Vite, Vitest, TypeScript 5 |
| Desktop shell | Wails v2 |
| Storage | SQLite WAL (single `data.db`) + filesystem CAS for media + per-corpus gob vector store |
| Secrets | `zalando/go-keyring` → OS keychain |
| Telemetry | OpenTelemetry SDK + OTLP HTTP + local SQLite exporters |
| Policy | `cedar-policy/cedar-go` (carve-out: configuration interpreter, not a service client) |

## Privacy

- **No telemetry by default.** The OTel SDK ships, but the OTLP exporter is
  off until you point it somewhere. Local SQLite span/metric/log sinks are
  always available for self-hosted introspection.
- **Credentials never touch disk in plaintext.** All API keys live in the OS
  keychain via `zalando/go-keyring`.
- **No outbound traffic** except the provider calls you authorize (model
  inference + optional MCP servers you install).
- **Verbose attributes are off by default** — even in your local telemetry
  sinks, raw prompts / responses / chunk text are gated behind an explicit
  toggle.
- **Memory is yours.** Greedy writes go to your local SQLite; the prune sweep
  is local; the audit panel surfaces every memory write.

## Contributing

Kenaz Harness is early. The best way to engage is
[Discussions](https://github.com/kameas-ai/kenaz-harness/discussions) — ask
questions, share ideas, or propose features.

For code contributions, see [CONTRIBUTING.md](CONTRIBUTING.md). The short
version: open an issue first for anything beyond a typo, keep PRs to one
logical change, all tests must pass, no third-party SDKs in `core/` (charter
C-001).

## License

Apache 2.0 — see [LICENSE](LICENSE).

Third-party assets (Geist fonts, Kenaz design tokens, shadcn-vue primitives)
are credited in [NOTICES](NOTICES).
