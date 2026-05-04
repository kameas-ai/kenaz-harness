# tasks.md — Branch as Sub-Agent + Recommendation

## Sequencing Diagram

```
WP01 (detector)         ┐
WP08 (settings/audit)   ┼─► WP02 (banner) ──► WP03 (context-pick) ──► WP04 (branch wiring) ─┐
                        │                                                                    │
                        └────────────────► WP05 (propose backend) ──► WP06 (preview modal) ──┼─► WP07 (commit RPC) ──► WP09 (integration tests + acceptance)
                                                                                              │
                                                                          (parallelizable)    │
                                                                                              ┘
```

- **Parallel after WP01+WP08**: WP02 (banner UI) and WP05 (propose backend) can start simultaneously.
- **Parallel after WP04**: WP05 still ongoing while WP06 (preview modal UI) is scaffolded.
- **Sequential**: WP07 must follow WP05+WP06; WP09 last.

## Work Packages

### WP01 — Heuristic detector + signals/noise lists
- **Effort**: M
- **Dependencies**: none
- **Files touched**:
  - `core/branchadvisor/detector.go` (new)
  - `core/branchadvisor/signals.go` (new)
  - `core/branchadvisor/detector_test.go` (new)
- **Acceptance**:
  - `Detect(message, threshold)` returns nil for empty / no-signal / below-threshold messages.
  - Returns populated `BranchSuggestion` with `Confidence`, `Rationale`, `ProposedTitle`, `Signals` for clear positive cases.
  - Negative-signal phrases ("actually", "instead", "scratch that") demonstrably dampen confidence below threshold in the test matrix.
  - Benchmark: `go test -bench=. ./core/branchadvisor/...` shows < 5ms per call on 200-char messages (NFR-001).
  - All regex compiled once at package init (no per-call allocation).

### WP02 — Suggestion banner UI + per-session/project dismiss
- **Effort**: M
- **Dependencies**: WP01, WP08
- **Files touched**:
  - `frontend/src/components/chat/BranchSuggestionBanner.vue` (new)
  - `frontend/src/components/chat/ChatInput.vue` (mount banner above latest user msg)
  - `frontend/src/store/sessions.ts` (or equivalent — wire suggestion payload from broker echo)
  - `core/conversation/manager.go` (attach `BranchSuggestion` to user-message echo)
  - `core/session/types.go` (add `branch_advisor_dismissed` field)
  - `core/projects/types.go` (add `branch_advisor` enum field)
- **Acceptance**:
  - Banner mounts above the user's most recent message when suggestion present.
  - All three buttons emit correct audit kinds + persist correct state (message-only, session-level, project-level resolution order works).
  - 800ms debounce verified: rapid send within window suppresses banner.
  - When `HARNESS_BRANCH_ADVISOR=0`, no banner ever mounts.

### WP03 — Context-pick modal + branch creation flow (frontend)
- **Effort**: M
- **Dependencies**: WP02
- **Files touched**:
  - `frontend/src/components/chat/BranchContextPickModal.vue` (new)
  - `frontend/src/components/shared/ModelPicker.vue` (reuse / extend)
  - `frontend/src/store/branches.ts` (orchestration helper)
- **Acceptance**:
  - Modal opens from `[Branch it off]` with all defaults applied per spec section 4.
  - Branch model picker defaults to compaction model and falls back gracefully when compaction-strategy-ui not present.
  - Tool grants dropdown shows three presets; "Advanced (custom Cedar policy id)" hidden when cedar editor flag absent.
  - Submit calls upstream `Sessions.CreateExplicitBranch` with correct payload; Cancel dismisses cleanly.

### WP04 — Branch session metadata + cheaper-model wiring (backend)
- **Effort**: S
- **Dependencies**: WP03
- **Files touched**:
  - `core/rpc/branches_wiring.go` (add `Branches.CreateSubagentBranch` thin wrapper)
  - `core/conversation/manager.go` (metadata enrichment hook)
  - `core/conversation/types.go` (add `SubagentBranch`, `BranchAdvisorSignals`, `BranchRecommendationID`)
- **Acceptance**:
  - New branch session metadata includes the three new fields populated correctly.
  - Branch session uses the picked model + tool-grant policy id end-to-end.
  - `KindBranchAdvisorAccepted` emitted with confidence + branch_session_id + recommendation_id.
  - Sidebar (from branching-ux-polish) renders the branch nested under parent.

### WP05 — Reintegration summarization backend (Propose RPC)
- **Effort**: M
- **Dependencies**: WP08 (for `BranchReintegrationMaxTokens` setting)
- **Files touched**:
  - `core/branches/reintegration.go` (new)
  - `core/branches/reintegration_test.go` (new)
  - `core/rpc/branches_wiring.go` (expose `Branches.ProposeReintegrationSummary`)
