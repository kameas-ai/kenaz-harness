# Branching UX Polish — Rollout Guide

Mission: `branching-ux-polish-01KQ8TD7`

## Background

The branching infrastructure landed in prior missions (session branch tables, `branches` RPC,
implicit edit-and-resend fork via `core/agentgraph/branch_seam.go`), but the product
surface was invisible: no sidebar tree, no breadcrumb, no direct "branch here" affordance.
This mission wires the four user-visible layers:

1. **Sidebar branch tree** — sessions that are children of another session render indented
   and collapsible under their parent in the left rail.
2. **Branch breadcrumb** — a slim strip at the top of the chat panel shows "Branch from
   turn N of &lt;parent&gt;" for any branch session; clicking the parent title navigates there.
3. **"Branch from this turn" button** — assistant message bubbles expose a ⎇ button that
   calls `branches.createExplicit` and navigates to the new child session.
4. **Display-meta columns** — migration 0322 adds `parent_message_id`, `branch_title`,
   `creation_path`, and `parent_session_title` to the `branches` table so the breadcrumb
   and sidebar can render without extra round-trips.

## Status

| Work Package | Title | Status |
|---|---|---|
| WP01 | Migration 0322 — branch display meta columns | Merged |
| WP02 | `CreateBranchAtMessage` + `ListBranchesByProject` + `ListWithBranchTree` | Merged |
| WP03 | Sidebar branch tree (`SessionTreeRow.vue`) + drag compat | Merged |
| WP04 | `BranchBreadcrumb` component + `SessionsView` mount | Merged |
| WP05 | "Branch from this turn" button on `MessageBubble` | Merged |
| WP06 | `HarnessClient.branches.createExplicit` frontend wire | Merged |
| WP07 | Integration tests + this acceptance doc | **This branch** |

## Feature Flag

`HARNESS_BRANCHING_POLISH` — defaults **on**. Override at runtime via the
`harness.feature.branchingPolish` localStorage key (set to `"off"` to disable).
When off:

- Sidebar renders flat (existing behaviour).
- `BranchBreadcrumb` returns null.
- "Branch from this turn" button is hidden.
- `branches.createExplicit` RPC still works so tests can exercise the path.

## Architecture

### Storage (WP01)

Migration `0322` (in `core/storage/sqlite/migrations/`) adds four columns to `branches`:

| Column | Type | Backfill | Purpose |
|---|---|---|---|
| `parent_message_id` | `TEXT NOT NULL DEFAULT ''` | `''` | Anchor for "turn N" breadcrumb |
| `branch_title` | `TEXT NOT NULL DEFAULT ''` | `''` | Display override in sidebar |
| `creation_path` | `TEXT NOT NULL DEFAULT 'unknown'` | `'unknown'` | `'explicit'` or `'edit_resend'` |
| `parent_session_title` | `TEXT NOT NULL DEFAULT ''` | `''` | Snapshot of parent name for deleted-parent fallback |

### Backend (WP02)

`core/conversation.Manager` gains:

- `CreateBranchAtMessage(ctx, ForkAtMessageOptions)` — validates the anchor message exists
  in the parent, copies messages `[0..anchor]` into a new child session, persists the
  branch row with `CreationPath="explicit"`.
- `ListBranchesByProject(ctx, projectID)` — aggregate query; used by sidebar and tests.
- `DeleteChildrenOf(ctx, parentSessionID)` — cascade-delete helper.

`core/rpc/views/branches.BranchesAPI` gains:

- `CreateExplicit(ctx, ExplicitBranchOptions)` — thin wrapper over `CreateBranchAtMessage`.
- `ListWithBranchTree(ctx, projectID)` — flat list with parent pointers.

`core/agentgraph/BranchSeamAdapter.Fork` sets `CreationPath="edit_resend"` on the branch
row for the implicit edit-and-resend path.

### Frontend (WP03–WP06)

