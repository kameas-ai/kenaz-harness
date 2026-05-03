# Long-Turn Resilience — Rollout Guide

Mission: `long-turn-resilience-01KR3PRS`

## Background

Long-running assistant turns — especially DeepSeek reasoning chains over
OpenRouter, where the model can sit silent for minutes between SSE
deltas — were dying mid-stream with the user-visible chain
`loop: node "agent_loop": body assistant_turn: model: ... chat: stream final: llm: transient provider error: Network connection lost.`
The TCP socket got RST'd by an edge proxy that idle-timed-out the silent
SSE; the chassis surfaced the wrapped error, the partial assistant
output evaporated, the user had to retype, and any tool calls already
emitted were forgotten. This mission delivers three layers of resilience
that, together, take that bug class from "happens daily" to "covered".

## Status

| Work Package | Title | Status |
|---|---|---|
| WP00 | Frontend partial-message commit on stream error | Merged |
| WP01 | TCP keepalive transport (Layer 1) | Merged |
| WP02 | Classified retry-with-backoff (Layer 2) | Merged |
| WP03 | Resume flow (Layer 3) | In flight (`feat/long-turn-resilience-wp03`) |
| WP04 | Acceptance smoke + this mission doc | **This branch** |

## Layered Strategy

| Layer | Package / file | Behavior | What it doesn't cover |
|---|---|---|---|
| **0. Frontend partial-commit** | `frontend/src/lib/useSession.ts`, `MessageBubble.vue` | When the streaming chunk handler sees an `error` event with non-empty `currentlyStreaming`, commits the partial bubble to `messages.value` with a `streamingError` field BEFORE clearing state. `llm:stream-closed` commits regardless of `payload.reason`. | Stops the visual disappearance, but the user still has to manually re-send. No backend persistence of the partial. |
| **1. TCP keepalive transport** | `core/llm/httpx/keepalive.go` (`DefaultTransport()`); wired by `anthropic`, `openai`, `openrouter`, `bedrock`, `bedrock/bearer`. | `net.Dialer.KeepAlive=30s` arms the kernel to send TCP keepalive probes during idle, defeating typical 60–300 s edge-proxy idle-timeouts on CloudFront / Cloudflare / OpenRouter's gateway. `WithHTTPClient(...)` still wins for tests / custom gateways. | True upstream 5xx, real network blips (carrier handover, VPN flaps), provider-side rate-limit drops. |
| **2. Classified retry-with-backoff** | `core/llm/retry/retry.go` (`RetryStream`, `StreamPolicy`); wired at `core/rpc/views/agentgraph/chat/llm_provider_adapter.go:144`. | Pre-stream `*llm.ErrTransient` → up to 3 attempts with 500 ms base, exponential, ±10 % jitter. Mid-stream `*llm.ErrTransient` with **zero** text and **zero** tool_use emitted → silent restart. `ErrAuth` / `ErrInvalidRequest` / `ErrCancelled` propagate immediately. | Mid-stream failures **after** content has emitted (text or tool_use). Re-issuing would either duplicate text or double-fire tool side-effects, so the layer escalates to Layer 3. |
| **3. Resume flow** | New SQLite columns (`session_messages.streaming_failed_at` / `_kind` / `_recoverable`); chat runner partial-persist; `Sessions_ResumeMessage` RPC; `MessageBubble.vue` Resume button. *(WP03, in flight)* | When mid-stream fails with partial output, persist the row marked `streaming_recoverable=(no tool_use ran)`. Frontend renders Resume (if recoverable) or grey re-send footer (if not). Resume issues a continuation prompt as a NEW assistant message linked via `continuation_of`. | Cross-turn replay (re-running prior tool calls). Long-poll heartbeat events (Layer 4, deferred). |

### Retry decision matrix (from `plan.md`)

| Failure point | Partial output? | Tool calls? | Action |
|---|---|---|---|
| Pre-stream HTTP error | No | No | **Retry** (Layer 2) |
| Stream open, scanner errored before any delta | No | No | **Retry** (Layer 2) |
| Stream errored after text deltas | Yes | No | **Resume** (Layer 3, recoverable) |
| Stream errored after tool calls executed | Yes | Yes | **No retry, no resume** — render grey re-send footer (Layer 3) |
| `ErrAuth` (401/403) | Any | Any | **Propagate** immediately |
| `ErrInvalidRequest` (400) | Any | Any | **Propagate** immediately |
| `ErrTransient` (429/5xx/network), no content | No | No | **Retry** (Layer 2, up to 3 attempts) |

## Acceptance Criteria

The cross-layer smoke test `core/llm/retry/acceptance_smoke_test.go`
asserts the matrix end-to-end. Each numbered step maps directly onto
`plan.md` §Acceptance smoke 1–6 and is exercised by a corresponding
`TestAcceptance_*` function:

