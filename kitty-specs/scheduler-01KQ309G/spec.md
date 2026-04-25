# Feature Specification: Scheduler — Cron + One-Time + Missed-Run Catch-Up

**Feature Branch**: `feat/scheduler-01KQ309G`
**Created**: 2026-04-25
**Status**: Draft
**Input**: User direction (earlier this session): "We want cron style and one time and care deeply about missed run catch up." Charter: "Sessions can be scheduled on a cron with missed runs executing when you reopen your laptop, no always-on server required."

## Why this mission exists

The harness is a desktop app — operators close their laptops; the OS suspends. Production cron only works when the host is always on. The harness's promise is that **a workflow scheduled at 09:00 fires at 09:00 if the laptop is open, OR fires the moment the laptop comes back from suspend with the missed run plainly recorded**. This requires:

1. A persistent scheduler state (knows what should have run while suspended)
2. Catch-up on resume (the OS or the harness detects wake)
3. Idempotency support (catch-up doesn't re-fire something that did fire)
4. Operator visibility ("this run fired late, here's why")

Without this, the harness is no different from `crontab` — and `crontab` doesn't run when the laptop is closed. With this, the harness becomes a uniquely-positioned local-first agent runtime that respects both productivity (sleep when idle) and reliability (don't drop scheduled work).

## Dependencies and relationships

- **Depends on**: `storage-foundations` (schedule + run-history persistence), `event-log` (audit), `policy-engine` (per-schedule policy gate), `workflow-engine` (the primary executor for scheduled work).
- **Enables**: scheduled workflows (the user's day-one need); future enterprise features (compliance reports run at midnight; daily memory rebuilds; periodic context refresh).
- **Coordinates with**: `core/scheduler/` existing scaffold (currently a stub).
- **Does not cover**: workflow execution itself (delegated to `workflow-engine`); UI for schedule management (a follow-up UI mission).

## User Scenarios & Testing *(mandatory)*

### User Story 1 — A bundle author declares a scheduled workflow that runs on cron (Priority: P1)

A bundle declares a schedule: "every weekday at 09:00, run workflow `daily-summary`." The harness, while open, fires the workflow at the scheduled time. The run is auditable end-to-end. Multiple schedules in a bundle don't conflict; multiple bundles' schedules coexist.

**Why this priority**: This is the cron half. Without it, scheduled work is a manual reminder.

**Independent Test**: A bundle declares a workflow scheduled `*/2 * * * *` (every 2 minutes). The harness runs for 6 minutes and fires the workflow exactly 3 times.

**Acceptance Scenarios**:

1. **Given** a bundle declaring `schedule: "0 9 * * 1-5"`, **When** the time arrives on a weekday and the harness is running, **Then** the workflow fires within ±5 seconds and the event log records the trigger.
2. **Given** two bundles each declaring schedules, **When** their cron expressions overlap (e.g., both `0 * * * *`), **Then** both fire within the same second window and audit records distinguish them.
3. **Given** an invalid cron expression in a bundle, **When** the bundle is activated, **Then** activation fails with a typed validation error naming the bundle and the offending expression.

---

### User Story 2 — A scheduled run that should have fired while the laptop was closed runs on resume (Priority: P1)

A schedule is `0 9 * * *` (daily at 09:00). The operator's laptop is closed at 08:30. They reopen it at 11:30. The harness detects the wake, sees that the 09:00 fire was missed, and runs it now — labeling the run "deferred" with the original target time and the actual fire time. The operator sees the deferred run in the audit log; downstream analytics (event log) can reconstruct that the run was late.

**Why this priority**: This is the entire reason the user said "we care deeply about missed run catch up." It's what differentiates this scheduler from cron.

**Independent Test**: A schedule fires hourly. The harness is paused (simulating sleep) for 3 hours. On resume, exactly 3 catch-up runs fire with original-fire-time and actual-fire-time recorded.

**Acceptance Scenarios**:

1. **Given** a schedule should have fired N times during a sleep window, **When** the harness resumes, **Then** N catch-up runs are executed with each one's `original_fire_at` and `actual_fire_at` recorded.
2. **Given** the operator's laptop has been closed for a week and a daily schedule has 7 missed fires, **When** they resume, **Then** the catch-up policy declared on the schedule decides whether to run all 7, only the latest, or none — with a typed audit event for each skipped one.
3. **Given** a catch-up run is itself running while another scheduled fire arrives, **When** the second fire arrives, **Then** the engine respects the schedule's `concurrency` policy (`queue`, `skip_if_running`, `cancel_running_and_replace`).

---

### User Story 3 — One-time schedules + recurring schedules coexist (Priority: P1)

A schedule can be `cron: "0 9 * * *"` (recurring) or `at: "2026-04-30T15:00:00Z"` (one-time). One-time schedules fire once and disappear; recurring schedules persist. Both flow through the same execution path.

**Why this priority**: One-time scheduling is just as common as cron (e.g., "remind me to commit this PR at 5pm"). Same engine, same audit shape — half the work, double the value.

**Independent Test**: A bundle declares one cron schedule and one one-time schedule for "30 seconds from now." Both fire correctly; the one-time schedule disappears from the next-fire calculation after firing.

**Acceptance Scenarios**:

1. **Given** a one-time schedule with `at` 30 seconds in the future, **When** the time arrives, **Then** the workflow fires exactly once.
2. **Given** the one-time schedule has fired, **When** the harness restarts, **Then** the schedule is no longer in the next-fire roster.
3. **Given** a one-time schedule with `at` in the past at activation time, **When** the bundle activates, **Then** the policy `on_past_at` (`fire_immediately` / `skip_with_warning` / `fail_activation`) is applied with a typed audit event.

---

### User Story 4 — Per-schedule catch-up policy gives operators control (Priority: P1)

Each schedule declares its catch-up policy: `all` (fire every missed instance), `latest_only` (one run, marked as "consolidating N missed instances"), `none` (skip with audit). Defaults: `latest_only` for cron, `fire_immediately` for one-time. An org policy may pin or restrict the catch-up behavior.

**Why this priority**: Catch-up isn't one-size-fits-all. A daily-summary workflow benefits from `latest_only` (one summary catches up the week). A backup workflow benefits from `none` (don't backup 7 times when you reopen). The author needs control.

