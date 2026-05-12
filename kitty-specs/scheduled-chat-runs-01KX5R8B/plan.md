# Plan — Scheduled Chat Runs (`scheduled-chat-runs-01KX5R8B`)

## Architecture overview

```
core/scheduler/
  job.go              — extend Job with Kind + ChatRunSpec fields
  chat_store.go       — NEW: SQLite CRUD for scheduled_chat_runs + history
  chat_dispatcher.go  — NEW: renders template, spawns headless session, routes sink

core/session/
  migrations_scheduled_chat_runs.go  — NEW: migration 0325 (two tables)
  migrations.go                      — add migration0325() call

core/policy/cedar/
  types.go            — add ActionScheduledRun* + EntityTypeScheduledChatRun + UID helper

core/rpc/views/scheduledchat/
  api.go              — NEW: ScheduledChatAPI interface + wire types
  impl.go             — NEW: concrete implementation
  impl_test.go        — NEW: unit tests

core/rpc/
  api.go              — add ScheduledChat() accessor to HarnessAPI interface + API struct field
  bindings.go         — add ScheduledChat_* Wails bindings

frontend/src/lib/
  scheduledChatClient.ts   — NEW: typed bridge to ScheduledChat_* bindings

frontend/src/views/settings/
  SettingsTabs.vue         — add Scheduled Chats tab entry
  SettingsView.vue         — mount ScheduledChatsPanel on ?tab=scheduledchats

frontend/src/views/settings/scheduledchat/  (new directory)
  ScheduledChatsPanel.vue      — list + enable-toggle + delete
  ScheduledChatFormModal.vue   — create / edit modal

frontend/src/views/workflows/
  ScheduledInbox.vue     — extend: add chat-run section below workflow rows
```

## WP decomposition

### WP01 — Schema + store (backend foundation)

Lands the SQLite migration and the `ScheduledChatStore` CRUD layer.
No scheduler wiring yet — just the DB shape and the store that reads/writes it.

**Files**:
- `core/session/migrations_scheduled_chat_runs.go` (new)
- `core/session/migrations.go` (add `migration0325()`)
- `core/scheduler/chat_store.go` (new)

**Tests**: store CRUD round-trips in `chat_store_test.go`.

---

### WP02 — Scheduler job-kind extension + dispatcher

Adds `Kind` + `ChatRunSpec` to `Job`, wires `ChatRunDispatcher`, extends
`core/scheduler` interface (no API surface yet).

**Files**:
- `core/scheduler/job.go` — add `Kind`, `ChatRunSpec` fields
- `core/scheduler/scheduler.go` — no interface change; dispatcher injection point added to `Config` in the concrete impl (not the interface — tests that stub the interface are unaffected)
- `core/scheduler/chat_dispatcher.go` (new) — template render + headless dispatch + sink routing
- `core/scheduler/chat_dispatcher_test.go` (new)

---

### WP03 — Cedar action family

Adds `ActionScheduledRun*` action constants, `EntityTypeScheduledChatRun`,
and `ScheduledChatRunUID` to `core/policy/cedar/types.go`. No gate wiring yet
(that lives in WP04 impl).

**Files**:
- `core/policy/cedar/types.go` — append constants

---

### WP04 — RPC view `scheduledchat`

The `ScheduledChatAPI` interface, concrete impl backed by `ScheduledChatStore`
+ scheduler, Cedar gate calls, and unit tests.  Wires `ScheduledChat()`
accessor onto `HarnessAPI` + `API`, and adds `ScheduledChat_*` Wails bindings.

**Files**:
- `core/rpc/views/scheduledchat/api.go` (new)
- `core/rpc/views/scheduledchat/impl.go` (new)
- `core/rpc/views/scheduledchat/impl_test.go` (new)
- `core/rpc/api.go` — add `ScheduledChat()` to interface + field + accessor + wiring in `New`
- `core/rpc/bindings.go` — add `ScheduledChat_*` bindings

---

### WP05 — Frontend client + Settings panel

`scheduledChatClient.ts`, `ScheduledChatsPanel.vue`, `ScheduledChatFormModal.vue`,
and the Settings wiring (tab + view mount).

**Files**:
- `frontend/src/lib/scheduledChatClient.ts` (new)
- `frontend/src/views/settings/scheduledchat/ScheduledChatsPanel.vue` (new)
- `frontend/src/views/settings/scheduledchat/ScheduledChatFormModal.vue` (new)
- `frontend/src/views/settings/SettingsTabs.vue` — add Scheduled Chats entry
- `frontend/src/views/settings/SettingsView.vue` — mount on `?tab=scheduledchats`
- `frontend/src/views/settings/scheduledchat/__tests__/ScheduledChatsPanel.spec.ts` (new)
- `frontend/src/views/settings/scheduledchat/__tests__/ScheduledChatFormModal.spec.ts` (new)

---

### WP06 — ScheduledInbox extension + mission close-out

Extends `ScheduledInbox.vue` to show a "Chat Runs" section below the workflow
rows, consuming the `scheduledChatClient`.  Updates `WorkflowsView.vue` if the
inbox tab needs to receive the new client prop.

**Files**:
- `frontend/src/views/workflows/ScheduledInbox.vue` — add chat-run section
- `frontend/src/views/workflows/WorkflowsView.vue` — pass `chatClient` prop if needed
- `frontend/src/views/workflows/__tests__/ScheduledInbox.spec.ts` — extend test

---

## Dependency order

```
WP01 (schema/store)
  └─ WP02 (job-kind + dispatcher)   WP03 (Cedar constants)
       └─────────────────────────────── WP04 (RPC view)
                                           └─ WP05 (frontend panel)
                                                └─ WP06 (inbox extension)
```

WP01 → WP02 → WP04 must be sequential (each needs the previous).
WP03 is a pure constants addition; it can land in parallel with WP02 or at
the start of WP04 (no ordering constraint beyond "before WP04").

## Acceptance criteria

- `go build ./core/...` green.
- `go test -count=1 -race -short ./core/scheduler/... ./core/rpc/...` green.
- `cd frontend && ./node_modules/.bin/vitest run --reporter=basic` green.
- User can create a scheduled chat run in Settings → Scheduled Chats.
- At trigger time (or via RunNow) the run fires and a history row appears.
- Banner sink emits `scheduled-chat:banner` event (verified via log / devtools).
