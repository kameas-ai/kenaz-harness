# Plan — long-turn-resilience-01KR3PRS

> Mission scope: make long-lived chat turns survive transient network
> failures. Today, when an SSE connection to OpenRouter / DeepSeek /
> any provider drops mid-stream (edge proxy idle-timeout, transient
> 5xx, packet loss), the chassis surfaces:
> `loop: node "agent_loop": body assistant_turn: model: node "assistant_turn": chat: stream final: llm: transient provider error: Network connection lost.`
> The turn dies; partial output + tool calls are lost; the user has to
> retype. This mission adds three layers of resilience: **TCP
> keepalive on the HTTP transport**, **classified retry-with-backoff at
> the chat-runner boundary**, and a **"resume" UI affordance** when the
> failure is unrecoverable mid-flight.

## Branch contract

- **Branch**: `feat/long-turn-resilience-01KR3PRS`
- **Base**: `main`
- **Merge gate**: `go test ./... && (cd frontend && pnpm test && pnpm typecheck)`.

## Diagnosis (verified)

The error chain in the bug report unwinds:
1. `core/llm/openrouter/openrouter.go:805` — scanner.Err() returns non-nil after edge dropped the SSE connection.
2. Adapter sets `s.finalErr = &llm.ErrTransient{Message: "openrouter: stream read: " + err.Error()}`.
3. `Stream.Final()` returns the ErrTransient.
4. Chat runner's `stream final` step propagates the error up through the kernel's `assistant_turn` node.
5. Kernel's `agent_loop` node propagates again. The user sees the wrapped chain.

**Why the underlying disconnect happens:**
- `core/llm/openrouter/openrouter.go:121` — `httpc: &http.Client{}`. No custom Transport. Default Transport has wire-level HTTP/2 keepalive but no application TCP-level `SO_KEEPALIVE` ping during long idle stretches.
- DeepSeek's reasoning model can sit silent for minutes between SSE deltas.
- OpenRouter's edge proxy idle-timeouts an SSE that's been silent for ~5 minutes; the TCP gets RST'd, scanner sees EOF.

**Why no current retry:**
- The chat runner has no retry wrapper around `Stream.Final()`. Every transient error propagates immediately.
- Mid-stream retry is hard *because* you can't safely re-issue once partial output (text, tool calls) has committed.

## Three-layer fix

### Layer 1 — TCP keepalive on every adapter HTTP client

`core/llm/httpx/keepalive.go` (new): `func DefaultTransport() *http.Transport` returning a Transport with:
```go
DialContext: (&net.Dialer{
  Timeout:   30 * time.Second,
  KeepAlive: 30 * time.Second, // OS-level TCP keepalive ping
}).DialContext,
TLSHandshakeTimeout:   10 * time.Second,
ExpectContinueTimeout: 1 * time.Second,
ResponseHeaderTimeout: 60 * time.Second,
IdleConnTimeout:       90 * time.Second,
ForceAttemptHTTP2:     true,
```

**No `Transport.Timeout`** — context drives lifetime per the
adapter's existing comment ("// no Timeout — context drives
lifetime"). Keepalive is OS-level pings during idle, not a request
timeout.

All four adapters (anthropic, openai, openrouter, bedrock) replace
`&http.Client{}` with `&http.Client{Transport: httpx.DefaultTransport()}`.
Each adapter's `WithHTTPClient` option still wins for tests/custom
gateways.

This **prevents** most edge-proxy idle disconnects without any
retry logic. Most production crashes go away with this single fix.

### Layer 2 — Classified retry at the chat-runner boundary

When Layer 1 isn't enough (real upstream 5xx, true network blip),
retry the **whole assistant turn** at the chat-runner level — but
ONLY when safe.

#### Retry decision matrix

| Failure point | Partial output? | Tool calls executed? | Action |
|---|---|---|---|
| Pre-stream (HTTP request errored) | No | No | **Retry** with backoff (up to 3 attempts) |
| Stream open, headers received, scanner errored before any delta | No | No | **Retry** |
| Stream errored after text deltas, no tool calls | Yes (text) | No | **Continuation prompt** (see Layer 3) |
| Stream errored after tool calls executed | Yes | Yes | **No retry** — surface "Connection lost" with resume button |
| ErrAuth (401/403) | Any | Any | **No retry** — propagate immediately |
| ErrInvalidRequest (400) | Any | Any | **No retry** — bug in our code or model |
| ErrTransient (429, 5xx, network) | No tool calls executed | No | **Retry** |