**Independent Test**: Three schedules with each policy: `all` produces N catch-up runs; `latest_only` produces 1; `none` produces 0 + audit events per skipped fire.

**Acceptance Scenarios**:

1. **Given** schedule with policy `all`, **When** N missed fires accumulated during sleep, **Then** N runs fire on resume in fire-time order.
2. **Given** schedule with policy `latest_only`, **When** N missed fires accumulated, **Then** one run fires with `consolidating_count: N` recorded.
3. **Given** schedule with policy `none`, **When** N missed fires accumulated, **Then** no runs fire and N `schedule/skipped_catchup` events are emitted with the original-fire-time of each.

---

### User Story 5 — Schedule + run state persists across harness restarts (Priority: P1)

The schedule registry and the run history live in `storage-foundations` (SQLite). On restart, the scheduler resumes its calculation from persisted state — including pending one-time schedules, last-fire timestamps for each cron, and any in-flight runs that were cancelled at shutdown.

**Why this priority**: Without persistence, every restart loses state and missed-run catch-up is impossible. This is the foundation that User Stories 2 and 3 depend on.

**Independent Test**: A schedule fires once; the harness is killed; on restart, the next-fire is calculated correctly relative to the recorded last-fire.

**Acceptance Scenarios**:

1. **Given** a recurring schedule that just fired at 09:00, **When** the harness restarts, **Then** the next-fire calculation uses the persisted last-fire (09:00) plus the cron expression.
2. **Given** an in-flight run when the harness shuts down, **When** the harness restarts, **Then** the run is marked as `interrupted_by_shutdown` and the schedule's recovery policy decides whether to re-fire.

---

### User Story 6 — Schedule activation is policy-gated (Priority: P2)

An org policy may forbid certain schedule patterns (e.g., "no schedules with intervals shorter than 5 minutes"; "no schedules that fire workflows containing banned MCP servers"). The policy engine evaluates each schedule at activation and at every fire. Denials produce typed audit events; the schedule fails to activate or fails to fire.

**Why this priority**: Enterprise readiness. A workflow author shouldn't be able to pin a per-second cron and consume the harness. P2 because the v1 default of "no policy = anything goes" is acceptable for solo use; enterprise deployments configure org policy.

**Independent Test**: An org policy `scheduler_min_interval: 60s` denies activation of a schedule pinned at `*/30 * * * * *` (every 30 sec). An accepted schedule fires successfully.

