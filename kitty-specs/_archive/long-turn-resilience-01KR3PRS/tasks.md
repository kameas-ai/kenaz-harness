# Tasks — long-turn-resilience-01KR3PRS

4 WPs; first is independent and high-leverage, last needs frontend.

## WP00 — Frontend fast-fix: stop dropping partial assistant content

**Goal**: stop the user-visible "messages disappeared" bug TODAY, before
WP03's full backend resume flow ships. Smallest possible change.

- `frontend/src/lib/useSession.ts:244–256` (the `case 'error'` branch
  in the streaming chunk handler):
  - When an error event fires AND `currentlyStreaming.value` has
    content, commit it to `messages.value` with a new
    `streaming_error: string` field BEFORE setting `error.value`.
  - Do NOT null `currentlyStreaming.value` here — let the
    `llm:stream-closed` handler do the cleanup. (It's idempotent
    once we commit.)
- `frontend/src/lib/useSession.ts:259–276` (the `llm:stream-closed`
  handler):
  - Commit `currentlyStreaming.value` to `messages.value` regardless of
    `payload.reason`. Today only the reason==='completed' path commits.
  - Stamp `streaming_error: payload.reason || 'closed-without-finish'`
    on the committed message when `reason !== 'completed'`.
- `frontend/src/lib/types.ts` `Message`:
  - Add `streamingError?: string` (omitempty serialise).
- `frontend/src/components/chat/MessageBubble.vue`:
  - When `streamingError` is set, render a "Connection lost"
    sub-line under the bubble. No Resume button yet (that's WP03);
    user can resend manually for now.
- Vitest: drive a sequence of `text` deltas → `error` event →
  `stream-closed reason='error'`. Assert the partial content lands in
  `messages.value` with `streamingError` set.

**Done when**: kill-network-mid-stream test no longer loses partial
output; user sees the partial bubble persist with a "Connection lost"
hint.

## WP01 — TCP keepalive transport (Layer 1)

**Goal**: kill 80% of the bug class with one HTTP transport change.

- `core/llm/httpx/keepalive.go` (new): `DefaultTransport()` returning
  the configured `*http.Transport` (see plan.md §Layer 1).
- `core/llm/anthropic/anthropic.go`: replace `&http.Client{}` with
  `&http.Client{Transport: httpx.DefaultTransport()}`.
- Same edit in `core/llm/openai/openai.go`,
  `core/llm/openrouter/openrouter.go`, `core/llm/bedrock/bedrock.go`,
  `core/llm/bedrock/bearer.go`.
- `WithHTTPClient` option still wins for tests/custom gateways.
- Tests:
  - Per-adapter: passing a custom `http.Client` via `WithHTTPClient`
    overrides the keepalive transport.
  - `httpx` package: `DefaultTransport()` returns a Transport with
    KeepAlive=30s (assert via reflection on the dialer).

**Done when**: every adapter constructs its default client through
the keepalive transport; existing tests stay green.

## WP02 — Classified retry-with-backoff (Layer 2)

**Goal**: pre-stream + safe mid-stream retries.

- `core/llm/retry/retry.go` (new): `RetryStream(ctx, fn func() (Stream, error), policy Policy) (Stream, error)`. Policy specifies max attempts (default 3), base delay (500ms), jitter (±10%).
- Retry classification:
  - `*llm.ErrTransient` → retry.
  - `*llm.ErrAuth` / `*llm.ErrInvalidRequest` / `*llm.ErrCancelled` → propagate.
- Wire site: `core/rpc/views/agentgraph/chat/llm_provider_adapter.go`
  (where `Adapter.Stream()` is called). Wrap that call with
  `RetryStream`.
- Mid-stream classifier: when the stream events channel closes with
  ErrTransient AND the per-turn buffer has zero tool_use AND zero text
  content → restart from scratch (re-issue Stream + drain). One
  attempt; if it fails again, give up and fall through to Layer 3.
- Logging: `chat.retry.attempt` / `.exhausted` / `.skip_partial_output`.
- Tests:
  - Pre-stream 503 → retried after 500ms, succeeds on second attempt.
  - Pre-stream 401 → no retry, propagates.
  - Mid-stream error before any delta → silent retry; no user-visible
    failure.
  - Mid-stream error after text deltas → no retry, escalates to Layer 3.
  - Mid-stream error after tool_use → no retry.
  - 3 consecutive transient failures → final ErrTransient propagates.

**Done when**: contrived flaky-server fixture exhibits the matrix in
plan.md §Retry decision matrix.

## WP03 — Resume flow (Layer 3)

**Goal**: don't lose partial output.

- New SQLite migration (next free number after autonomy/worklist
  migrations have landed). Adds three columns to `session_messages`:
  `streaming_failed_at TIMESTAMP`, `streaming_failure_kind TEXT`,
  `streaming_recoverable BOOLEAN`. Update migrations_test.go count.
- `core/session/store.go`: `Message` struct gains the three fields.
  ListMessages SELECTs them.
- Chat runner partial-persist path: when stream errors AND buffer
  contains text content but zero tool_use, persist the message with
  `streaming_failed_at = NOW()`, `streaming_failure_kind = "transient"|...`,
  `streaming_recoverable = true`. When tool_use exists, persist with
  `streaming_recoverable = false`.
- `core/rpc/views/sessions/api.go`: `Sessions_ResumeMessage(messageID) (newMessageID string, err error)`.
- Resume implementation:
  - Load the partial message + its session history.
  - Build a continuation system-injection: `"Your previous reply was
    cut off by a network error. Continue from where you stopped.
    Your interrupted reply ended with: <last 200 chars>"`.
  - Stream the continuation as a NEW assistant message linked to the
    original via `continuation_of` (new column or session message
    metadata).
- Frontend: `MessageBubble.vue` renders `streaming_failed_at` state.
  Recoverable → Resume button. Non-recoverable → grey re-send footer.
- Wails binding + harnessClient typed method.
- Tests:
  - Stream errors with text content → row persisted with
    `streaming_recoverable=true`.
  - Stream errors with tool_use → `streaming_recoverable=false`.
  - Resume flow: original + continuation in correct order; original
    bubble shows greyed; continuation bubble has fresh ID.
  - Vitest: button visibility logic; click handler calls the typed
    method.

**Done when**: kill-the-network-mid-stream test produces a partial
bubble with a working Resume button; click resumes cleanly.

## WP04 — Acceptance smoke + mission doc

- Walk plan.md §Acceptance smoke 1–6 in the running app.
- `docs/missions/long-turn-resilience.md`.
- MEMORY.md index update.
- Add to `docs/missions/cedar-credential-policy.md` follow-ups list
  if any cross-mission interaction is found.

## Order

- WP01 → WP02 → WP03 → WP04. Each green before the next starts.
- WP01 alone is so cheap and high-leverage it should ship even if
  WP02/WP03 stall. Prioritise.