1. **Layer 1 sanity** — `httpx.DefaultTransport()` returns a `*http.Transport`
   with `ForceAttemptHTTP2`, non-zero `ResponseHeaderTimeout`, non-zero
   `IdleConnTimeout`, and non-zero `TLSHandshakeTimeout`. No request-level
   `Timeout` (context drives lifetime).
2. **Layer 2 pre-stream retry** — first stream-open returns 503; the wrapper
   waits for the backoff; second open succeeds; user sees a single bubble
   with the second stream's text. `fn` is called exactly twice.
3. **Layer 2 mid-stream retry (no partial)** — handshake completes, scanner
   errors with `ErrTransient` before any delta; wrapper restarts from
   scratch; the second stream's content reaches the caller transparently;
   no escalation to the resume surface.
4. **Layer 3 resume hand-off (text-only partial)** — text deltas land,
   then `ErrTransient`; **no retry**; partial events preserved verbatim;
   the resume surface is notified with `recoverable=true` and
   `kind="transient"`. *(Full RPC + SQLite assertions fenced behind
   WP03 — see "Open follow-ups".)*
5. **Layer 3 no-resume hand-off (tool_use partial)** — tool_use emits,
   then `ErrTransient`; **no retry**; resume surface is notified with
   `recoverable=false` so the frontend renders the grey re-send footer
   instead of a Resume button.
6. **No retry on auth** — `ErrAuth{Status: 401}` propagates immediately
   on the very first attempt; resume surface receives a non-transient
   classification so no Resume button is offered.

## Acceptance Smoke (manual, post-merge)

Run after a release build to validate the integrated stack on real
hardware against a real provider:

1. Start a long DeepSeek reasoning chain via OpenRouter. With `tcpdump`
   on the loopback / NIC, confirm TCP keepalive packets every 30 s on
   the SSE socket. Stream completes without disconnect.
2. With `pfctl` (or `Network Link Conditioner`), kill the network 100 ms
   after Send. Adapter's HTTP request fails; retry succeeds 500 ms later
   when the network is restored. Single bubble emits.
3. Mock a stream that errors after handshake but before first delta;
   confirm retry kicks in; final assistant message renders identically.
4. Drop the network mid-text-stream. Partial bubble persists with
   "Connection lost — Resume" affordance (WP03). Click → continuation
   appears below the original; original is greyed with "(continued
   below)".
5. Drop the network mid-tool-stream (after tool_use has executed). Bubble
   shows grey "(connection lost mid-tool-call; please re-send your
   request)" footer. **No** Resume button.
6. Forge a 401 (rotate the key without telling the app). Failure
   propagates as `ErrAuth` immediately. No retry, no Resume.

## Open follow-ups

1. **WP03 mainline merge** — once `feat/long-turn-resilience-wp03` lands
   on `release/v0.2.0`, flip the `wp03Available` constant in
   `core/llm/retry/acceptance_smoke_test.go` to `true` and remove the
   `t.Skip` calls in `TestAcceptance_Layer3_TextPartialEscalatesToResume`
   and `TestAcceptance_Layer3_ToolUsePartialIsNotRecoverable`. Replace
   the in-process `resumeHook` assertions with the real
   `Sessions_ResumeMessage` RPC round-trip + `session_messages` column
   reads (`streaming_failed_at`, `streaming_failure_kind`,
   `streaming_recoverable`).
2. **Layer 4 heartbeat (deferred)** — emit a synthetic StreamEvent every
   30 s while the model is reasoning silently (e.g. parsed from
   Anthropic's `ping` events; absent in OpenAI/OpenRouter). Tracked
   separately. The current three layers solve the disconnect; this is
   purely a UX-during-quiet-stretches enhancement.
3. **Manual Resume button polish** — once WP03 ships the Resume button,
   add a Vitest scenario for the "Resume click while a different turn is
   already streaming" race.
4. **Cross-mission interaction with `cedar-credential-policy`** — none
   found. Resume re-issues the continuation as a fresh stream against
   the same profile credentials; Cedar evaluates the second open
   identically to the first. No follow-ups required there.

## Logging / Observability

Every retry decision logs at info level via `core/logging`:

- `chat.retry.attempt` — `attempt_n`, `backoff_ms`, `error_class`, `error`.
- `chat.retry.skip_partial_output` — `attempt_n`, `has_text`, `has_tool`,
  `error_class`, `error`. Today this is informational; once WP03 wires
  the resume RPC, this log line marks the hand-off boundary.
- `chat.retry.exhausted` — final failure after `MaxAttempts`.
- (Layer 1) The `httpx` keepalive transport emits no per-call log; the
  unit test (`core/llm/httpx/keepalive_test.go`) is the standing
  assertion that the dialer KeepAlive field is set to 30 s.

## Out of Scope

- Long-poll heartbeat events (Layer 4 — deferred above).
- Cross-turn replay (re-running the previous turn's tool calls). The
  resume flow is a continuation, not a rewind.
- Provider-specific HTTP/3 tuning.
- Bandwidth-detection / adaptive timeout. Static keepalive interval.
