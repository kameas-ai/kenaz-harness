# Spec: Branching / forking UX polish

**Status**: draft · **Owner**: alecfeeman

## 1. Why

The branching feature exists in code (BranchSeam, ForkRequest, branches table, BranchSidebar). The UX path — when does the compact-handoff fire, when does the merge suggestion appear, what does the user see when a branch parent updates — needs review. This mission is a UX-only audit + polish pass; no new core capability.

## 2. Goals

- Audit every branching surface (sidebar, create-branch modal, merge-suggestion toast, fork events).
- Identify and fix UX gaps: ambiguous states, missing affordances, dead-end flows.
- Document the canonical user flow for branching in `docs/branching-ux.md`.

## 3. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | Audit doc enumerates every branching event (fork, merge-suggested, parent-updated, child-completed) with the current UX response. | proposed |
| FR-002 | Compact-handoff prompt clarity: when a branch is forked, the user sees a clear "compact handoff" inline message in the child explaining what was carried over. | proposed |
| FR-003 | Merge-suggestion toast appears within 5 s of the agent producing a "ready to merge" signal. | proposed |
| FR-004 | BranchSidebar shows parent-update notifications (a project moved on after the branch forked). | proposed |
| FR-005 | Fork operation is undoable for 30 s via a toast. | proposed |
| FR-006 | Cross-branch artifact pinning surfaces in both branches' Artifacts tabs. | proposed |

## 4. Success criteria

- New-user usability test: ≥ 80% complete a fork → work → merge cycle without help on first attempt.
