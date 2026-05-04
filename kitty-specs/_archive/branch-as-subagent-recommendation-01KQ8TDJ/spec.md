# Spec — Branch as interactive sub-agent + reintegration (`branch-as-subagent-recommendation-01KQ8TDJ`)

**Status**: draft · **Owner**: alecfeeman

## 1. Why

Today, a session branch is a passive artifact: the user edits a past turn or clicks "Branch from here" and gets a fresh divergent timeline. The user has to manually decide *when* to branch and manually decide *whether* to bring the branch's findings back into the parent.

The user has identified a richer mental model: **branches as interactive sub-agents**. Concretely:

1. **Recommendation**: when the user proposes a sub-problem mid-conversation ("can you also figure out X?"), the harness should proactively suggest a branch — "this looks like a contained side-task; want me to spawn a branch, work on it, and bring back a summary?" — for cost / efficiency / focus reasons.
2. **Delegation**: a branch isn't just a forked timeline — it's a self-contained workspace with its own (possibly cheaper) model, its own tool grants, its own context window. The user can pin specific facts/inputs from the parent into the branch, run the branch to a conclusion, and...
3. **Reintegration**: ...bring the branch's *output* back into the parent as a single condensed summary message. The branch's full transcript stays accessible (already covered by `branching-ux-polish`); the parent only receives the *result*, not the noise.

This pattern echoes how skilled humans work: when a sub-question pops up mid-thought, you scratch-paper it, solve it, and write down the answer — you don't dump every intermediate scribble back into the main document. The current spec ranking has `branching-ux-polish-01KQ8TDB` at #22 covering passive branch UX; this mission complements it with the *active* surface.

## 2. Goals

- Inline detection: the harness recognizes when a user message contains a sub-task that's a good branching candidate, and surfaces a non-blocking suggestion banner.
- Explicit user gesture (always opt-in): "Branch this off" affordance turns the highlighted sub-task into a new branch, pre-seeded with relevant parent context.
- The branch is a real session — same chat surface, but rendered with branch indicators and a sticky "Bring back to parent" button at the top.
- Reintegration: when the user completes the branch work, the harness summarizes the branch's outcome (using the configured compaction model from `compaction-strategy-ui-01KQ8TDI`) and inserts it into the parent timeline as a single `[Branch summary: ...]` system-flagged message linking to the source branch.
- The mechanism reuses existing primitives: branching infrastructure from `branching-ux-polish`, compaction summarization from `compaction-strategy-ui`, the artifact pipeline for any deliverables produced inside the branch.

## 3. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | New `core/branchadvisor/` package with `Detect(message)` returning `BranchSuggestion{Confidence, Rationale, ProposedTitle, ProposedContextPick[]}` or nil. Pure heuristic (no LLM call) v1: regex/token-pattern matching for common sub-task signals: "also figure out", "while you're at it", "can you also", "side question", "tangent", "before that, let me know". | proposed |
| FR-002 | `BranchSuggestion` rendered as a non-blocking inline banner above the user's message bubble: "This looks like a side task. [Branch it off] [No thanks] [Don't suggest again]". The "Don't suggest again" sets a per-session-or-project preference. | proposed |
| FR-003 | "Branch it off" action: copies parent history up through the previous turn into a new branch session (tagged `session.kind = "subagent_branch"`, `session.metadata.parent_session_id = ...`, `session.metadata.parent_message_id = <message_being_branched_from>`). The user's flagged message becomes the *first turn* in the branch. | proposed |
| FR-004 | Branch sessions have a configurable model — defaults to a cheaper-than-parent model per the compaction-model picker pattern. User can override at branch-creation time. Rationale: "do this side-task on Haiku, not on Opus." | proposed |
| FR-005 | Branch sessions have a configurable tool-grant scope — defaults to "inherit from parent" but can be tightened ("no filesystem-full for this side task"). Cedar policy gate enforces. | proposed |
| FR-006 | Context-pick UI: when creating a branch, the user sees a checklist of "candidate context items" (last N turns, pinned memories, attached artifacts) and ticks which to seed the branch with. Default = "last 4 turns + pinned memories" — keeps branch lean. | proposed |
| FR-007 | "Bring back to parent" button visible at the top of every branch session. Click triggers the reintegration flow. | proposed |
| FR-008 | Reintegration flow: (1) calls compaction model to summarize the branch's full conversation. The summary is **length-flexible** — NOT constrained to a single paragraph. The model is prompted: "Produce a summary as long as needed to faithfully capture the branch's facts, decisions, tool outputs, and any artifacts produced. Do not truncate. Use multi-paragraph structure with headings when the branch covers multiple topics." Default token budget `Settings.BranchReintegrationMaxTokens = 2000` (configurable; meant to be generous, not stingy). (2) **Preview-before-insert modal**: shows the proposed summary in an editable view; user reviews, edits, or rejects; on confirm, inserts. (3) appends to parent session as a synthetic message `{role: system, content: "[Branch summary from <branch_title>]: <summary>", source_branch_id: <branch_id>, branch_artifact_ids: [...]}` with link to the source branch. (4) attaches any artifacts produced during the branch to the parent session's artifact pool with provenance. (5) emits `KindBranchReintegrated` audit event. The full branch transcript stays accessible (per `branching-ux-polish-01KQ8TDB`'s "Show full history" toggle); reintegration is non-destructive. | proposed |
| FR-008a | `Settings.BranchReintegrationMaxTokens int` (default 2000, min 500, max 16000). Caps the summary length the model is instructed to produce. The instruction is "use up to N tokens if the branch warrants it" not "produce exactly N tokens" — short branches get short summaries. | proposed |
| FR-008b | Reintegration preview modal: editable textarea pre-filled with the model's summary. Buttons: `[Insert into parent]`, `[Regenerate]` (re-runs the summarization), `[Cancel]` (closes modal, branch session stays as-is, no insertion). User edits to the summary persist into the inserted message verbatim. | proposed |
| FR-009 | Parent session renders the reintegration message with a distinct visual treatment ("Brought back from branch: <title>" badge with a chevron to expand the full branch transcript inline OR jump to the branch session). | proposed |
| FR-010 | Recommendation system uses **a confidence threshold** (`Settings.BranchAdvisorMinConfidence` default 0.7); below threshold = no banner. Per-session "hide all suggestions" toggle. Per-project "always-on" / "always-off" overrides Settings default. | proposed |
| FR-011 | Recommendation explanations: each suggestion has a `Rationale` field surfaced in the banner ("looks like a side question — branching keeps your main thread focused and saves context"). User can hover for "What signals were detected?" tooltip showing the regex/token matches. | proposed |
| FR-012 | Cost telemetry: each branch's spend is tracked separately and shown in the parent's "Branch overhead" cost line ("Spent $0.04 across 3 branches this session"). | proposed |
| FR-013 | LLM-based recommendation upgrade (optional, behind `Settings.BranchAdvisorUseLLM bool` default false): instead of (or alongside) heuristic detection, run a tiny LLM call to classify whether the user's message is branchworthy. Adds latency + cost; off by default. | proposed |
| FR-014 | "Auto-branch" mode (advanced, behind `Settings.BranchAutoMode bool` default false): when confidence > some-higher-threshold (default 0.9), the harness auto-creates the branch without asking and inserts a "Auto-branched: see <branch> for the side task" message inline. Power-user feature; default off because it surprises new users. | proposed |
| FR-015 | Reverse path: "Pull from parent" button inside a branch — opens a context-pick UI to import additional turns/memories/artifacts from the parent into the branch's context. Useful when the branch realizes it needs more parent context mid-work. | proposed |

