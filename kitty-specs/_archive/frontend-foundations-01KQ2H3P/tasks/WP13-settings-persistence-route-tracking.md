---
work_package_id: "WP13"
title: "Settings persistence (single JSON, schemaVersion) + LoadRoute/SaveRoute/LogRouteChange"
dependencies:
  - "WP10"
  - "WP12"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 13 - Persistence"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP13 – Settings persistence + route tracking

## Goal

Implement Kenaz privacy CI invariant #5: a single JSON file at `$USER_CONFIG_DIR/kaneaz-harness/settings.json` with `schemaVersion: 1`, `lastRoute`, `theme`, `accent`, `windowSize`. Read once on app start; written debounced (250 ms) on change. Migrations gated on `schemaVersion`. No second persistence file. Add `LoadRoute / SaveRoute / LogRouteChange` plus `LoadTheme / SaveTheme` Bindings methods so the router restores `lastRoute` on first paint.

## Spec references

- FR-006 (light / dark / system theme — persistence)
- FR-016 (window-size minimum)
- C-005 (local-first)
- NFR-001 (first paint latency under 1 s — `lastRoute` must restore without slowing first paint)

## Plan references

- §2.2 (`settings.go` under `core/rpc/`)
- §3.2 Bindings (`LoadRoute`, `SaveRoute`, `LoadTheme`, `SaveTheme`)
- §4.1 ("Router restores `lastRoute` from `Settings.LoadRoute()` on first paint; falls back to `/sessions` if absent")
- §4.3 invariant #5 ("Single-file persistence with schema versioning … `$USER_CONFIG_DIR/kaneaz-harness/settings.json` with a top-level `schemaVersion: 1` integer. The file is read once on app start, written debounced (250 ms) on change. No second persistence file. Rotation/migration logic gates `schemaVersion` mismatches behind a guarded migration step. Charter `WindowSize` defaults are read from the charter at first run and merged in — never re-read after first persist.")
- §5.5 persisted UI state JSON schema
- §7 v1.0 item 12 (privacy CI invariants)

## Subtasks

- T001 — Implement `core/rpc/settings.go` defining `type SettingsStore interface { LoadRoute() (string, error); SaveRoute(string) error; LogRouteChange(from, to string) error; LoadTheme() (string, error); SaveTheme(string) error; LoadAll() (Settings, error); SaveAll(Settings) error }`. Default implementation reads/writes `$USER_CONFIG_DIR/kaneaz-harness/settings.json`. Charter `WindowSize` default merged at first run only.
- T002 — Add a `schemaVersion`-gated migration step. v1 schema is the only one for now; the migrator panics-with-message if `schemaVersion > 1` is read by an older binary. Add Go unit tests covering: missing file → defaults; corrupted JSON → quarantine + defaults; schemaVersion mismatch → migration path called.
- T003 — Wire `Bindings` (WP10) to expose `LoadRoute`, `SaveRoute`, `LoadTheme`, `SaveTheme`. Add `LogRouteChange` so the router can audit-log route transitions through `event-log` (downstream mission) once available; for now, route to `eventLog.ts` client-side with a Go-side stub.
- T004 — Implement `frontend/src/lib/settings.ts` with debounced (250 ms) write coalescing. Implement `frontend/src/lib/routing.ts` to call `LoadRoute()` once on app boot and restore the route before first paint; fall back to `/sessions` if absent. Add Vitest tests exercising debounce, restoration, and fallback.

## Acceptance criteria

- `settings.json` schema matches plan §5.5 exactly: `{ schemaVersion: 1, lastRoute, theme, accent, windowSize }`.
- The file is read once on app start; subsequent writes are debounced 250 ms.
- Cold-start route restoration: `LoadRoute()` returns the prior route (e.g., `/audit`) and the router restores it before first meaningful paint (NFR-001).
- Charter `WindowSize` default applies at first run only; never re-read after first persist.
- Schema migration logic gates `schemaVersion` mismatches behind a guarded step; older binary refuses to overwrite a newer schema.
- Privacy CI invariant #5 check (a CI grep + a Go test) asserts there is exactly one persistence file path under `core/rpc/settings.go`.

## Files to create/modify

- Create: `core/rpc/settings.go`, `core/rpc/settings_test.go`.
- Modify: `core/rpc/bindings.go` to expose `LoadRoute / SaveRoute / LogRouteChange / LoadTheme / SaveTheme`.
- Create: `frontend/src/lib/settings.ts`, `frontend/src/lib/routing.ts` (or modify if WP12 stubbed it).
- Create: `scripts/ci/check-single-persistence-file.sh`.
- Update: `docs/ci-invariants.md` listing invariant #5.
- Create: Vitest tests under `frontend/src/lib/__tests__/settings.spec.ts`.

## Definition of done

- All acceptance criteria pass.
- `useTheme()` from WP12 wired to `LoadTheme / SaveTheme`.
- `useKeepAlive()` from WP12 explicitly does NOT use `settings.json` for per-session state — per-session summaries round-trip through RPC instead, per plan §5.5 ("No session-by-session state lives here").
- WP14 will reference invariant #5's CI check among the four remaining invariants it lands.
