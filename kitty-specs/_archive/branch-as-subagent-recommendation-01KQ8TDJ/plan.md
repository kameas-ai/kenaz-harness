# plan.md — Branch as Sub-Agent + Recommendation (`branch-as-subagent-recommendation-01KQ8TDJ`)

## Branch Contract

- **Mission branch**: `mission/branch-as-subagent-recommendation-01KQ8TDJ`
- **Base**: `main` (rebased atop `branching-ux-polish-01KQ8TDB` and `compaction-strategy-ui-01KQ8TDI` once merged)
- **Merge gate**:
  - Heuristic detector unit tests pass (signal/noise/threshold matrix).
  - Reintegration round-trip integration test passes (create branch -> work -> propose -> commit -> parent shows summary, branch transcript intact).
  - Acceptance smoke (5-step manual checklist in Rollout) recorded.
  - Audit kinds emitted at correct points (verified by audit-tap test).
  - Feature-flag off path: `HARNESS_BRANCH_ADVISOR=0` disables banner end-to-end without breaking parent send.
- **Public Go API additions** (under `core/`):
  - `core/branchadvisor/detector.go`: `Detect(message string, threshold float64) *BranchSuggestion`; `BranchSuggestion struct { ID string; Confidence float64; Rationale string; ProposedTitle string; Signals []string; ProposedContextPick []ContextItemRef }`.
  - `core/branchadvisor/signals.go`: signal/noise tables, scoring helper.
  - `core/rpc` view (or extension of `branches_wiring.go`): `Branches.ProposeReintegrationSummary(branchSessionID string) (BranchReintegrationProposal, error)` and `Branches.CommitReintegration(branchSessionID, finalSummaryText string, opts CommitOpts) (CommitResult, error)`. Reuses upstream `Sessions.CreateExplicitBranch` from branching-ux-polish for branch creation.
  - `core/rpc` view: `Sessions.AppendContext(sessionID string, items []ContextItemRef) error` for FR-015 reverse path (lower priority).
  - Session metadata struct gets `SubagentBranch bool`, `BranchAdvisorSignals []string`, `BranchRecommendationID string` JSON fields (additive).
- **Feature flag**: `HARNESS_BRANCH_ADVISOR` env var, default ON. When OFF, `Detect` returns nil immediately and the frontend banner never mounts.
- **Hard upstream deps**:
  - `branching-ux-polish-01KQ8TDB` for `Sessions.CreateExplicitBranch`, sidebar nesting, breadcrumb, "Show full history" toggle.
  - `compaction-strategy-ui-01KQ8TDI` for `Settings.CompactionModel` and the model-dispatch path used by reintegration summarization.
- **Soft deps**:
  - `cedar-policy-editor-ui-01KQ8TD6` — tool-grant "Advanced (custom Cedar policy id)" option only renders when the editor is shipped.
  - `provider-implementation-uniformity-01KQ8V4F` — branch model picker reuses the same provider/profile capability machinery.

## Architecture

### 1. Heuristic Detector (`core/branchadvisor/`)
- File: `core/branchadvisor/detector.go` exposes `Detect(message string, minConfidence float64) *BranchSuggestion`.
- File: `core/branchadvisor/signals.go` declares two slices of compiled `*regexp.Regexp` patterns:
  - `positiveSignals` (each match contributes +1 to `signal_count`): `(?i)\balso\s+(figure\s+out|do|check|look\s+at)`, `(?i)\bwhile\s+you'?re\s+at\s+it\b`, `(?i)\bcan\s+you\s+also\b`, `(?i)\bside\s+(question|task|note)\b`, `(?i)\btangent\b`, `(?i)\bas\s+a\s+one[- ]off\b`, `(?i)\bin\s+parallel\b`, `(?i)\bbefore\s+that,?\s+let\s+me\s+know\b`, `(?i)\bquick\s+aside\b`, `(?i)\bunrelated\s+but\b`.
  - `negativeSignals` (each match contributes +1 to `noise_count`, dampening confidence to avoid false positives where the user is *replacing* the request rather than adding to it): `(?i)\bactually\b`, `(?i)\binstead\b`, `(?i)\blet\s+me\s+clarify\b`, `(?i)\bnevermind\b`, `(?i)\bscratch\s+that\b`, `(?i)\bignore\s+that\b`.