Implementation site: `core/rpc/views/agentgraph/chat/chat_runner.go`
or wherever the LLM stream is consumed inside the assistant_turn
node. The retry wrapper:
- Only retries `*llm.ErrTransient`. ErrAuth/ErrInvalidRequest/ErrCancelled propagate untouched.
- Backoff: 500ms → 1s → 2s → fail (3 attempts max). Jittered ±10%.
- Each retry sets a fresh `attempt_n` field on a logging context so observability picks it up.
- Counts against MaxIterations? No — a retry is fixing the SAME iteration, not advancing. Maintain a per-turn `attempts` counter exposed on `Response.Attempts` (already exists on the type).

#### Where the fix lives

Two places need changing:
1. **Pre-stream retry** — `Adapter.Stream()` errors before any event channel exists. Easy: wrap the call site in a retry loop.
2. **Mid-stream retry** — only when no partial output and no tool calls. The chat-runner needs to pump events into a buffer; when stream errors with ErrTransient AND buffer has no tool_use blocks AND text content is empty, restart from scratch. When buffer is non-empty, escalate to Layer 3.

### Layer 3 — "Resume?" UI affordance for unrecoverable mid-flight

When mid-stream fails AND partial output committed, persist what we
have and surface a UI affordance instead of dying.

**Backend:**
- On stream error with partial output: chat runner persists the
  partial assistant message with `streaming_failed_at: <timestamp>`,
  `streaming_failure_kind: "transient"|"auth"|"unknown"`,
  `streaming_recoverable: bool` (true when no tool calls executed).
- New columns on `session_messages`: `streaming_failed_at TIMESTAMP`,
  `streaming_failure_kind TEXT`, `streaming_recoverable BOOLEAN`.
  Migration after the autonomy / worklist ones (next free number).
- New RPC `Sessions_ResumeMessage(messageID)`:
  - Loads the partial message.
  - Reconstructs the conversation history with the partial as the
    last assistant turn.
  - Sends a continuation prompt: `"Your previous reply was cut off
    by a network error. Continue from where you stopped. Your
    interrupted reply ended with: <last 200 chars>"`.
  - Streams the continuation as a NEW assistant message
    (`continuation_of: <original messageID>`).

**Frontend:**
- `MessageBubble.vue`: when `streaming_failed_at` is set AND
  `streaming_recoverable=true`, render a "Connection lost — Resume"
  button at the bottom of the partial bubble.
- Click → `Sessions_ResumeMessage(messageID)`.
- A new sibling bubble appears below; the original is greyed out
  with a "(continued below)" caption.
- When `streaming_recoverable=false` (tool calls already ran), show
  the grey "(connection lost mid-tool-call; please re-send your
  request)" footer with no resume button — re-running the same
  request is the right move there.

### Layer 4 (deferred) — long-poll heartbeat

A future enhancement: emit a synthetic StreamEvent every 30s that
the model is "still thinking" (parsed from Anthropic's `ping`
events; absent in OpenAI/OpenRouter), so the UI doesn't go silent.
Tracked separately.

## Logging / observability

Every retry decision logs at info level:
- `chat.retry.attempt` — provider, kind, attempt_n, backoff_ms, error_class.
- `chat.retry.skip_partial_output` — when mid-stream + partial output prevents retry; will trigger Layer 3 instead.
- `chat.retry.exhausted` — final failure after N attempts.
- `httpx.keepalive.dial` — periodic on slow connects so we know the keepalive transport is in play.

## Out of scope

- Long-poll heartbeat events (Layer 4).
- Cross-turn replay (re-running the previous turn's tool calls). The
  resume flow is a continuation, not a rewind.
- Provider-specific HTTP/3 tuning.
- Bandwidth-detection / adaptive timeout. Static keepalive interval.

## Acceptance smoke

1. **Layer 1 sanity**: tcpdump while a long DeepSeek stream is mid-thought; confirm TCP keepalive packets every 30s. Stream completes without disconnect.
2. **Layer 2 pre-stream retry**: kill the network with `pfctl` 100ms after Send; the adapter's HTTP request fails; retry succeeds 500ms later when network is restored. Single bubble emits.
3. **Layer 2 mid-stream retry (no partial)**: mock a stream that errors after handshake but before first delta; confirm retry kicks; final assistant message renders identically.
4. **Layer 3 mid-stream resume (text only, no tools)**: partial message persists, "Resume" button shows, click → continuation appears below the original.
5. **Layer 3 mid-stream no-resume (tools already ran)**: partial message shows grey "re-send" footer; no Resume button.
6. **No retry on auth**: 401 propagates immediately as ErrAuth without retrying.
