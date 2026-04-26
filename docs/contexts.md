# Contexts, Projects, and Memory

This document explains the harness's scope hierarchy, the project model,
and the user-visible behaviour each work package of the
`context-library-01KQ3MF1` mission delivered.

## 1. Scope hierarchy

Every piece of "starting context" the harness ships to a model lives at
exactly one of three scopes. The scopes form a strict hierarchy:

```
[ global    ]   ← applies to every session in the harness
   ↓
[ project   ]   ← applies to every session in a project
   ↓
[ session   ]   ← applies to one session
```

When a session opens a stream, the harness resolves the merged stream by
concatenating attachments in scope order:

```
[global..., project..., session..., conversation history...]
```

Loose sessions (sessions not bound to a project) skip the project layer
entirely. Inside each scope, attachments are ordered by their stored
`position` field — drag-and-drop reorder in the UI persists through
`Attachments_Reorder`.

## 2. Project model

A **Project** is a top-level grouping of related sessions. It is the
unit the user thinks in: "the docs-rewrite project", "the personal
finances project". Projects own:

- **Name + description** — editable from the project landing page.
- **Sessions** — drag-and-drop in the rail to attach / detach.
- **Project-scope attachments** — files that get prepended to every
  session in the project (e.g. a brief, a glossary, a "how I want you
  to behave" instruction).
- **Project-scope memory** — pinned snippets that retrieval pulls into
  every session in this project at send-time.

Sessions live under at most one project. Detaching from a project
("Loose") simply removes the project layer from the merged stream;
session-scope attachments and memory are unaffected.

### Deleting a project

The delete-project modal offers two outcomes:

| Cascade checkbox | Sessions | Project-scope attachments |
|---|---|---|
| OFF | become loose (project_id → NULL via `ON DELETE SET NULL`) | snapshots survive on each detached session if the user previously promoted them |
| ON | deleted | deleted |

The two paths are exercised by `TestAPI_Delete_PreservesSessions` (A6)
and `TestAPI_Delete_CascadesSessions` (A7) in
`core/rpc/views/projects/impl_test.go`.

## 3. WP-by-WP user-visible behaviour

| WP | What you see |
|---|---|
| **WP01 — Project entity** | New "Projects" header in the rail; create / rename / delete projects from the right-click menu. |
| **WP02 — Rail grouping** | Sessions visibly nest under their project header. The "Loose" group at the bottom collects sessions outside any project. |
| **WP03 — Attachments backend** | The chat surface still works; under the hood a session's starting context is now an Attachment row at session scope, and `buildMessages` reads via `Attachments_ListResolved`. |
| **WP04 — Scoped attachment UI** | Project landing page grows a "Project context" section with add / refresh / remove / drag-to-reorder. Settings grows a "Global context" section with the same affordances. The chat surface gets a collapsible "Resolved context" panel above the message list. |
| **WP05 — In-place editor + watcher** | `/contexts` lets you edit a markdown file in place; an fsnotify watcher invalidates the tree when an operator drops a file in via Finder. |
| **WP06 — Memory scoping** | The 📌 button asks "remember at which scope?". `/memory` grows scope filter pills (All / Global / Project / Session) and a per-row "Promote scope" action. |
| **WP07 — Polish + integration** (this WP) | Drag-and-drop a session row onto a project header in the rail to move it. Project landing page gains an editable description, a project-scope memory count + deep-link, a sorted-by-last-active sessions table, and a "Start a session in this project" CTA. |

## 4. Acceptance criteria coverage (A1-A10)

The mission spec at `kitty-specs/context-library-01KQ3MF1/spec.md` lists
ten acceptance criteria. Test coverage per criterion:

| # | Criterion | Test(s) |
|---|---|---|
| A1 | Project persistence | `core/projects/projects_test.go` (round-trip), `core/rpc/views/projects/impl_test.go::TestAPI_RoundTrip` |
| A2 | Project attachment applied | `core/attachments/scope_test.go` + `core/rpc/views/llm/integration_test.go::TestIntegration_ResolvedSystemPromptOrdering` |
| A3 | Global attachment applied | same integration test (`global1`/`global2` rows) |
| A4 | Memory promotion | `core/memory/scope_test.go::TestPromoteScope_*` + `core/rpc/views/memory/impl_test.go` |
| A5 | Global memory | `core/memory/scope_test.go` (global scope) + retrieval tests |
| A6 | Delete project preserve sessions | `core/rpc/views/projects/impl_test.go::TestAPI_Delete_PreservesSessions` |
| A7 | Delete project + cascade | `core/rpc/views/projects/impl_test.go::TestAPI_Delete_CascadesSessions` |
| A8 | Resolution panel order | `core/rpc/views/llm/impl_test.go::TestBuildMessages_AttachmentsPrependedInOrder` + `core/rpc/views/llm/integration_test.go` + `frontend/src/views/sessions/__tests__/ResolvedContextPanel.test.ts` |
| A9 | Path-traversal rejected | `core/contexts/library_test.go::TestPathTraversalRejected` |
| A10 | Snapshot survives library deletion | `core/attachments/attachments_test.go` (inline content survives source delete) + `frontend/src/components/contexts/__tests__/AttachmentRow.test.ts` (source-missing UI) |

## 5. Audit events

The mission planned three audit event kinds:

- `project.created`
- `context.attached`
- `memory.scoped`

These are not yet emitted in production. The blocker is that the rpc
layer has not threaded a process-wide `event.Emitter` (with a redaction
pipeline + chain head) through `core/rpc/api.go` — see the
`TODO(audit)` comment in `newLLMStack` in that file. The three events
have `// TODO(audit-wired)` markers at the call sites that should fire
them (`core/projects/projects.go`, `core/attachments/attachments.go`,
`core/memory/store.go`); when an emitter is wired the markers spell
out the exact payload shape to emit.

## 6. Migration ledger

A fresh harness boot runs migrations 0300 (`sessions/0300-init`) and
0301 (`sessions/0301-context-attachments`) in the sessions block
(versions 300-399). The
`core/session/migrations_ledger_test.go::TestBootMigrationLedger_Records300And301`
test guards this — a stale binary with missing migration registration
fails the test before it can silently break the chat path.

## 7. Manual smoke checklist (`wails dev`)

1. Create a project from the rail's "+ project" affordance.
2. Open the project landing page (file-icon button next to the project
   header). Edit the description; save.
3. Click "Start session in this project". The session opens; the rail
   shows it nested under the project.
4. Send a turn. The "Resolved context" panel above the message list
   shows zero contexts.
5. Add a context at project scope (the "Add context" button on the
   landing page). Resolved panel updates on next session refresh; the
   merged system prompt now includes the project file.
6. Add a global attachment via Settings → Global context. Resolved
   panel shows global → project order.
7. Drag the session row onto the "Loose" header. The session moves out
   of the project group; the resolved panel drops the project layer.
8. Drag the row back onto the project header. Project layer reappears.
9. Open `/memory`, click the "Project" filter pill from a deep-link via
   the project landing page's "Open memory at project scope" button.
   The pill activates and the URL carries `?scopeKind=project&scopeId=<id>`.
