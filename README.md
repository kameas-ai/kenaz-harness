<h1 align="center">Kenaz Harness</h1>

<p align="center">
  <strong>Local-first desktop AI agent harness with explicit graph topology.</strong><br />
  Run agents on your machine. Bring your own models. Your data stays put.
</p>

<p align="center">
  <a href="#install">Install</a> ·
  <a href="https://github.com/kameas-ai/kenaz-harness/releases/latest">Download</a> ·
  <a href="https://github.com/kameas-ai/kenaz-harness/discussions">Discussions</a> ·
  <a href="docs/agent-kernel-graph-node-catalog.md">Node Catalog</a> ·
  <a href="CONTRIBUTING.md">Contributing</a>
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License: Apache 2.0" /></a>
  <img src="https://img.shields.io/badge/Go-1.26+-00ADD8.svg" alt="Go 1.26+" />
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

## Install

Download the installer for your platform from the
[latest release](https://github.com/kameas-ai/kenaz-harness/releases/latest).
Every file is named `kenaz-harness-<version>-<platform>.<ext>` and has a
matching `.sha256` file next to it.

| Platform | File | Signed | Notes |
|---|---|---|---|
| macOS, Apple silicon | `…-darwin-arm64.dmg` | Developer ID signed and notarized | Recommended Mac build. The on-device ML engine is available only here. |
| macOS, Intel | `…-darwin-amd64.dmg` | Developer ID signed and notarized | Works without the on-device ML engine. |
| Windows 10/11, x64 | `…-windows-amd64-setup.exe` | Microsoft Trusted Signing | Installs the WebView2 runtime if it is missing. |
| Windows on Arm | `…-windows-arm64.zip` | The `.exe` inside is signed | No installer: unzip and run `kenaz-harness.exe`. |
| Linux, x64 | `…-linux-amd64.AppImage` | **Not signed** | Also as `…-linux-amd64.tar.gz`. Needs WebKitGTK 4.1 (`libwebkit2gtk-4.1`). |
| Linux, Arm64 | `…-linux-arm64.tar.gz` | **Not signed** | No AppImage. Needs WebKitGTK 4.1. |

On macOS or Linux you can also run the install script, which downloads the
right file from the latest release, checks it against its `.sha256`, and
installs it (`/Applications` on macOS, `~/.local/bin` on Linux):

```bash
curl -fsSL https://raw.githubusercontent.com/kameas-ai/kenaz-harness/main/scripts/install.sh | sh
```

Set `KENAZ_HARNESS_VERSION=v0.106.3` (any released tag) to pin a version.
There is no Homebrew cask for the Harness yet: the `kenaz` cask in
`kameas-ai/homebrew-tap` is Kenaz for Claude Code and Codex, a different
product.

### System requirements

- **macOS 12 Monterey or later.** The release is built with Go 1.27, which
  does not run on older macOS.
- **Windows 10 or 11** (64-bit). The installer fetches Microsoft's WebView2
  runtime if it is not already installed.
- **Linux** with WebKitGTK 4.1 (Ubuntu 24.04 ships it as
  `libwebkit2gtk-4.1-0`). Builds are unsigned; check the `.sha256`.
- An API key for at least one model provider (Anthropic, OpenAI, OpenRouter
  or AWS Bedrock), unless you only use local models.

### First run

1. Open **Kenaz Harness**. On first launch it creates its data directory
   (`~/.kenaz/harness/prod/` under your home folder, which holds `data.db`) and walks you through
   adding a model provider.
2. **Add a model key:** **Settings → Providers → Add provider**, pick
   Anthropic / OpenAI / OpenRouter / Bedrock, paste the key and validate it.
   Keys go to your OS keychain, never to disk in plaintext.
3. **New chat:** pick a model and send a message. Type `/help` in the
   composer for the slash commands.
4. The Harness checks the release CDN for updates. Pick the **Stable** or
   **Prerelease** channel, or turn checks off, in **Settings → Updates**.

The Harness works on its own with no account.

### Signing in to Fleet (optional)

**Settings → Account → Sign in to fleet** connects the Harness to your
organization's [Kenaz Fleet](https://kenaz.kameas.ai/) account. Signing in
adds the features your organization has set up: org policy pushed down to
the Harness (it overrides your local defaults), shared team context and
catalog, settings and memory sync across your devices, and role-based
capabilities. Fleet is a paid service; nothing is sent to it until you sign
in.

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

Requires Go 1.26+ (release builds use go1.27.4), Node 20+, and the Wails CLI.

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

A source build behaves like an installed one: first launch creates
`<DataDir>/data.db` (the unified SQLite store) and prompts you to add a
provider.

## Quick start

1. Launch the app (installed, or `wails dev` / the built binary).
2. **Settings → Providers → Add provider**: pick Anthropic / OpenAI /
   OpenRouter / Bedrock, paste a key, validate.
3. **New chat**: pick a model, type a message.
4. Try `/help` in the composer for the slash-command catalog.
5. Drop a file into the composer to attach it as multimodal context.
6. **Tools → Enable filesystem MCP** if you want the agent to read your
   working directory.

### Policy defaults

Every tool call is checked against Cedar policy before it runs. What happens
when no policy you wrote decides a call depends on where the Harness runs:

- **On your computer (starter policy, the default for new installs).** The
  agent reads files, and edits files inside its workspace, without asking.
  Shell commands, the built-in web fetch and search tools, and writes
  outside the workspace ask you first. Harness state, credential folders and
  system paths always ask. An install that predates the starter policy keeps
  its earlier behaviour (every file read and write asks; web requests run
  without asking) until you accept the new default. The first launch says
  which default is active; change it in **Settings → Permissions**.
- **Inside a Kenaz workbench.** The virtual machine is the boundary. Shell
  commands are on unless your organization's Fleet policy turns them off.

A policy you add on the **Policy** page, or your organization's Fleet
policy, overrides either default. MCP servers you install make their own
network calls; their tools follow the per-tool permissions you set.

## Subsystem docs

The deep docs live under [`docs/`](docs/):

- [`agent-kernel-graph-node-catalog.md`](docs/agent-kernel-graph-node-catalog.md) — every node-kind, manifest schema, inheritance chains, codegen flow
- [`migration-from-old-kind-names.md`](docs/migration-from-old-kind-names.md) — alias guide for the kind renames (`llm`→`model`, `plan`→`planner`, `branch`/`fork` swap)
- [`adr/`](docs/adr/) — architecture decision records (Cedar policy carve-out, etc.)

## Tech stack

| Layer | Tech |
|---|---|
| Backend | Go 1.26+, stdlib + `gopkg.in/yaml.v3`, `modernc.org/sqlite` (CGo-free) |
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
- **No outbound traffic** except what you turn on or use: the provider calls
  you authorize (model inference + optional MCP servers you install), the
  built-in web search and fetch tools when the agent uses them, the update
  check against the release CDN (**Settings → Updates**), and Fleet once you
  sign in.
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
