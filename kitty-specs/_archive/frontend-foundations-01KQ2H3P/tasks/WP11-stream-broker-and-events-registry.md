---
work_package_id: "WP11"
title: "streamBroker + Emitter + contracts/wails-events.md (24 entries)"
dependencies:
  - "WP10"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 11 - Streaming"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP11 – streamBroker + Emitter + Wails-event topic registry

## Goal

Implement the single emitter for all `channel → Wails-event` bridges: a Go-side `streamBroker` that owns subscription lifecycle, fan-out, and `:stream-closed` payloads, paired with an `Emitter` interface that is the only authorized caller of `runtime.EventsEmit`. Document every topic in a new `contracts/wails-events.md` file at the repo root with 24 v1.0 entries (12 view topics × 2 kinds: `<view>:event` + `<view>:stream-closed`).

## Spec references

- FR-014 (streaming-friendly text rendering)
- FR-019 (type-safe streaming consumers)
- NFR-004 (streaming smoothness)
- C-001 (architectural integrity)
- C-005 (local-first; no outbound traffic)

## Plan references

- §2.2 (`emitter.go`, `stream_broker.go` under `core/rpc/`)
- §4.2 ("`Emitter` interface is the only authorised caller of `runtime.EventsEmit` … grep CI check forbids `runtime.EventsEmit` outside these two files. `streamBroker` owns subscription lifecycle: `Subscribe(viewName, eventKind, source <-chan T) (id, error)`, `Unsubscribe(id)`, and a pump that emits `(view-name):(event-kind)` topic + `(view-name):stream-closed` payload `{ id, reason, message? }` on close. `reason ∈ {ctx-cancelled, stop-called, backend-error}`.")
- §5.4 (Wails event topic registry; v1.0 entries: `sessions:event`, `mcp:event`, `a2a:event`, `llm:event`, `policy:event`, `audit:event`, `workflow:event`, `bundle:event`, `context:event`, `secrets:event`, `storage:event`, `scheduler:event`, plus `:stream-closed` for each — 24 entries; `shell:status-changed` reserved for v1.x)
- §7 v1.0 item 7 (streamBroker + Emitter + `contracts/wails-events.md`)

## Subtasks

- T001 — Create `core/rpc/emitter.go` defining `type Emitter interface { Emit(topic string, payload any) }`. Implement a Wails-backed default that wraps `runtime.EventsEmit`. This file is one of only two files allowed to call `runtime.EventsEmit`.
- T002 — Create `core/rpc/stream_broker.go` defining `type streamBroker` with `Subscribe(ctx, viewName, eventKind string, source <-chan T) (subID string, err error)`, `Unsubscribe(subID)`, and an internal pump goroutine per subscription. On close emit `<view>:stream-closed` payload `{ id, reason, message? }` with `reason ∈ {ctx-cancelled, stop-called, backend-error}`. Mirrors Kenaz spec 024 broker.
- T003 — Create `contracts/wails-events.md` at the repo root listing every topic with columns: topic, payload (Go struct), payload (TS interface), cardinality, lifecycle, ordering, privacy. Include 12 `<view>:event` + 12 `<view>:stream-closed` = 24 v1.0 entries. Mark `shell:status-changed` reserved for v1.x. Document the rule: "adding a topic requires a registry edit in the same PR (DIRECTIVE_010)".
- T004 — Add `scripts/ci/check-emitter-isolation.sh` greping for `runtime.EventsEmit` calls outside `core/rpc/emitter.go` and `core/rpc/stream_broker.go`; exits non-zero if any other file calls it. Wire into CI.

## Acceptance criteria

- `streamBroker.Subscribe` returns a stable subscription id and emits one Wails event per source-channel item until close.
- On close, broker emits `<view>:stream-closed` with the correct reason.
- `contracts/wails-events.md` lists 24 v1.0 entries plus the `shell:status-changed` v1.x reservation.
- `scripts/ci/check-emitter-isolation.sh` exits 0 on a clean tree; exits non-zero if a third file calls `runtime.EventsEmit`.
- Go unit tests cover: subscribe, fan-out, ctx-cancel close, stop-called close, backend-error close.

## Files to create/modify

- Create: `core/rpc/emitter.go`, `core/rpc/stream_broker.go`, `core/rpc/stream_broker_test.go`, `core/rpc/emitter_test.go`.
- Create: `contracts/wails-events.md`.
- Create: `scripts/ci/check-emitter-isolation.sh`.
- Modify: `core/rpc/bindings.go` (from WP10) to inject `*streamBroker` per `Bindings` field.
- Modify: CI workflow to invoke `check-emitter-isolation.sh`.

## Definition of done

- All acceptance criteria pass.
- `contracts/wails-events.md` is the canonical registry for downstream missions adding new streams.
- WP12's TS-side `useStream` composable consumes these topics typed end-to-end.
- Cross-mission note: downstream missions adding a new topic must update both `contracts/wails-events.md` and the corresponding `<view>` sub-package interface in the same PR.
