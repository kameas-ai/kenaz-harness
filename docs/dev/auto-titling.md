# Auto-Titling — Operator Notes

Mission `session-auto-titling-01KQ8TDS` ships an opt-out auto-title flow
that fires once per session after the first user-assistant exchange
completes. This document captures the operator-facing surface: what to
flip, what to watch, and how to reproduce the flow end-to-end.

## What it does

After a chat session's first kernel run terminates cleanly, the chat
runner schedules a one-shot async goroutine that:

1. Re-reads the session record under the store's read lock.
2. Confirms `auto_titled == 0` and the session name still matches a
   placeholder pattern (empty, `New chat…`, or `Chat …`).
3. Reads up to the most-recent 10 active messages.
4. Calls the auto-title generator (a 5-second-bounded LLM round-trip
   tagged with `cost.KindAutoTitle`).
5. Writes the sanitized title back via `Manager.AutoTitle` (atomic with
   `auto_titled = 1`).
6. Emits `EventKindSessionAutoTitled` for both success and failure paths
   so the audit dashboard can see the attempt.

A user rename between schedule and fire causes the write to no-op
(`session.ErrAutoTitleSuperseded`) — the audit row still lands so the
"lost the race" signal is visible.

## Settings dials

The Settings panel exposes three dials under "Auto-titling":

| Dial | Default | Notes |
|---|---|---|
| `AutoTitleEnabled` | `true` (nil → effective true) | Master kill switch. Disabling skips both the auto trigger and the manual `Sessions_SuggestTitle` RPC. |
| `AutoTitleModel` | unset (chained default) | When unset, the resolver tries `Settings.CompactionModel` first, then falls back to the active chat's `(profileID, modelOverride)` pair. |
| `AutoTitleWorkflowRuns` | `false` | Reserved for the workflows mission. Inert until `kind="workflow_run"` sessions exist. |

Per-field RPC accessors mirror the compaction-model pattern:
`Settings_Get/SetAutoTitleEnabled`, `Settings_Get/SetAutoTitleModel`,
`Settings_Get/SetAutoTitleWorkflowRuns`. Each round-trips
independently of `SaveAll`/`LoadAll`.

## Manual surfaces

The session header exposes a "Suggest new title" button that calls
`Sessions_SuggestTitle(id)`. When the current name is non-empty and
non-auto-titled, the frontend confirms before overwriting. On success
the session row updates italic-and-muted (the auto-titled rail
distinction).

`Sessions_ClearTitle(id)` writes `name=""` and `auto_titled=0` and
emits `session.renamed`. The next eligible chat run will re-trigger the
auto-titler. The left-rail rename input also clears via this path when
the user submits an empty string.

## Cost + audit

- Cost tag: `cost.KindAutoTitle = "auto_title"`. The wiring adapter
  (`core/sessions/autotitle/wiring/llm.go`) tags every call, so the
  cost reducer reports auto-titling separately from chat / compaction.
- Audit kind: `audit.KindSessionAutoTitled` with payload
  `{session_id, generated_title?, model_used, duration_ms, trigger,
  error_kind?}`. `trigger` is one of `first_turn`, `manual`,
  `after_clear`.
- Per NFR-002, the round-trip should stay under ~210 input tokens
  (system prompt + truncated last user-assistant pair clamped to
  6 KB). Watch the `auto_title` cost row for drift.

## Smoke checklist (release-time)

Walk these end-to-end before merging the auto-titling release into
production. Each step is a manual gate; the flow is asynchronous so
allow up to 5 seconds for the title write.

1. **Happy path.** Fresh install → new session → send "what's a good way
   to learn Rust?" → assistant replies. Within 5s the rail row should
   transition from "New session" to a Rust-ish title rendered italic
   and muted.
2. **Manual rename locks out auto-title.** Edit the title to "Rust
   learning plan" and press enter. Send another turn. Title remains
   "Rust learning plan" (italic gone — `auto_titled=1`, but the rail
   distinction class is dropped because the user committed it).
3. **Clear rename re-arms.** Submit an empty rename (left rail input).
   Title clears, `auto_titled` flips to 0. Send another turn → fresh
   auto-title fires; italic returns.
4. **Manual suggest with confirm.** Open a long session that already
   has a non-empty user-set title. Click "Suggest new title" in the
   header. Confirm-overwrite modal appears → confirm → new title lands.
5. **Kill switch.** Toggle `AutoTitleEnabled` off. Create a new session
   and send a turn. Rail row stays "New session" indefinitely. No
   `KindSessionAutoTitled` audit row.
6. **Forced provider failure.** Set `AutoTitleModel` to a deliberately-
   broken provider profile. Create a new session and send a turn. Rail
   row stays "New session" but a `KindSessionAutoTitled` audit row
   exists with `error_kind="provider_error"` (or whichever classified
   error fired).
7. **Workflow-runs dial (deferred).** With `AutoTitleWorkflowRuns=false`
   run a workflow → workflow_run session keeps its derived title. Flip
   the dial → next workflow run gets an auto-title. Skip until the
   workflows mission lands.

## Regression coverage

- `core/session/store_test.go` and `core/session/manager_test.go`
  pin the schema, the predicate-recheck inside `AutoTitle`, and the
  `Rename`/`ClearTitle` interactions with `auto_titled`.
- `core/sessions/autotitle/generator_test.go` and `validate_test.go`
  pin sanitization, length clamps, refusal handling, and the 5s
  context cancellation.
- `core/rpc/views/agentgraph/chat/chat_runner_integration_test.go`
  walks the chat-runner trigger end-to-end against the real
  `chat_default.yaml`: happy path, dial off, generator failure,
  manual re-trigger.

## Failure modes worth watching

| Symptom | Likely cause | Where to look |
|---|---|---|
| Sessions never auto-title | Dial off, or AutoTitle deps not wired in `core/rpc/api.go::buildChatRunner` | `chat.autotitle.disabled` log; `Config.AutoTitle == nil` |
| Auto-title fires twice on one session | `Manager.AutoTitle` predicate-recheck regressed | `core/session/store.go` `AutoTitle`; integration scenario `happy path fires once` |
| Rename mid-flight produces double-write | Race between `Rename` and `fireAutoTitle` | `chat.autotitle.superseded` debug log; `ErrAutoTitleSuperseded` path |
| Auto-title produces gibberish | Model not following ≤50-char instruction; sanitize should clamp | `Sanitize` truncation log; `validate.go::ErrTitleTooShort` / refusal heuristics |
| Cost reducer shows `auto_title` rows after disable | Wiring adapter still tagging despite kill switch | `core/sessions/autotitle/wiring/llm.go`; chat runner predicate gate |