## 4. Non-functional requirements

| ID | Requirement | Threshold |
|---|---|---|
| NFR-001 | Heuristic detector latency | < 5ms per message (pure-Go regex; runs synchronously on every user turn) |
| NFR-002 | LLM-based detector latency (when enabled) | < 500ms; uses smallest available model in the configured profile, runs in parallel with the main turn dispatch (non-blocking — banner appears late if needed) |
| NFR-003 | Reintegration summarization | < 5s wall-clock; same provider/model dispatch path as compaction-strategy |
| NFR-004 | Banner UX | Never blocks message send; user can ignore and the message goes through normally |

## 5. Constraints

| ID | Constraint |
|---|---|
| C-001 | Always opt-in (except `BranchAutoMode`): the harness suggests, the user decides. Default behavior never auto-creates branches. |
| C-002 | Reintegration is reversible: parent's reintegration message can be deleted; branch session persists independently. |
| C-003 | Branch sessions are first-class — same RPC surface, same artifact pipeline, same chat surface. NOT a special UI mode. |
| C-004 | DIRECTIVE_001: frontend learns of suggestions via existing chat broker topic; no new RPC layer for the advisor (suggestions ride alongside the user message echo). |
| C-005 | Cost transparency: branches show their own cost line; the user always knows what each branch cost separately from the parent. |

## 6. Success criteria

- A user types "great, now also figure out X" and the harness banners "branch this off?" with > 70% accuracy (no test-set yet; calibrate during implementation against captured chat history).
- Click "Branch it off" → new branch session opens in < 1s with seeded context.
- Click "Bring back to parent" in branch → parent session shows the new summary message in < 6s.
- Cost telemetry breaks out branch overhead from main session cost.
- User can disable advisor entirely via Settings; banner stops appearing.

## 7. Out of scope (v1)

- Multi-level branching (branch of a branch's branch). Single level only — second-level branches just attach to the topmost parent for reintegration purposes.
- Concurrent branch execution from a single message (fan-out). Sequential for v1.
- Parallel side-by-side branch comparison view (the "diff two branches" UX is a separate concern handled in `branching-ux-polish`).
- Cross-session branch reintegration ("bring this branch into a different session"). Branches stay coupled to their original parent.
- Branch templates ("when you see X kind of side-task, always branch it with Y model"). Future mission.
- The advisor system informing the model of the branching opportunity inline so the model itself proposes branching as part of its turn ("I'd recommend branching this — want me to?"). Cleanest UX but requires deeper model-side integration; deferred.

## 8. Open questions

- **Q1**: Should the heuristic detector run on assistant messages too (so the model can suggest branching to itself)? Lean **no for v1** — keep it user-message-only; assistant-side suggestion is a follow-up.
- **Q2**: Reintegration target — does the summary always land in the parent session, or can the user route it elsewhere (e.g., into a separate "facts" memory)? Lean **parent only** for v1; multi-target reintegration is YAGNI.
- **Q3**: How does this interact with `compaction-strategy-ui`? Branch reintegration uses the configured compaction model; no separate pipeline.
- **Q4**: Should branches inherit pinned memories from the parent automatically, or require explicit context-pick? Lean **explicit** (FR-006 default) — keeps branches lean and the user in control.