- **Acceptance**:
  - Builds correct prompt with length-flexibility instruction + `BranchReintegrationMaxTokens` interpolated.
  - Dispatches via `Settings.CompactionModel`; reuses compaction-strategy summarization plumbing (no duplicate code).
  - Empty-branch case returns empty `ProposedSummary` (no RPC error).
  - Returns `TokenCount`, `Model`, `ArtifactRefs` populated.
  - Per-branch mutex guards concurrent calls.
  - Latency: < 5s wall-clock on a 20-turn branch (NFR-003) — measured in test.

### WP06 — Reintegration preview modal frontend (Insert/Regenerate/Cancel)
- **Effort**: M
- **Dependencies**: WP04, WP05
- **Files touched**:
  - `frontend/src/views/sessions/ReintegrationPreviewModal.vue` (new)
  - `frontend/src/views/sessions/SessionsView.vue` (sticky "Bring back to parent" chip + modal mount)
  - `frontend/src/store/branches.ts` (propose / commit / regenerate orchestration)
- **Acceptance**:
  - "Bring back to parent" chip visible only when `metadata.subagent_branch == true`.
  - Modal opens in "Generating..." state; renders editable textarea on response.
  - Edits persist verbatim into Insert payload.
  - Regenerate re-invokes propose and replaces the textarea content.
  - Cancel closes modal cleanly; branch stays open and editable.
  - Empty-branch case shows "Discard branch" affordance instead of Insert.
  - Error case (model failure) shows Retry button.

### WP07 — Commit reintegration RPC + parent insertion + artifact provenance
- **Effort**: M
- **Dependencies**: WP06
- **Files touched**:
  - `core/branches/reintegration.go` (extend with commit path)
  - `core/rpc/branches_wiring.go` (expose `Branches.CommitReintegration`)
  - `core/conversation/manager.go` (synthetic system-message insertion)
  - `core/artifacts/provenance.go` (or equivalent — attach provenance fields)
  - `frontend/src/components/chat/MessageBubble.vue` (or equivalent — render "Brought back from branch" badge with chevron link)
- **Acceptance**:
  - Synthetic system message appears in parent session with correct content + `source_branch_id` + `branch_artifact_ids`.
  - Parent renders the badge with chevron / link to source branch (FR-009).
  - Branch artifacts attached to parent's pool with `produced_in_session` and `reintegrated_via` provenance fields.
  - `KindBranchReintegrated` emitted with `was_edited` flag accurate.
  - Branch session NOT deleted; transcript accessible via "Show full history" toggle.
  - Per-branch mutex serializes against any concurrent `Sessions.AppendContext` (FR-015).

### WP08 — Settings dials + audit emission
- **Effort**: S
- **Dependencies**: none (can run in parallel with WP01)
- **Files touched**:
  - `core/config/settings.go` (add five new fields)
  - `core/context/audit/audit.go` (add four new `Kind` constants)
  - `core/rpc/views/settings/api.go` + `impl.go` (expose dials)
  - `frontend/src/views/settings/BranchAdvisorSettings.vue` (new section)
- **Acceptance**:
  - `Settings.BranchAdvisorMinConfidence` default 0.85; bounds-checked.
  - `Settings.BranchReintegrationMaxTokens` default 2000, min 500, max 16000.
  - `Settings.BranchAdvisorDefaultModel` chains default = `Settings.CompactionModel`.
  - `Settings.BranchAdvisorUseLLM` and `Settings.BranchAutoMode` reserved fields exist (default false), not yet wired to behavior.
  - Four audit kinds defined with correct string values; audit-tap test verifies emission points.

### WP09 — Integration tests + acceptance docs + reverse path (FR-015)
- **Effort**: L
- **Dependencies**: WP01–WP08
- **Files touched**:
  - `core/branches/reintegration_integration_test.go` (new)
  - `core/branchadvisor/end_to_end_test.go` (new)
  - `frontend/src/views/sessions/PullFromParentModal.vue` (new — FR-015, optional)
  - `core/rpc/sessions_wiring.go` (add `Sessions.AppendContext` — FR-015, optional)
  - `docs/branch-as-subagent.md` (acceptance smoke checklist)
- **Acceptance**:
  - End-to-end test: detector fires -> banner accepted -> branch created -> work done -> propose -> commit -> parent shows summary, branch transcript intact, all four audit kinds emitted in order.
  - 5-step manual smoke checklist documented and recorded in `docs/branch-as-subagent.md`.
  - FR-015 reverse path either implemented (preferred) or explicitly deferred to a follow-up mission with a tracking note.
  - Feature-flag-off path (`HARNESS_BRANCH_ADVISOR=0`) verified end-to-end: no banner, no detection, parent send unaffected.
  - Dismissal-rate audit query (used to monitor "false positives at 0.85 threshold" risk) documented.
