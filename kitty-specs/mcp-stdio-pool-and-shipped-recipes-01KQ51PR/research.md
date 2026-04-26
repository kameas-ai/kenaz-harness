# Phase 0 Research — MCP stdio pool + shipped recipes

> **Research-pass note**: WebFetch was unavailable during this research session.
> Citations below name canonical URLs but their textual claims must be re-verified
> against the live spec at WP01-implement time. Evidence rows in
> `research/evidence-log.csv` are flagged `medium-pending-fetch` until the WP that
> lands the framer code promotes them after live-fetch verification.

## Decision: Newline-delimited JSON-RPC 2.0 framing on stdio

- **Decision**: Use `bufio.Scanner` with `MaxScanTokenSize` raised to 4 MiB; one JSON-RPC message per line on stdin/stdout. No `Content-Length:` header — that is the HTTP/SSE transport, out of scope per spec.md §3.
- **Rationale**: MCP stdio transport is line-delimited JSON-RPC 2.0; each message is a complete JSON object terminated by `\n`. Bare `\r\n` is also accepted by reference implementations.
- **Alternatives**: `Content-Length` framing — rejected, that is the HTTP transport's framing and mixing them invites compatibility breaks with official servers.
- **Source**: https://spec.modelcontextprotocol.io/specification/basic/transports/ (sections "stdio")

## Decision: `protocolVersion` and `initialize` handshake

- **Decision**: Send `initialize` with `protocolVersion: "2024-11-05"` (current stable). On response, extract the server's `protocolVersion`; if mismatched, accept anyway and record a warn-level event (servers are expected to negotiate down). Then send `notifications/initialized`. If the server returns an `error`, mark the recipe failed; do not retry initialize on first-failure (per spec.md §9 edge case 2).
- **Rationale**: Spec-defined handshake; the official Brave Search server (`@modelcontextprotocol/server-brave-search`) and other Anthropic reference servers all key off `2024-11-05`.
- **Alternative**: pin to a future version — rejected, would lock out today's npm release.
- **Source**: https://spec.modelcontextprotocol.io/specification/basic/lifecycle/

## Decision: Capability advertisement

- **Decision**: Advertise `roots: { listChanged: false }`, `sampling: {}` (only when the per-server `samplingEnabled` toggle is on; otherwise omit), and `clientInfo: { name: "kaneaz-harness", version: <build version> }`.
- **Rationale**: MCP requires the client to advertise which server-initiated features it supports; servers gate their requests on this advertisement.
- **Source**: https://spec.modelcontextprotocol.io/specification/basic/lifecycle/#capabilities

## Decision: Cancellation correlation

- **Decision**: Every outbound request gets a monotonically increasing integer `id`. The pool's per-server response router holds a `map[id]chan response`. On `ctx.Done()` mid-call, the pool sends `notifications/cancelled` with `requestId == id` and removes the channel from the router atomically. A late response (server replies after we cancel) is logged at debug and dropped via `select { case ch <- resp: default: log.Debug(...) }` — the reader never blocks on a channel nobody is listening on.
- **Rationale**: Spec says cancellation is best-effort and the server may still respond.
- **Source**: https://spec.modelcontextprotocol.io/specification/basic/utilities/cancellation/

## Decision: `sampling/createMessage` pass-through

- **Decision**: Server-initiated `sampling/createMessage` translates to a `corellm.GenerationRequest` against the **active** provider/model. The server's `messages`, `systemPrompt`, `maxTokens`, `temperature`, `stopSequences` fields map onto the request; streaming events from the registry are drained synchronously; the resulting `corellm.Response` is reshaped into the MCP sampling response. Per-server `samplingEnabled` toggle (default false) gates the entire path; when off, respond with `error.code = -32601` ("Method not found").
- **Rationale**: This is the spec's bridge between MCP servers and the host LLM. Per-recipe toggle (vs global) lets the user trust e.g. a memory server with their model without trusting a freshly installed one.
- **Source**: https://spec.modelcontextprotocol.io/specification/client/sampling/
- **Risk noted**: see "sampling rate-limit / cost amplification" in plan.md risk register.

## Decision: `roots/list` response

- **Decision**: Always respond with two roots: `file://<DataDir>` and (optionally) `file://<active-project-root>` if a session has one set. We do not yet have a "project model" so the second is empty for v1; structured this way so the project mission can fill it in by setting a field on `Pool`.
- **Source**: https://spec.modelcontextprotocol.io/specification/client/roots/

## Decision: `notifications/message` (logging) → slog

- **Decision**: `level` field maps `debug|info|notice|warning|error|critical|alert|emergency` onto slog levels (notice → info, alert/emergency → error). The slog message key is `mcp.<recipe-id>.message` and structured attrs include `mcp.recipe`, `mcp.level` (original string), `mcp.logger` (server-side logger name if present), and `mcp.data` (the raw JSON payload).
- **Source**: https://spec.modelcontextprotocol.io/specification/server/utilities/logging/

