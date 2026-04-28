---
work_package_id: "WP06"
title: "Scoped long-term memory — global / project / session"
dependencies:
  - "WP02"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 6 — Scoped memory"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-26T01:55:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP06 — Scoped long-term memory

## Goal

Wire the scope dimension into the memory store, retriever, and pin UI. Memory chunks gain `scope_kind` + `scope_id`. Pin defaults to **session** scope; the user can promote to project / global. Retrieval at send time queries the union of `global ∪ project:<this> ∪ session:<this>`.

This WP can land in **parallel with WP04** — only depends on WP02 (Project entity).

## Spec references

- Spec: §3 US12 / US13 / US14 / US15, §4 FR-301 / FR-302 / FR-303 / FR-304, §5 NFR-005 / NFR-006.
- Plan: § "Phase 6".

## Prerequisites

WP02 merged.

## Subtasks

- **T001 — `core/memory/chunk.go` + store update.** Add `ScopeKind` + `ScopeID` fields to `Chunk`. The chromem-go-replacement gob format gains optional fields; reading back an old gob without these fields defaults `ScopeKind="session"` + `ScopeID=Chunk.SessionID` (so existing chunks keep their session scoping). Update `Add / Delete / List / Query` to accept + filter on scope.
- **T002 — Retriever.** `core/memory/retriever.go` query union: `WHERE scope_kind='global' OR (scope_kind='project' AND scope_id=session.project_id) OR (scope_kind='session' AND scope_id=session.id)`. Top-k by similarity over the union; threshold + dedup unchanged.
- **T003 — Memory builtin hooks.** `core/hooks/memory_builtins.go memory.persist` defaults to session scope; allow `Hook.Config.default_scope: "global"|"project"|"session"`. `memory.retrieve` reads the active session's `project_id` (via the existing SessionContextReader-equivalent surface) and runs the union query.
- **T004 — Memory view + bindings.** `core/rpc/views/memory/api.go + impl.go` — `RememberMessage(sessionID, messageID, scope)` (3rd arg added), `PromoteScope(chunkID, newScopeKind, newScopeID)` (move semantics: delete original, insert new), `ListChunks(filter)` gains scope filter. Bindings `Memory_RememberMessage`, `Memory_PromoteScope`, `Memory_ListChunks` updated.
- **T005 — Frontend.**
  - `frontend/src/components/chat/MessageBubble.vue` — pin button gains a small dropdown with "Pin to session" (default), "Pin to project" (when applicable), "Pin to global". Right-click + long-press both open the menu.
  - `frontend/src/views/memory/MemoryView.vue` — scope filter pill row at the top. Each row gains "Promote scope" + "Forget at scope" actions.

## Acceptance

- A4 (promote session→project chunk; sister sessions retrieve, others don't).
- A5 (global memory retrievable everywhere).
- Promote semantics: original session-scope row deleted, new project-scope row inserted (move, not copy). Verified via test.
- Existing pinned chunks pre-WP06 default to `ScopeKind="session"` and continue to work — verified by a fixture test loading a v1 gob file.
- `go test -race -count=1 ./core/memory/...` covers the new union query + scope filtering.

## Branch strategy

Branch `wp06-scoped-memory` off `main`, merge when WP06 acceptance gate passes.
