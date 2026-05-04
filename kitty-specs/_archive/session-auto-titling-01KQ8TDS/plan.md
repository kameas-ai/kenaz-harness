# plan.md — session-auto-titling-01KQ8TDS

Auto-generate concise session titles after the first user-assistant exchange completes. One-shot, opt-out by user-edit, manual re-trigger via "Suggest new title" header affordance, configurable via Settings dials.

## 1. Branch contract

- **Branch**: `kitty/session-auto-titling-01KQ8TDS`
- **Base**: `main`
- **Soft deps**:
  - `compaction-strategy-ui-01KQ8TDI` (already on `main`) — reuse `Settings.CompactionModel` as chained default for `AutoTitleModel`, the `compaction.ProviderProfileRef` shape, the `compaction/wiring.LLMCaller` profile-resolution pattern, the `cost.Kind*` cost-tag convention.
  - `provider-implementation-uniformity-01KQ8V4F` (merged) — capability descriptor lookup is the same surface compaction uses.
  - `workflows-01KQ8TDG` (in flight, not hard dep) — that mission introduces `kind="workflow_run"` sessions. Until it lands, `Settings.AutoTitleWorkflowRuns` dial is wired but inert.
- **No imports** of internal packages from any of the soft deps; we reuse only their exported types + constants.
- **Merge gate**: all WPs done, integration test (WP06) green, smoke produces auto-title within 5s on success path and leaves placeholder on a forced-failure run.

## 2. Architecture

### 2.1 Generator package — `core/sessions/autotitle/`

```
core/sessions/autotitle/
  generator.go        // GenerateTitle(ctx, transcript) (string, error)
  generator_test.go
  validate.go         // length clamp (>50 truncate, <3 fail) + sanitize
  validate_test.go
  doc.go
```