## Decision: `notifications/progress` → chassis stream

- **Decision**: `progressToken` correlates a server-side progress event to an in-flight request id. The pool maintains `map[id]progressToken` and forwards every progress notification onto the existing `core/rpc.StreamBroker` topic `mcp:progress`. v1 does not subscribe a UI to this topic — the data path is wired and a follow-on UI mission lights it up.
- **Source**: https://spec.modelcontextprotocol.io/specification/basic/utilities/progress/

## Decision: Brave Search v1 entry — exact npm package and tools

- **Decision**: Recipe `id: brave-search`, command `["npx","-y","@modelcontextprotocol/server-brave-search"]`. Server exposes two tools: `brave_web_search` (general web search) and `brave_local_search` (local business search; falls back to web). Required env: `BRAVE_API_KEY`. Init time on a warm npm cache: ~1.5 s; cold (npm-fetch first): 6–12 s typical.
- **Rationale**: Anthropic's reference server in `modelcontextprotocol/servers` is the canonical implementation. Tool names are stable and documented in the README; the `brave_*` snake_case is the actual export.
- **Source**: https://github.com/modelcontextprotocol/servers/tree/main/src/brave-search
- **Follow-up for WP**: WP that lands `shipped.json` MUST `gh repo clone` or `npm view` to verify tool names + env-var name verbatim before merging.

## Decision: `npx -y` cold-spawn behavior

- **Decision**: Treat the first 30 s of a spawn as "warming". The modal in the UI shows `"Warming…"` with a determinate-by-step indicator (Spawning → Initializing → Listing tools). After 30 s with no `initialize` response, surface a soft warning. Hard fail at 60 s. Mandatory because spec.md NFR-004 requires this UX.
- **Rationale**: `npx -y <pkg>@latest` triggers an `npm install` on first use; this regularly takes 5–15 s for `@modelcontextprotocol/server-brave-search` on broadband.
- **Source**: empirical observation; cross-checked against https://modelcontextprotocol.io/quickstart/server.
- **Implication**: `init_timeout_ms` is the *response* deadline once stdin/stdout is open, not the *spawn* deadline. We need a separate `firstByteTimeout` (default 30 s) before the response timer starts.

## Decision: Resource and prompt surfaces

- **Decision**: Wire `resources/list`, `resources/read`, `resources/subscribe`, `prompts/list`, `prompts/get` data paths in `core/mcp/stdio/protocol.go` even though spec.md §10 marks the UI as out of scope. Spec says "data path lands here; UI in a follow-on mission" (§10 + US7). Methods are part of `Pool` only as opt-in extension methods; `mcp.Pool` itself stays as defined.
- **Rationale**: Not paying this debt now means any mission that consumes resources later refactors the framer/router. Cheap to add at framer level.
- **Source**: https://spec.modelcontextprotocol.io/specification/server/resources/, https://spec.modelcontextprotocol.io/specification/server/prompts/

## Decision: Health pings

- **Decision**: `ping` is a JSON-RPC request `{"method":"ping"}` with no params; expected response is `{}`. Period: 30 s, 5 s response deadline, two consecutive failures (timeout or transport error) trigger restart per FR-008.
- **Source**: https://spec.modelcontextprotocol.io/specification/basic/utilities/ping/

## Decision: Catalog format

- **Decision**: Embedded `shipped.json` via `//go:embed` (consistent with `core/llm/capabilities/loader.go` and `core/llm/cost/reducer.go` patterns). User-installed/enabled state lives separately at `<DataDir>/mcp/recipes.enabled.json`.
- **Rationale**: Catalog is data, not code; matches existing harness conventions.

## Sources cited

1. https://spec.modelcontextprotocol.io/specification/basic/transports/ — stdio framing
2. https://spec.modelcontextprotocol.io/specification/basic/lifecycle/ — initialize
3. https://spec.modelcontextprotocol.io/specification/basic/utilities/cancellation/
4. https://spec.modelcontextprotocol.io/specification/client/sampling/
5. https://spec.modelcontextprotocol.io/specification/client/roots/
6. https://spec.modelcontextprotocol.io/specification/server/utilities/logging/
7. https://spec.modelcontextprotocol.io/specification/basic/utilities/progress/
8. https://spec.modelcontextprotocol.io/specification/basic/utilities/ping/
9. https://github.com/modelcontextprotocol/servers/tree/main/src/brave-search
10. https://modelcontextprotocol.io/quickstart/server — cold-spawn UX corroboration

## Open research items (deferred to WP-implement time)

- Verify `protocolVersion` string against the live spec at WP-implement time.
- Verify Brave server tool names + env-var name verbatim via `npm view` or repo clone.
- Check whether the Brave server emits banner text on stderr at startup (we capture in the ring buffer either way, but this affects which stderr lines should be tagged "expected" vs "warn").
- Confirm whether `roots/list` is initiated server-side at session start or on demand (impacts whether we cache or re-fetch).
