# IDE Context Bridge — Design Spike

**Status**: spike / design-only — no implementation  
**Author**: 2026-07-21 · mcp-connector-pack-dev-ide-01NCONN05 WP07  
**Follow-on mission**: `ide-context-bridge-<ULID>` (proposed — see Recommendation below)

---

## What sigil had

The legacy sigil daemon shipped editor plugins for VSCode and IntelliJ/JetBrains that established a
bidirectional JSON-RPC channel between the running editor and the daemon. Through this channel,
sigil could:

- Read the currently open file path and contents.
- Read the active selection (character range and text).
- Read cursor position (line/column).
- List open workspace files.
- Trigger run/build commands and capture stdout/stderr.
- Receive real-time diagnostics (lint errors, type errors) from the language server.

The channel used a local Unix socket (macOS/Linux) or named pipe (Windows) owned by the daemon.
Each editor plugin connected on startup and sent push notifications on state changes.

---

## What the harness needs differently

Sigil was a background daemon with a long-lived process. The harness is a desktop GUI application
(Wails). Key differences that affect the bridge design:

1. **No daemon lifecycle** — the harness may not be running when the editor opens. The bridge
   protocol must tolerate a disconnected harness and reconnect gracefully.
2. **MCP as the tool surface** — the harness exposes tools to Claude via MCP. The IDE context
   should appear as MCP tools/resources on the harness's *server* side, not as a second RPC
   channel alongside MCP.
3. **Multi-editor, single harness** — the harness should serve multiple editors simultaneously
   (VSCode + JetBrains + future editors) without per-editor code in the harness core.
4. **Security boundary** — the editor extensions run with the editor's ambient privileges. The
   harness must not accept arbitrary commands from them; it should only receive read-only context
   pushes (file path, selection, diagnostics) and expose that context upward to Claude.

---

## Protocol options

### Option A: Harness exposes local MCP server (recommended)

The harness binds a local MCP server on a well-known Unix socket or TCP loopback port
(e.g. `~/.kameas/harness.sock` or `127.0.0.1:40900`). Editor extensions connect to this MCP
server as MCP *clients*. They call harness-defined MCP tools to push context:

```
Editor extension (MCP client)
  → harness local MCP server
    → harness receives context update (open file, selection, diagnostics)
    → harness surfaces updated context as MCP resources / tool results
      → Claude (via the harness's own MCP client surface)
```

**Advantages:**
- No new protocol to design — MCP is already the harness lingua franca.
- Editor extensions only need to implement a standard MCP client (SDKs available for both
  VSCode/Node.js and JetBrains/Kotlin).
- Context can be surfaced to Claude as a standard MCP resource
  (`ide://active-file`, `ide://selection`, `ide://diagnostics`) with no harness core changes
  beyond the MCP server binding.
- Transport security: Unix socket is process-local; TCP loopback with a session token in the
  first message satisfies localhost-only requirements.

**Disadvantages:**
- The harness must accept inbound connections, which requires a new listener goroutine and
  lifecycle management (start on launch, stop on quit).
- Requires a local MCP server implementation in Go (not currently in the codebase — this is the
  primary work item).
- Port/socket conflict resolution needed if multiple harness instances run.

### Option B: LSP-style JSON-RPC channel

The harness and extensions communicate over a custom JSON-RPC 2.0 channel (similar to LSP) on a
local socket. This was sigil's approach.

**Advantages:**
- Familiar pattern; well-tested in the ecosystem.
- Lightweight.

**Disadvantages:**
- Requires designing a new protocol on top of JSON-RPC (message types, notification schemas,
  versioning). Duplicates work that MCP already solved.
- Two separate RPC protocols in the harness (MCP outbound + custom JSON-RPC inbound) increases
  cognitive overhead and maintenance surface.
- Not recommended — Option A uses MCP end-to-end which is already the harness standard.

### Option C: Filesystem polling (lowest fidelity, no extension needed)

The harness polls well-known editor state files (VSCode's `.vscode/` workspace state, JetBrains
`.idea/` files) or reads the OS clipboard to detect context.

**Advantages:**
- No editor extension to install.

**Disadvantages:**
- High latency (polling interval), missing real-time diagnostics.
- Fragile — editor internal state file formats are not stable APIs.
- Cannot capture selection or cursor position.
- Not recommended for any production use.

---

## Editor surfaces to cover

### VSCode extension API

VSCode provides a stable Extension API (`vscode` Node.js module) with:

- `vscode.window.activeTextEditor` — active file URI, selection, visible ranges.
- `vscode.languages.getDiagnostics()` — all lint/type errors in the workspace.
- `vscode.workspace.workspaceFolders` — open project roots.
- `vscode.tasks.executeTask()` — run a build/test task programmatically.

A VSCode extension (TypeScript) can use the `@modelcontextprotocol/sdk` Node.js client to
connect to the harness's local MCP server and push context on editor events.

**Extension distribution**: VSCode Marketplace (`.vsix` package). Can be bundled in the harness
installer or published separately with a download link from the harness UI.