**Acceptance Scenarios**:

1. **Given** an org policy denying intervals shorter than 5 minutes, **When** a bundle declares a 1-minute schedule, **Then** activation fails with a typed denial.
2. **Given** an active schedule, **When** the schedule's referenced workflow contains a node denied by org policy, **Then** the fire-time evaluation fails with a typed audit event before the workflow runs.

---

### User Story 7 — Operator can list / pause / resume / delete schedules (Priority: P2)

An operator surface (UI later, RPC API now) lists all active schedules with their next fire times, history, and policies. Operators can pause a schedule (next fires skip with audit), resume, or delete.

**Why this priority**: Without operator visibility, schedules are invisible background processes. P2 because v1 can ship with bundle-declared schedules-only and add the management surface in v1.x.

**Independent Test**: An operator paused schedule does not fire; a resumed schedule fires; a deleted schedule is gone.

**Acceptance Scenarios**:

1. **Given** the operator pauses a schedule, **When** the next fire time arrives, **Then** the schedule is skipped with `schedule/paused_skip` event.
2. **Given** a paused schedule is resumed, **When** the next fire time arrives, **Then** the schedule fires normally.
3. **Given** a deleted schedule, **When** the harness restarts, **Then** it does not re-appear from any persisted state.

---

### Edge Cases

- Cron expression valid but pathological (e.g., `0 0 31 2 *` — Feb 31, never fires): activation succeeds (the parser accepts); audit records "next fire never" so operator can spot it.
- DST transition (a schedule of `0 2 * * *` on the spring-forward day): the schedule fires exactly once, at the wall-clock-relevant interpretation; document the choice; audit records actual fire time.
- A schedule's referenced workflow is removed (bundle uninstalled): subsequent fires fail at workflow lookup with `schedule/workflow_missing` and the schedule auto-pauses.
- Two harnesses running on the same machine with the same data dir (operator misconfiguration): single-writer enforcement (per `storage-foundations`) prevents the second from claiming schedule state; the second exits with a clear "another harness is already using this database" error.
- Daylight Saving fall-back (clock goes back, an hour repeats): a `0 1 * * *` schedule fires once during the repeated hour, not twice — the scheduler tracks fire windows by monotonic key, not wall clock.
- Catch-up on resume when the harness was unreachable for 90 days: hard cap on missed-fire enumeration to prevent runaway audit; operator surface visibly summarizes the cap if hit.
- A one-time schedule with `at` set ten years in the future: persisted with the fire-time; the harness uses a long-poll wakeup rather than spinning forever.

## Requirements *(mandatory)*

### Functional Requirements