- Confidence formula: `confidence = signal_count / max(signal_count + noise_count, 1)`. Returns nil if `signal_count == 0` OR `confidence < minConfidence`.
- `Rationale` constructed from the matched positive-signal labels (so banner tooltip can show "What signals were detected?" per FR-011).
- `ProposedTitle` = first 40 chars of message (trimmed at last whitespace boundary).
- Pure Go, no IO, < 5ms per message (NFR-001). All regex compiled once at package init.

### 2. Suggestion Banner (frontend)
- Component: `frontend/src/components/chat/BranchSuggestionBanner.vue`.
- Mount point: above the most recent user message bubble in chat scrollback, rendered by `frontend/src/components/chat/ChatInput.vue` (or its parent message-list view) when the message has an attached `branchSuggestion` payload.
- Delivery channel: per DIRECTIVE_001 + spec C-004, the suggestion rides alongside the user message echo on the existing chat-runner broker channel — no new RPC. Backend computes `Detect` synchronously when handling the user-turn submission and attaches the `BranchSuggestion` (or nil) into the echoed message metadata.
- Buttons:
  - `[Branch it off]` -> opens Context-Pick modal (section 4).
  - `[No thanks]` -> dismiss for this message only; emits `KindBranchAdvisorDismissed{scope:"message", reason:"no_thanks"}`.
  - `[Don't suggest again]` -> sets session metadata `branch_advisor_dismissed = true`; emits `KindBranchAdvisorDismissed{scope:"session", reason:"dont_suggest_again"}`.
- Race guard: banner debounces 800ms — if the user re-sends or types again within that window, banner suppresses to avoid racing the user's send (NFR-004).

### 3. Confidence Threshold + Per-Session/Project Overrides
- Setting: `Settings.BranchAdvisorMinConfidence float64` default **0.85** (locked Q29.1; FR-010 amended to 0.85).
- Per-session dismiss: stored in session metadata as `branch_advisor_dismissed bool`; checked before backend computes detection.
- Per-project override: `project.settings.branch_advisor` enum `"default" | "always_on" | "always_off"` set via project-settings UI (lives next to existing project metadata flags).
- Resolution order at suggestion time (backend, in `chat-runner` user-turn handler):
  1. If `HARNESS_BRANCH_ADVISOR` env disabled -> nil.
  2. If `project.settings.branch_advisor == "always_off"` -> nil.
  3. If `project.settings.branch_advisor == "always_on"` -> threshold = 0 (any signal fires).
  4. Else if `session.branch_advisor_dismissed` -> nil.
  5. Else threshold = `Settings.BranchAdvisorMinConfidence`.

### 4. Context-Pick Modal
- Component: `frontend/src/components/chat/BranchContextPickModal.vue`.
- Triggered by `[Branch it off]`. Fields:
  - **Workflow title** (text input, default = `BranchSuggestion.ProposedTitle`).
  - **Branch model picker** (provider/profile dropdown reusing the shared model selector; default = `Settings.BranchAdvisorDefaultModel` which itself defaults to `Settings.CompactionModel`).
  - **Context-pick checklist** with default-checked state:
    - "Last 4 turns" (checked).
    - "Pinned memories" (checked).
    - "Attached artifacts" (unchecked).
    - "System prompt" (unchecked).
  - **Tool grants** dropdown:
    - "Inherit from parent" (default).
    - "Read-only tools only".
    - "No tools".
    - "Advanced (custom Cedar policy id)" — only rendered when cedar-policy-editor-ui is shipped.
  - `[Submit]` / `[Cancel]`.

### 5. Branch Session Creation
- On Submit, frontend calls upstream `Sessions.CreateExplicitBranch` (from branching-ux-polish) with the selected context items, picked model, and tool-grant policy id.
- Backend wrapper `Branches.CreateSubagentBranch` (thin layer over `CreateExplicitBranch`) enriches the new session metadata:
  - `subagent_branch: true`
  - `branch_recommendation_id: <ulid>` (matches `BranchSuggestion.ID`)
  - `branch_advisor_signals: [...]` (the triggered positive-signal labels)
  - `parent_session_id`, `parent_message_id` (already provided by upstream)
