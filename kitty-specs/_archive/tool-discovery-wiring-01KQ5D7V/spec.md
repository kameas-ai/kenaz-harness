# Spec: Tool discovery wiring (LLM request → tools array)

**Mission ID**: `tool-discovery-wiring-01KQ5D7V`
**Status**: in-flight (single-WP, dispatched as `tool-discovery-wiring` worktree)
**Owner**: alecfeeman
**Planning base**: `main`
**Merge target**: `main`

## 1. Why this mission

The harness has a complete tool-execution pipeline (`core/toolloop` with permissions, hooks+audit, parallel dispatch, cancellation, confirm-each modal) and an MCP pool surface that exposes tools via `mcp.Pool.Tools(ctx)`. But `core/rpc/views/llm/impl.go buildRequest` constructs the `corellm.GenerationRequest` **without populating the `Tools []ToolSpec` field**. Result: every model request goes out without any tool schemas. The model never knows tools exist, never emits `tool_use`, the toolloop never fires.

The mcp-tool-execution mission and the mcp-stdio-pool mission both implicitly assumed this wiring was present. It isn't. This mission closes the gap.

## 2. Goals

- Project pool tools into `corellm.ToolSpec` shape and pass them in `GenerationRequest.Tools`.
- Filter by per-session permission policy: if `perms.Resolve(...)` returns `Policy=deny` for `(server, tool)`, that tool is omitted from the request entirely (the model can't ask for what it doesn't know about).
- Namespace tool names: `<server>__<tool>` (double underscore). The toolloop un-namespaces on dispatch so `pool.Call(server, tool, args)` sees the correct components.
- Integrate cleanly with all three existing provider adapters (Anthropic, OpenAI, AWS Bedrock) via the existing `ToolSpec` serialization at `core/llm/llm.go:217`.

## 3. Non-goals

- Per-session tool *availability* selection (UI to enable/disable specific tools per session). Today: pool is harness-global; perms `deny` rules are the only per-session subtractive filter. Per-session enable lists are a follow-up.
- Provider-specific quirks beyond what the existing `ToolSpec` shape already handles (e.g., Anthropic-specific tool_choice parameters).
- Frontend surface for tool discovery — the model uses tools transparently; the user doesn't see a list.

## 4. Functional requirements

- **FR-001** New `core/llm.ToolDiscoverer` interface: `Tools(ctx, sessionID) ([]ToolSpec, error)`. Defined in `core/llm/` to avoid import cycles (`core/llm` cannot depend on `core/toolloop` since `core/toolloop` depends on `core/llm`).
- **FR-002** New `core/rpc/views/llm/discoverer_adapter.go` providing `mcpToolDiscoverer` that wraps `mcp.Pool` + `toolloop.PermissionResolver`. Exposed via `NewMCPToolDiscoverer(pool, perms) corellm.ToolDiscoverer`.
- **FR-003** `(*mcpToolDiscoverer).Tools(ctx, sessionID)`:
  1. Calls `pool.Tools(ctx)`.
  2. For each tool, calls `perms.Resolve(ctx, sessionID, t.Server, t.Name)`. Drops tools with `Policy == PolicyDeny`.
  3. Projects to `ToolSpec{Name: t.Server + "__" + t.Name, Description: t.Description, InputSchema: t.InputSchema}`.
  4. Returns. Pool error → propagate. Perms error on a single tool → log warn, include the tool (fail-open on resolution errors so a transient registry hiccup doesn't make all tools vanish).
- **FR-004** `core/rpc/views/llm/impl.go` Config gains `Tools corellm.ToolDiscoverer` field. `buildRequest` populates `req.Tools = a.tools.Tools(ctx, sessionID)` when non-nil; on error logs at warn and degrades to no-tools (does not block the request).
- **FR-005** Toolloop un-namespacing: `core/toolloop/loop.go` (or `concurrent.go`) where it reads `tool_use.Name` from the model's response. If the name contains `__`, split on the first occurrence: server = prefix, tool = suffix. If no separator, fall back to the WP01 pattern (server="" or use the `Server` field on the call envelope if the model's API provides it). The fixture pool's tests must continue to work.
- **FR-006** `core/rpc/api.go newLLMStack` constructs `mcpToolDiscoverer` with the same pool + perms it already wires for the toolloop and passes it to `llm.New(Config{... Tools: discoverer})`.

## 5. Non-functional requirements

- **NFR-001** No new tests degrade the baseline. `go test -race -count=1 -short ./core/...` ≥ 966 (current baseline post recent merges) + new tests.
- **NFR-002** No frontend changes. `cd frontend && npm test -- --run` and `npm run build` unchanged.
- **NFR-003** Discoverer call from `buildRequest` is bounded by the existing request `ctx` — a slow `pool.Tools()` cannot block the chat indefinitely.

## 6. Architecture

```
core/llm/
└── discoverer.go             # NEW: ToolDiscoverer interface + NoopDiscoverer

core/rpc/views/llm/
├── discoverer_adapter.go     # NEW: mcpToolDiscoverer wrapping mcp.Pool + perms
├── discoverer_adapter_test.go
├── api.go                    # MODIFIED: Config gains Tools field
└── impl.go                   # MODIFIED: buildRequest populates req.Tools

core/toolloop/
├── loop.go OR concurrent.go  # MODIFIED: un-namespace tool_use.Name on dispatch
└── *_test.go                 # extended with namespaced tool_use round-trip

core/rpc/api.go               # MODIFIED: newLLMStack wires the discoverer
```

## 7. Acceptance criteria

- **A1** With the fixture pool's tools registered, `req.Tools` is non-empty in the `GenerationRequest` sent to provider adapters. Verified by an `impl_test.go` extension that captures the request via a stub `corellm.Registry`.
- **A2** A perms rule denying `(server="filesystem", tool="*")` removes every filesystem-server tool from `req.Tools` for that session, but leaves them in for a different session that doesn't have the deny.
- **A3** Model emits `tool_use` with namespaced name `"brave-search__brave_web_search"`; toolloop un-namespaces to `pool.Call(ctx, "brave-search", "brave_web_search", args)`.
- **A4** Backward-compat: existing fixture-pool tests (`core/toolloop/loop_test.go`, `concurrent_test.go`) still pass without modification when their tool names don't contain `__`.
- **A5** Empty pool → `req.Tools` is nil/empty; the request still goes out and the model responds normally.
- **A6** Discoverer error (pool returns error) → warn-logged, `req.Tools` stays nil, request proceeds without tools (degrade gracefully).

## 8. Open questions

1. **Namespacing separator**: `__` (double underscore) chosen because it's allowed in all provider tool-name regexes (`[a-zA-Z0-9_-]+`). Alternative: `:` — cleaner visually but Anthropic's tool name regex rejects it. **Decision**: `__`.
2. **Perms-error policy**: fail-open per FR-003 step 4 — log + include the tool — so a transient resolver hiccup doesn't black-hole the whole tool surface. Worth revisiting if abuse vectors emerge.
3. **Per-session enable lists**: out of scope for v1. Today users get the global pool minus per-session denies. A follow-up mission can add positive selection.

## 9. Branch strategy

Single branch `tool-discovery-wiring` off `main`. Merge when acceptance gate passes. The implementation is in flight as of mission creation (dispatched 2026-04-26 via subagent worktree).