| Component | Location | Change |
|---|---|---|
| `SessionTreeRow.vue` | `frontend/src/shell/` | New recursive sidebar row with chevron, indent, GitBranch icon, drag-compat (`data-session-id` attr) |
| `LeftRail.vue` | `frontend/src/shell/` | `branchCollapsed` ref, `sessionsByParent` computed, `onTreeSessionDragStart` with native DOM attr fallback |
| `BranchBreadcrumb.vue` | `frontend/src/components/chat/` | Slim breadcrumb strip; hidden for root sessions; "Branch from turn N of &lt;parent&gt;" |
| `MessageBubble.vue` | `frontend/src/components/chat/` | ⎇ button on assistant bubbles; disabled while streaming; emits `branch-from-turn` |
| `MessageList.vue` | `frontend/src/components/chat/` | Bubbles `branch-from-turn` up to parent |
| `SessionsView.vue` | `frontend/src/views/sessions/` | Handles `branch-from-turn`, calls `createExplicit`, navigates to child; mounts `BranchBreadcrumb` |

## Acceptance Criteria

### Automated

All gates green on `feat/branching-ux-polish-v0.5.0-retry`:

- `go test ./...` — includes `core/conversation/...` (integration round-trip, cascade
  delete, implicit fork creation path) and `core/rpc/views/agentgraph/...`
  (`BranchSeamAdapter.Fork` sets `creation_path="edit_resend"`).
- `cd frontend && pnpm test` — 789 tests passing, including:
  - `BranchBreadcrumb.test.ts` (6 tests)
  - `MessageBubble.branch.test.ts` (6 tests)
  - `LeftRail.tree.test.ts` (4 tests)
  - `SessionsView.branching.test.ts` (3 tests)

### Manual Acceptance Smoke (5 steps)

Operator runs this before merging to `main`. Record pass/fail for each step.

**Pre-condition**: app launched with default settings; `HARNESS_BRANCHING_POLISH` flag on
(default); at least one LLM provider configured.

---

**Step 1 — Baseline turn sequence**

1. Create a new session named "alpha".
2. Send 5 user messages and receive 5 assistant responses.
3. Verify sidebar shows "alpha" as a top-level row with no chevron and no indented children.

Pass: row renders flat, no branch indicators.

---

**Step 2 — Branch from turn 3**

1. Hover the assistant response on turn 3.
2. Click the ⎇ "Branch from this turn" button in the message action strip.
3. Confirm a new session opens immediately (URL changes to `/sessions/<child-id>`).
4. Confirm the new session's chat history contains exactly the first 3 turns from "alpha"
   (user + assistant pairs for turns 1–3).

Pass: navigation succeeds; child message list is correct; no console errors.

---

**Step 3 — Breadcrumb**

1. While on the child session from Step 2, confirm the breadcrumb strip reads
   "Branch from turn 3 of alpha".
2. Click the "alpha" link in the breadcrumb.
3. Confirm navigation returns to the parent session.

Pass: breadcrumb text correct; click navigates to parent; no page reload.

---

**Step 4 — Sidebar tree collapse / expand**

1. Return to the sidebar.
2. Confirm "alpha" now shows a chevron (▸/▾) and the child session is indented below it.
3. Click the chevron → child collapses (hidden).
4. Click the chevron again → child expands (visible).
5. Reload the app; confirm collapse state is preserved (persisted in localStorage under
   `harness.sidebar.branchCollapsed.v1`).

Pass: chevron toggle works; state survives reload.

---

**Step 5 — Implicit edit-and-resend + branch delete**

1. On the parent "alpha", edit the user message on turn 4 and resend (existing implicit path).
2. Confirm a second child session appears as a nested sibling under "alpha" in the sidebar.
3. Right-click the **first** child session → "Delete session".
4. Confirm only the second child remains under "alpha"; parent "alpha" still has one child
   with a chevron.
5. Confirm no console errors and no spurious navigation occurred.

Pass: both creation paths produce sidebar rows; delete removes only the targeted branch;
`KindBranchCreated` audit events visible in the audit view for both paths (optional: check
audit panel if the UI surface is exposed in this build).

---

**Feature flag off path**

With `localStorage.setItem('harness.feature.branchingPolish', 'off')` and page reload:

- Sidebar renders flat (no chevrons, no tree indentation).
- BranchBreadcrumb absent from chat header.
- ⎇ button absent from all message bubbles.

Pass: no branching UX surfaces appear; all other features unaffected.