- Emits `KindBranchAdvisorAccepted{confidence, branch_session_id, recommendation_id}`.
- Branch session opens in the sidebar nested under parent (per branching-ux-polish sidebar tree).

### 6. "Bring Back to Parent" Button
- Rendered at the top of every session where `metadata.subagent_branch == true` (rendered in `frontend/src/views/sessions/SessionsView.vue` as a sticky chip alongside the existing branching-ux-polish breadcrumb).
- On click: opens the Reintegration Preview Modal (section 7).

### 7. Reintegration Preview Modal
- Component: `frontend/src/views/sessions/ReintegrationPreviewModal.vue`.
- Flow:
  1. Modal opens in a "Generating..." state and calls `Branches.ProposeReintegrationSummary(branchSessionID)`.
  2. Backend (in `core/branches/reintegration.go`):
     - Loads branch transcript (all messages + tool-output records).
     - Builds prompt: `"You are summarizing a side-conversation that happened in a branch session. Produce a summary as long as needed to faithfully capture facts, decisions, tool outputs, and any artifacts produced. Do not truncate. Use multi-paragraph structure with headings when the branch covers multiple topics. Token budget: up to <BranchReintegrationMaxTokens> output tokens — use only what the content warrants. Short branches deserve short summaries; deep branches deserve thorough ones."` + transcript.
     - Dispatches via `Settings.CompactionModel` (reusing compaction-strategy-ui's summarization plumbing).
     - Returns `BranchReintegrationProposal{ ProposedSummary string, TokenCount int, Model string, ArtifactRefs []ArtifactRef }`.
     - Empty-branch safeguard: if the branch has zero user/assistant turns, returns `ProposedSummary == ""` and the modal switches to a "discard branch" affordance.
  3. Modal renders an editable `<textarea>` pre-filled with `ProposedSummary`, plus buttons `[Insert into parent]`, `[Regenerate]`, `[Cancel]`.
  4. **Insert** -> `Branches.CommitReintegration(branchSessionID, finalSummaryText, CommitOpts{WasEdited: bool})`:
     - Inserts a synthetic system message into the parent session: `{ role: "system", content: finalSummaryText, source_branch_id: <id>, branch_artifact_ids: [...], synthetic_kind: "branch_reintegration" }`.
     - Parent renders this with a "Brought back from branch: <title>" badge that links to the source branch (FR-009).
     - Attaches branch-produced artifacts to the parent's artifact pool with provenance fields `{produced_in_session: <branch_id>, reintegrated_via: <commit_id>}`.
     - Emits `KindBranchReintegrated{parent_session_id, branch_session_id, summary_token_count, was_edited}`.
     - Branch session is **not** deleted; transcript remains accessible per the "Show full history" toggle.
  5. **Regenerate** -> calls `ProposeReintegrationSummary` again (same model; future enhancement could nudge temperature).
  6. **Cancel** -> closes modal; branch session stays open and editable.
- Concurrency: per-branch `sync.Mutex` keyed by `branch_session_id` serializes propose/commit and any concurrent `Sessions.AppendContext` from FR-015.

### 8. Settings Dials
- `Settings.BranchAdvisorMinConfidence float64` — default **0.85** (locked Q29.1).
- `Settings.BranchAdvisorUseLLM bool` — default `false` (FR-013, field reserved; no implementation in v1).
- `Settings.BranchAutoMode bool` — default `false` (FR-014, field reserved; no implementation in v1).
- `Settings.BranchReintegrationMaxTokens int` — default **2000**, min 500, max 16000 (locked Q29.2 / FR-008a).
- `Settings.BranchAdvisorDefaultModel ProviderProfileRef` — default = `Settings.CompactionModel` (which itself defaults to active session's model).
- All dials surfaced in the existing Settings panel under a new "Branch Advisor" section.

### 9. Audit Emission
Four new kinds added to `core/context/audit/audit.go`:
- `KindBranchAdvisorSuggested` — payload: `{ confidence, signals, message_id, session_id, recommendation_id }`. Emitted when `Detect` returns non-nil.
- `KindBranchAdvisorAccepted` — payload: `{ confidence, branch_session_id, recommendation_id }`. Emitted when user submits the context-pick modal.
- `KindBranchAdvisorDismissed` — payload: `{ scope: "message" | "session" | "project", reason: "no_thanks" | "dont_suggest_again" | "project_off", recommendation_id? }`.
- `KindBranchReintegrated` — payload: `{ parent_session_id, branch_session_id, summary_token_count, was_edited }`.

### 10. "Pull from Parent" Reverse Path (FR-015)
- Component: `frontend/src/views/sessions/PullFromParentModal.vue`.
- Button visible inside subagent-branch sessions next to "Bring back to parent".
- Opens a context-pick UI listing parent turns / pinned memories / artifacts not yet in the branch.
- Calls `Sessions.AppendContext(branchSessionID, contextItems)` which appends as additional context items (not as a fork) to the branch session.
- Lower priority than main flow; can be deferred to follow-up if scope creeps. Tracked in WP09 as optional.

### 11. Cross-Mission Seams
- **branching-ux-polish-01KQ8TDB**: hard read on `Sessions.CreateExplicitBranch`, the sidebar tree nesting (subagent branches must appear nested under parent), the breadcrumb, and the "Show full history" toggle (must continue to expose the branch transcript post-reintegration).
- **compaction-strategy-ui-01KQ8TDI**: hard read on `Settings.CompactionModel` and the underlying summarization dispatch helper. Reintegration is a separate codepath but reuses the same model-routing infrastructure.
- **cedar-policy-editor-ui-01KQ8TD6**: optional read on the policy-id selector for "Advanced (custom Cedar policy id)"; UI hides this option when the editor is not yet shipped.
- **provider-implementation-uniformity-01KQ8V4F**: reuses the shared provider/profile capability machinery for the branch model picker.

## Risk Register

| Risk | Mitigation |
|---|---|
| False positives even at 0.85 threshold (banner blindness) | Track dismissal rate via `KindBranchAdvisorDismissed`. If session-scoped dismissal rate >30% across active users, raise threshold globally to 0.9 or trim positive-signal list. |
| Reintegration summary loses critical context | Preview-before-insert modal (FR-008b) lets user edit verbatim. Branch transcript stays accessible via "Show full history" so the parent can always re-inspect. |
| Summary too long for parent context window | `BranchReintegrationMaxTokens` ceiling clamps output; if model exhausts budget mid-thought, output ends with `[summary continues — see full branch transcript for the rest]` marker. |
| Branch with zero user/assistant turns | Propose returns empty `ProposedSummary`; modal switches to "Discard branch" affordance instead of insert. |
| Branch model fails mid-summarization | Backend returns typed error; modal renders error banner with `[Retry]` button (re-invokes propose). |
| Pull-from-parent + reintegration race on same branch | Per-branch `sync.Mutex` keyed by `branch_session_id` serializes both operations. |
| Cost attribution confusion (parent vs branch spend) | Each branch's spend tracked separately, surfaced as parent's "Branch overhead" cost line per FR-012. |
| Banner racing with the user's send keystroke | 800ms debounce — banner only mounts if user hasn't sent within that window. |
| Cedar policy editor not yet shipped when this mission lands | Tool-grant dropdown's "Advanced (custom Cedar policy id)" hidden when editor capability flag absent; three preset options remain functional. |

## Rollout

- **Feature flag**: `HARNESS_BRANCH_ADVISOR` env var, default ON. When OFF, detector short-circuits to nil and banner never mounts.
- **Acceptance smoke (5-step manual checklist)**:
  1. Type a clear-side-task message ("great, while you're at it can you also figure out X"). Banner appears.
  2. Click `[Branch it off]`. Context-pick modal opens with workflow title pre-filled, last-4-turns and pinned-memories pre-checked, branch model = compaction model.
  3. Submit. New branch session opens nested under parent in sidebar; metadata shows `subagent_branch: true` and `branch_advisor_signals` populated.
  4. Do work in the branch (a few user/assistant turns + maybe an artifact). Click `[Bring back to parent]`. Preview modal shows a multi-paragraph summary (with headings if multi-topic). Edit a minor detail. Click `[Insert into parent]`.
  5. Switch to parent session. Confirm new "Brought back from branch: <title>" message renders with badge linking to the branch. Click "Show full history" -> branch transcript still accessible. Audit log shows all four kinds emitted in the right order.