- `Generator` carries an `LLMCaller` interface so the package never imports `core/llm/registry` directly.
- `GenerateTitle` builds user prompt by rendering most recent user-assistant pair as `User: ...\nAssistant: ...` (truncated to ~6 KB to honor NFR-002's <200-input-token target). System prompt is the locked text:
  > "Produce a concise (≤ 50 chars) title summarizing this conversation. Output ONLY the title, no quotes, no explanation."
- Returned title runs through `validate.Sanitize`:
  - Strip leading/trailing whitespace + matched outer quotes.
  - Strip leading "Title:" prefix.
  - If < 3 runes after sanitize, return `ErrTitleTooShort`.
  - If > 50 runes, truncate to 49 runes + ellipsis (`…`).
  - If contains newline, take only the first non-empty line.

### 2.2 Trigger — chat runner observes first assistant turn complete

`core/rpc/views/agentgraph/chat/chat_runner.go::driveRun` already runs after the kernel completes. Trigger is additive post-completion step that fires async (via `go`) so chat surface stream-close is not delayed.

Eligibility predicate (all must be true):
1. `Settings.AutoTitleEnabled == true`.
2. Session record's `auto_titled == false` AND name matches placeholder pattern.
3. Kernel run terminated with non-error, non-paused-without-assistant-turn outcome.
4. If `Settings.AutoTitleWorkflowRuns == false` AND session has `kind == "workflow_run"` marker, skip.

Eligible runs schedule:
```go
go r.fireAutoTitle(context.Background(), sub.sessionID, profileID, modelOverride)
```

`fireAutoTitle`:
- Re-reads session record under short read lock (race-safe).
- If `auto_titled == true` OR name no longer matches placeholder → return.
- Reads up to most-recent 10 messages via `manager.ListMessagesActive`.
- Calls `autotitle.Generator.GenerateTitle(ctx, transcript)`.
- On success: `manager.AutoTitle(ctx, sessionID, generatedTitle)`.
- On failure: `manager.MarkAutoTitleAttempted(ctx, sessionID)`.
- Both paths emit `KindSessionAutoTitled`.

Context: `fireAutoTitle` uses fresh context (not request-scoped `streamCtx`) with 5s timeout (NFR-001).

### 2.3 Schema — `sessions.auto_titled bool`

Migration `0311-auto-titled` (next slot after compaction's 0310):
```sql
ALTER TABLE sessions ADD COLUMN auto_titled INTEGER NOT NULL DEFAULT 0;
```

- Stored as INTEGER 0/1 to match existing harness sqlite convention.
- Default `0` means: existing sessions are "never tried" — but trigger predicate requires placeholder name match, so existing sessions with user-edited names won't be re-titled.
- New write paths in `core/session/store.go`:
  - `AutoTitle(ctx, id, name string, now time.Time) error` — atomically sets name + `auto_titled=1`.
  - `MarkAutoTitleAttempted(ctx, id string, now time.Time) error` — sets `auto_titled=1` without changing name.
  - `Rename` augmented to also set `auto_titled=1` whenever new name is non-empty.
  - `Rename` to empty: now legal for manual-clear path; resets `auto_titled=0`.

### 2.4 Manual title path

- Existing `Sessions_Rename` behavior unchanged: typing a new name and hitting enter sets title and locks `auto_titled=1`.
- New `Sessions_ClearTitle(id)` binding hits `Manager.ClearTitle` which writes `name=""`, `auto_titled=0`, and emits `EventKindSessionRenamed`.

### 2.5 Settings dials

```go
AutoTitleEnabled       *bool                `json:"autoTitleEnabled,omitempty"`
AutoTitleModel         ProviderProfileRef   `json:"autoTitleModel,omitempty"`
AutoTitleWorkflowRuns  bool                 `json:"autoTitleWorkflowRuns,omitempty"`
```

- `AutoTitleEnabled` is `*bool` so zero value (nil) means "default true". `EffectiveAutoTitleEnabled() bool` returns `true` when nil.
- `AutoTitleModel.IsZero() == true` triggers chained-default fallback: try `Settings.CompactionModel` first; if zero, fall back to active chat's `(profileID, modelOverride)`.
- `AutoTitleWorkflowRuns` defaults `false` (Q1=C).

Per-field RPC accessors (mirrors compaction-model pattern):
- `Settings_GetAutoTitleEnabled` / `Settings_SetAutoTitleEnabled`
- `Settings_GetAutoTitleModel` / `Settings_SetAutoTitleModel`
- `Settings_GetAutoTitleWorkflowRuns` / `Settings_SetAutoTitleWorkflowRuns`

### 2.6 Length validation + sanitize

`core/sessions/autotitle/validate.go::Sanitize(raw string) (string, error)`:
- Trim ASCII whitespace.
- Strip matched outer single/double quotes.
- Strip leading `Title:`-style prefix (case-insensitive).
- First-line-only when raw string contains newline.
- Rune-count length: `< 3` → `ErrTitleTooShort`; `> 50` → truncate to 49 runes + `…`.
- Reject control characters (replace with space).

The 50-char cap is enforced both here (post-model output) and in the prompt itself.

### 2.7 "Suggest new title" header affordance (Q2=C)

Frontend chat header (top of `frontend/src/views/sessions/SessionsView.vue`):
- Small button next to session title triggers `Sessions_SuggestTitle(id)`.
- Disabled while suggestion in flight; shows spinner; re-enabled when broker emits `session.title_updated` (success) or `session.auto_title_failed` (failure toast).

Backend:
- New RPC `Sessions_SuggestTitle(id)` invokes `Manager.RequestRetitle(ctx, id)`:
  - Calls `autotitle.Generator.GenerateTitle` against current full transcript.
  - On success, atomically sets `name = generated`, `auto_titled = 1`, emits `EventKindSessionRenamed` + `KindSessionAutoTitled` audit with `trigger="manual"`.
  - On failure, returns typed error.

Manual re-trigger overwrites a previously-set user title — but we surface a confirm modal before overwriting non-empty titles.

### 2.8 Audit + cost

```go
KindSessionAutoTitled Kind = "sessions.auto_titled"

type SessionAutoTitledPayload struct {
    SessionID      string `json:"session_id"`
    GeneratedTitle string `json:"generated_title,omitempty"`
    ModelUsed      string `json:"model_used"`
    DurationMs     int64  `json:"duration_ms"`
    Trigger        string `json:"trigger"` // "first_turn" | "manual" | "after_clear"
    ErrorKind      string `json:"error_kind,omitempty"`
}
```

Cost tag in `core/llm/cost/reducer.go`:
```go
const KindAutoTitle = "auto_title"
```

The autotitle wiring adapter tags every call with `KindAutoTitle`, mirroring `core/compaction/wiring/llm.go::recordOverhead`.

### 2.9 Frontend distinction (FR-010)

`Session` wire shape gains `AutoTitled bool`.

`LeftRail.vue` reads new flag:
- Auto-titled rows render title in italic with slightly muted color.
- On hover, tooltip says "Auto-generated title — click to edit".
- On rename submit, rail clears italic immediately (optimistic).

Subtle italic + muted is the "soft hint" the spec asks for.

## 3. Risk register

| Risk | Likelihood | Mitigation |
|---|---|---|
| Auto-title fires before assistant turn durably persisted | M | Trigger fires inside `driveRun`'s post-`Kernel.Run` block, AFTER `HistoryWriter` has written the assistant turn. |
| User renames mid-flight, then auto-title overwrites | M | `Manager.AutoTitle` is conditional: re-checks `auto_titled == 0 && name matches placeholder` inside same transaction. |
| Model returns garbage / refuses to title | M | Sanitize strips leading "Title:" prefixes; "Sorry|I can't|I cannot" prefix → `ErrModelRefused`. |
| Auto-titler runs against workflow_run before workflows mission lands | L | `kind` column doesn't exist yet → predicate falls through to "eligible". Acceptable until workflows-01KQ8TDG lands. |
| Cost surprise: auto-title for every new session | L | NFR-002 caps ~210 tokens/call (~$0.0001 with Haiku). `AutoTitleEnabled=false` kill-switch. |
| Manual re-trigger overwrites carefully-curated title | M | Frontend confirm modal when `name != ""`. Auto-titled titles get re-titled without modal. |
| Title generation hits paused-tool-use turn | M | Predicate checks for at least one assistant message with non-empty text content. |
| `Settings.AutoTitleModel` points to non-existent profile | M | Chassis profile-lookup adapter returns `ok=false`; wiring layer treats as `ErrProfileNotFound`. |

## 4. Rollout + smoke

### 4.1 Rollout

- All work behind `Settings.AutoTitleEnabled` dial, default true.
- Schema migration `0311` is additive.
- Cost-tag constant `KindAutoTitle` is additive.
- New RPC bindings are additive.

### 4.2 Smoke checklist

1. Fresh install: create new session → send "what's a good way to learn Rust?" → assistant replies → within 5s rail row updates from "New session" to a Rust-ish title (italic + muted).
2. Edit title to "Rust learning plan" → send another turn → title remains "Rust learning plan" (italic gone).
3. Clear title (submit empty rename) → send another turn → new auto-title fires, italic returns.
4. Click "Suggest new title" header button on long session → confirm-overwrite modal appears → confirm → new title lands.
5. Disable `AutoTitleEnabled` → create new session, send turn → rail row stays "New session" forever.
6. Set `AutoTitleModel` to deliberately-broken provider → create new session, send turn → rail row stays "New session" but `KindSessionAutoTitled` audit row exists with `error_kind="provider_error"`.
7. Workflow_run smoke (deferred): with `AutoTitleWorkflowRuns=false`, run workflow → workflow_run session keeps derived title. Flip dial → next workflow run gets auto-title.
