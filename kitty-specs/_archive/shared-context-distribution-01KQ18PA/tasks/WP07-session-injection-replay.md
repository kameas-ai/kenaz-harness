---
work_package_id: "WP07"
title: "Session-time injection hook and replay against pinned versions"
dependencies:
  - "WP06"
  - "WP05"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 7 - Injection hook + replay"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP07 – Session injection hook + replay

## Goal

Implement `core/context/inject/` (the single declarative session-start hook) and `core/context/replay/` (snapshot reconstruction from event log + lockfile). This is how every agent session sees shared context, and how a 30-day-old session reproduces its exact context. The injection point is the same regardless of which layers contributed (FR-010 invariant).

## Spec references

- FR-010 (Single declarative injection hook into agent context)
- FR-012 (Session-time audit event records snapshot id consumed)
- FR-018 (Replay against pinned pack versions)
- SC-001 (Reproduce a session's resolved context from event log alone)
- SC-005 (30-day-later replay byte-identical)
- Assumption: session-start only — no mid-session mutation

## Plan references

- §3 (`Inject(ctx, sessionID, snap, scope) → EventID`, `Replay(ctx, sessionID) → ResolutionSnapshot`)
- §4.6 (Snapshot store + replay fallback path: store first, lockfile-pinned re-resolution second)
- §4.8 (Session-time injector — single hook called by `core/session/`)
- §6 (Workflow engine / `core/session/` consumes only `ContextResolver.Inject`)
- Risk R2 (re-check access policy at injection time, not just resolution time)

## Subtasks

- T001 Implement `core/context/inject/inject.go`: takes a `ResolutionSnapshot` and shapes it into the in-memory representation the LLM connector / agent expects (system-message-style blocks plus structured skill/guidance metadata).
- T002 Re-check access policy at injection time per Risk R2 — if the operator's role changed since resolution, abort injection and emit `scope_revoked`.
- T003 Implement `core/context/replay/replay.go`: given a session id, query the event log for the recorded snapshot id, look up via the snapshot store (WP06); fall back to re-resolving from the lockfile-pinned pack versions in the event log entries.
- T004 Wire `ContextResolver.Inject` and `ContextResolver.Replay` as the single public entry points consumed by `core/session/` and the workflow engine.
- T005 Integration test: record a session, age the snapshot store-only fallback paths (delete snapshot row), replay reconstructs from lockfile + event log to a byte-identical resolved context.

## Acceptance criteria

- Injection hook is the single point where agent context receives pack content (FR-010 invariant verified by static check).
- Replay returns byte-identical snapshot from store path *and* from event-log + lockfile fallback path (validates SC-005).
- Access-policy re-check at injection time blocks ejection of pack content when role changed (Risk R2).
- Injection produces an `injection_emitted` event candidate (audit emission proper lands in WP10).

## Files to create/modify

- `core/context/inject/inject.go`
- `core/context/replay/replay.go`
- `core/context/resolver.go` (orchestrating `Resolve`/`Inject`/`Replay`)
- `core/context/inject_test.go`
- `core/context/replay_test.go`

## Definition of done

- Replay byte-identity verified against stored snapshot and against fallback path.
- No injection path other than the single declarative hook.
- WP merged to main via squash-merge PR.