| ID | Title | User Story | Priority | Status |
|----|-------|------------|----------|--------|
| FR-001 | Schedule bundle artifact kind | As an author, I want to declare schedules as a registered bundle artifact kind via `bundle-format-resolver`. | High | Open |
| FR-002 | Cron expression support | As an author, I want standard cron expressions (5-field minute-precision; second-precision is opt-in via `*/N * * * * *` 6-field form behind a per-schedule capability flag). | High | Open |
| FR-003 | One-time `at` schedule | As an author, I want one-time schedules with an ISO-8601 timestamp. | High | Open |
| FR-004 | Persistent schedule registry | As an operator, I want all schedules + last-fire timestamps + run history persisted via storage-foundations. | High | Open |
| FR-005 | Wake / resume detection | As an operator, I want the scheduler to detect when the host has resumed from sleep / suspend / hibernation and trigger the catch-up evaluation. | High | Open |
| FR-006 | Catch-up policy per schedule | As an author, I want a `catchup_policy` declared per schedule: `all`, `latest_only`, `none`; defaults documented. | High | Open |
| FR-007 | Concurrency policy per schedule | As an author, I want a `concurrency_policy` declared per schedule for "what if the previous run is still in-flight": `queue`, `skip_if_running`, `cancel_running_and_replace`. | High | Open |
| FR-008 | Pre-flight validation | As an operator, I want every schedule's cron / at / catchup / concurrency / referenced-workflow validated at bundle activation; bad schedules fail fast. | High | Open |
| FR-009 | Audit per schedule and per fire | As an operator, I want the event log to record `schedule/registered`, `schedule/fire`, `schedule/catchup_fire`, `schedule/skipped_catchup`, `schedule/paused`, `schedule/resumed`, `schedule/deleted`, `schedule/workflow_missing`, `schedule/policy_denied`, `schedule/interrupted_by_shutdown`. | High | Open |
| FR-010 | Original vs actual fire time | As an operator, I want each fire's `original_fire_at` (when cron said) and `actual_fire_at` (when it actually fired) recorded — distinct on catch-up. | High | Open |
| FR-011 | Policy gate | As an operator, I want every schedule activation and every fire gated by `policy-engine.Evaluate` against the appropriate control kind. | High | Open |
| FR-012 | Manual operator surface (RPC) | As an operator, I want list / pause / resume / delete via the harness RPC API; the management UI consumes this in a follow-up. | Medium | Open |
| FR-013 | Hard-cap missed-fire enumeration | As an operator, I want a hard cap on how many missed fires the catch-up evaluator enumerates (default 1000); beyond, the schedule auto-pauses with `schedule/missed_cap_exceeded`. | Medium | Open |
| FR-014 | DST and time-zone awareness | As an author, I want to declare a schedule's time zone (default: host); cron expressions are interpreted in the declared TZ; DST transitions are documented and consistent. | Medium | Open |
| FR-015 | Idempotency hint | As an author, I want a per-schedule `idempotency_key` template (templated on fire time); the engine passes it to the workflow so workflows can implement their own idempotency on top. | Medium | Open |
| FR-016 | Long-future schedule support | As an author, I want one-time schedules years in the future to persist without spinning the scheduler; long-poll wake on the persistent timer. | Medium | Open |
| FR-017 | Single-writer safety | As an operator, I want a second harness instance to refuse to run against the same data dir; the first instance owns scheduling. | High | Open |
| FR-018 | Auto-pause on workflow-missing | As an operator, I want a schedule whose referenced workflow no longer exists to auto-pause with a typed audit event, not silently fail forever. | Medium | Open |

### Non-Functional Requirements

| ID | Title | Requirement | Category | Priority | Status |
|----|-------|-------------|----------|----------|--------|
| NFR-001 | Fire-time accuracy (running) | When the harness is running, scheduled fires occur within ±5 seconds of the cron-derived target. | Performance | High | Open |
| NFR-002 | Catch-up latency on resume | Time from harness wake-from-sleep to first catch-up fire under 10 seconds. | Performance | High | Open |
| NFR-003 | Schedule registry persistence | Every schedule mutation (register / pause / resume / delete) persists in under 5 ms p99. | Performance | High | Open |
| NFR-004 | Concurrent schedule capacity | Engine sustains ≥ 1000 active schedules without per-tick contention regression. | Reliability | Medium | Open |
| NFR-005 | Audit completeness | 100 % of schedule lifecycle events and per-fire decisions produce append-only event-log entries. | Auditability | High | Open |
| NFR-006 | DST round-trip correctness | Across the spring-forward and fall-back days, a daily schedule fires exactly once per day in 100 % of test-matrix runs. | Reliability | Medium | Open |
| NFR-007 | Hard-cap enforcement | When missed-fire count exceeds the hard cap, the auto-pause + cap-exceeded event fire 100 % of the time. | Reliability | High | Open |

### Constraints

| ID | Title | Constraint | Category | Priority | Status |
|----|-------|------------|----------|----------|--------|
| C-001 | Architectural integrity | Scheduler logic in `core/scheduler/`; no other `core/` package imports scheduler internals. | Technical | High | Open |
| C-002 | Bundle-format compatibility | Schedules live within the existing bundle artifact format. | Technical | High | Open |
| C-003 | Append-only event log | All scheduler events are append-only with redaction. | Security | High | Open |
| C-004 | Storage-foundations persistence | Schedule + run state persists via storage-foundations migrations; no parallel persistence. | Technical | High | Open |
| C-005 | Local-first / single-process | The scheduler runs in-process inside the harness; no external scheduler daemon (no systemd timer, no launchd hook). Wake detection is OS-event-based and best-effort. | Technical | High | Open |
| C-006 | Policy gate before every fire | Every fire MUST consult `policy-engine.Evaluate` against the appropriate control kind. | Security | High | Open |
| C-007 | SOC 2 readiness | Schedule lifecycle and per-fire decisions produce evidence sufficient for SOC 2 audit per the charter. | Regulatory | High | Open |

### Key Entities