### JetBrains plugin API

JetBrains Platform (IntelliJ, GoLand, PyCharm, etc.) provides:

- `FileEditorManager.getInstance(project).selectedEditor` — active file and selection.
- `PsiFile` / `Document` — file contents + AST.
- `DaemonCodeAnalyzer` + `HighlightInfo` — diagnostics (errors, warnings).
- `ExternalTaskExecutor` / `BuildManager` — run configurations.

A JetBrains plugin (Kotlin) uses the Ktor HTTP client (or a pure-Java WebSocket) to connect to
the harness local MCP server.

**Plugin distribution**: JetBrains Marketplace (`.zip` plugin). Requires JetBrains plugin review
(~2–4 weeks). Consider bundling a download link from the harness instead of direct Marketplace
publishing for v1.

---

## Context the bridge exposes as MCP tools/resources

The harness local MCP server would expose the following to Claude (via the harness's own tool
dispatch surface):

| MCP Resource / Tool | Description | Push or pull? |
|---|---|---|
| `resource: ide://active-file` | URI + language + full contents of the currently active file | Push (updated on tab switch) |
| `resource: ide://selection` | Character range + selected text in the active editor | Push (updated on selection change) |
| `resource: ide://diagnostics` | Array of {file, line, col, severity, message} from the language server | Push (updated on diagnostic change) |
| `resource: ide://workspace` | Workspace root(s) and open file list | Push (updated on project open/close) |
| `tool: ide_run_command` | Ask the editor to run a named task (build, test, lint) and return stdout | Pull (Claude calls on demand) |

The context resources are read-only from the harness's perspective — the editor extensions push
updates; the harness stores the latest state and serves it to Claude on request.

`ide_run_command` is the only write/action tool and should be gated behind a Cedar policy
requiring explicit user approval (analogous to the `prompt_on_first_use` pattern used for
destructive filesystem tools).

---

## Implementation effort estimate (per component)

| Component | Effort | Notes |
|---|---|---|
| Harness local MCP server (Go) | L (3–5 days) | New listener goroutine, MCP server protocol, context store, resource/tool handlers, lifecycle management. The most complex piece — requires `core/mcp/local/` new package. |
| VSCode extension (TypeScript) | M (2–3 days) | Boilerplate extension + MCP client + 4–5 editor event hooks. Node.js MCP SDK handles the protocol. |
| JetBrains plugin (Kotlin) | M (2–3 days) | Plugin scaffold + Ktor/WebSocket client + 4–5 IntelliJ API event hooks. No MCP Kotlin SDK yet — would need to implement the handshake manually or wrap the Java SDK. |
| Harness UI (install instructions, extension download links) | S (0.5 days) | Settings pane entry pointing at extension download; no new UI surface needed. |
| Security review + Cedar policy | S (0.5 days) | `ide_run_command` Cedar gate; connection token validation for local MCP socket. |
| **Total** | **~H (8–12 days)** | Two engineers recommended for parallel editor work. |

---

## Recommendation

**Proceed with Option A (harness local MCP server).** It is the only option that:

1. Reuses MCP end-to-end (no new protocol).
2. Delivers real-time, low-latency context (no polling).
3. Scales to future editors without harness core changes (each new editor needs only a new
   extension, not a harness protocol update).

**Go/no-go**: GO — the effort is well-bounded (8–12 days), the editor APIs are stable, and
the MCP Node.js SDK makes the VSCode extension straightforward. The JetBrains plugin is the
riskiest piece (no Kotlin MCP SDK; Marketplace review latency) and can be deferred to v2 if
needed.

**Deferred to this spike**: JetBrains Marketplace publishing timeline. Ship VSCode extension
first; JetBrains plugin follows in a subsequent minor.

---

## Follow-on mission

**Proposed name**: `ide-context-bridge-<ULID>`  
**Scope**: Implement Option A — harness local MCP server + VSCode extension + JetBrains plugin.  
**Estimated size**: L mission (2 engineers, ~2 sprints).  
**Prerequisites**:
- This spike document reviewed and approved by alecfeeman.
- Harness version >= current (MCP infrastructure in place — shipped).
- VSCode Extension API version target pinned (recommend: `^1.85.0`).
- JetBrains Platform SDK version target pinned (recommend: `2024.1+`).

**Proposed WP breakdown**:
- WP01: `core/mcp/local/` — harness local MCP server skeleton (listener, session, context store).
- WP02: `core/mcp/local/handlers.go` — resource handlers for active-file, selection, diagnostics, workspace.
- WP03: `core/mcp/local/tools.go` — `ide_run_command` tool + Cedar gate.
- WP04: VSCode extension (`extensions/vscode/`) — scaffold, MCP client, editor event hooks.
- WP05: JetBrains plugin (`extensions/jetbrains/`) — scaffold, MCP client, IntelliJ event hooks.
- WP06: Harness UI — settings entry, extension download links, connection status indicator.
- WP07: Integration tests + security review.
