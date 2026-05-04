# Spec — Session auto-titling (`session-auto-titling-01KQ8TDS`)

**Status**: draft · **Owner**: alecfeeman

## 1. Why

Sessions today are nameable manually but new sessions land with no title (or a placeholder). After a few turns, the sidebar fills with untitled rows. ChatGPT, Claude, etc. auto-title from first message; we should too.

## 2. Goals

- New sessions get an auto-generated title after the first user-assistant exchange completes.
- Titles are concise (≤ 50 chars), descriptive, derived from the conversation content.
- Manual titles always win — auto-titling is one-shot, never overwrites a user-set title.
- Cheap: uses the configured compaction model (or a similar cheap model) for one short call.
- Honest: when auto-titling fails (model unavailable, etc.), session keeps its placeholder; doesn't error noisily.

## 3. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | New `core/sessions/autotitle/` package with `GenerateTitle(ctx, transcript) (string, error)`. Calls the configured auto-title model (default = compaction model, falls back to active session's model). Prompt: "Produce a concise (≤ 50 chars) title summarizing this conversation. Output ONLY the title, no quotes, no explanation." | proposed |
| FR-002 | Trigger: chat runner observes the first assistant turn completing in a session whose title is empty or matches the placeholder pattern. Fires `GenerateTitle` async (doesn't block the chat). On success, updates `sessions.title` and emits a `session:title-updated` broker event. | proposed |
| FR-003 | One-shot: a `sessions.auto_titled bool` column tracks whether auto-titling has fired. Default false; set true on success OR after one failed attempt (don't retry forever). User-set titles always set it to true. | proposed |
| FR-004 | Manual titling path: existing UI for editing a session title stays; saving sets `auto_titled = true` so the auto-title trigger never fires again. If the user clears the title back to empty, `auto_titled` resets to false (next turn re-triggers). | proposed |
| FR-005 | Settings dial `Settings.AutoTitleEnabled bool` (default true). When false, no auto-titling fires; sessions keep placeholders. | proposed |
| FR-006 | Settings dial `Settings.AutoTitleModel ProviderProfileRef` — defaults to `Settings.CompactionModel` (chained default). User can override. | proposed |
| FR-007 | Title length validation: response > 50 chars truncated with ellipsis; response < 3 chars treated as failure (don't ship "ok"). | proposed |
| FR-008 | Audit kind `KindSessionAutoTitled` with payload `{session_id, generated_title, model_used, duration_ms}`. No transcript content in audit. | proposed |
| FR-009 | Cost attribution: the auto-title call's tokens flow through the existing cost reducer with `cost.kind = "auto_title"` (new tag), so users can see how much auto-titling has cost. | proposed |
| FR-010 | Frontend: sidebar row visually distinguishes auto-titled vs user-titled (subtle italic? small icon?). User can click-edit any title; auto-titled becomes user-titled on edit. | proposed |

## 4. Non-functional requirements

| ID | Requirement | Threshold |
|---|---|---|
| NFR-001 | Title generation latency | < 5s wall-clock; runs async, doesn't block the chat surface |
| NFR-002 | Cost per auto-title | < 200 input tokens + ~10 output tokens per call (typical) |

## 5. Constraints

| ID | Constraint |
|---|---|
| C-001 | One-shot: never overwrites a user-set title. |
| C-002 | Async: chat surface keeps streaming the assistant turn; title appears in sidebar when ready. |
| C-003 | Privacy: title generation prompt sees the conversation transcript; that's the same surface the active model already sees. No additional exposure. |
| C-004 | Failure-tolerant: model unavailable / cap-exceeded / etc. → log + give up; placeholder remains. |

## 6. Locked open questions

- **Q1 = C**: Workflow_run sessions skip auto-titling by default — they already have a derived title from the workflow name (e.g. "release_notes – 2026-04-29 09:14"). `Settings.AutoTitleWorkflowRuns bool` (default false) lets users opt in for descriptive per-run titles when they want them.
- **Q2 = C**: One-shot auto-title only — no automatic re-titling on topic drift (avoids cost + avoids surprise overwrites). Session header gains a "Suggest new title" affordance: clicking re-runs auto-titling against the current full transcript. User retains full control; harness offers help on demand. Per-session usage of the affordance is unbounded — runs as many times as the user clicks.

## 7. Success criteria

- Send "what's a good way to learn Rust?" → assistant answers → 2-5 seconds later, sidebar row updates from placeholder to "Learning Rust" or similar.
- User edits the title to "Rust learning plan" → next turn doesn't overwrite it.
- Disable the dial → new sessions stay placeholder forever.

## 8. Out of scope

- Automatic re-titling as conversation drifts.
- Title-based search / sorting (covered in cross-session-search).
- Multi-language title generation (relies on whatever the model produces; English-default).