- **Schedule** — bundle artifact declaring `cron` or `at`, target workflow id, catchup_policy, concurrency_policy, time_zone, idempotency_key template, paused (bool).
- **ScheduleState** — runtime/persisted state per schedule: `last_fire_at`, `next_fire_at`, `paused`, `auto_paused_reason`, history of recent fires.
- **ScheduledFire** — typed record per fire: original_fire_at, actual_fire_at, kind (`on_time`, `catchup`, `manual_trigger`), workflow_run_id, outcome.
- **CatchUpEvaluator** — runs on resume + at startup; given persisted ScheduleState and the current wall-clock + monotonic clock, enumerates missed fires up to the hard cap and applies per-schedule catchup_policy.
- **WakeDetector** — OS abstraction. macOS: `IOPMSchedulePowerEvent` callback / NSWorkspace willWakeNotification. Linux: systemd-logind suspend/resume signals (where available) + monotonic-clock-jump heuristic. Windows: PowerSettingNotify. Fallback: monotonic-clock-jump > N seconds since last tick.
- **ScheduleEvent** — event log entry kinds: `schedule/registered`, `schedule/fire`, `schedule/catchup_fire`, `schedule/skipped_catchup`, `schedule/paused`, `schedule/resumed`, `schedule/deleted`, `schedule/workflow_missing`, `schedule/policy_denied`, `schedule/interrupted_by_shutdown`, `schedule/missed_cap_exceeded`, `schedule/auto_paused`, `schedule/wake_detected`.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A bundle author declares a cron schedule and the harness fires the workflow at the scheduled time within ±5 seconds while running.
- **SC-002**: A schedule that should have fired during a 3-hour simulated sleep fires within 10 seconds of resume, with `original_fire_at` and `actual_fire_at` distinct.
- **SC-003**: 100 % of schedule lifecycle events and per-fire decisions produce append-only event-log entries with the `schedule/` namespace.
- **SC-004**: A daily schedule across spring-forward and fall-back DST days fires exactly once per day in 100 % of test-matrix runs.
- **SC-005**: A second harness instance pointed at the same data dir refuses to run with a clear error.
- **SC-006**: An invalid cron expression in a bundle fails activation 100 % of the time with the offending expression named.
- **SC-007**: Engine sustains ≥ 1000 active schedules without per-tick contention regression.
- **SC-008**: When missed-fire count exceeds the hard cap, the schedule auto-pauses and a `schedule/missed_cap_exceeded` event fires 100 % of the time.

## Assumptions

- The Go cron parser library is reasonable to depend on (e.g., `github.com/robfig/cron/v3` or similar). Planning-phase decision.
- Wake detection is best-effort: not all OSes / power events fire reliably. Where the OS doesn't notify, the scheduler falls back to a monotonic-clock-jump heuristic on every tick.
- The hard cap on missed-fire enumeration (default 1000) is a footgun-prevention measure, not a security boundary; operators can raise it via per-schedule override.
- The scheduler runs in-process inside the harness — when the harness is killed, no schedules fire until it's reopened. The charter's "always available" promise is met by the harness being a normal desktop app the user opens, not by a system-level daemon.
- One-time schedules with `at` in the past at activation produce a typed audit event per the `on_past_at` policy; default is `fire_immediately`.

## Open Questions

1. **[NEEDS CLARIFICATION]** Cron parser — `robfig/cron/v3` (well-known, supports both 5- and 6-field cron, optional second precision) vs `adhocore/gronx` (newer, Goroutine-friendly) vs hand-rolled? Default if unresolved: `robfig/cron/v3` for its maturity and broad operator familiarity.
2. **[NEEDS CLARIFICATION]** Wake-detection priority — do we ship macOS first (NSWorkspace willWakeNotification via cgo) and rely on the monotonic-clock-jump heuristic on Linux/Windows, or all three OS-native paths in v1? Default if unresolved: macOS-native + clock-jump fallback at v1; Linux/Windows native paths in v1.x.
3. **[NEEDS CLARIFICATION]** Default catch-up policy when a bundle doesn't declare one — `latest_only` (safer, fewer surprise runs) or `all` (matches operator expectation that "every schedule fires every time it should")? Default if unresolved: `latest_only` is safer — the surprise of "10 emails sent at once on resume because I forgot to declare catchup_policy" is bigger than the surprise of "I expected 10 runs to consolidate, only got 1." Document loudly.
