# Fleet Emergency Lockdown — Operator & Developer Guide

Mission: `fleet-emergency-lockdown-01NDFSEX12`
Shipped: v0.19.0
Tier: Team+ (requires `CapEmergencyLockdown` capability)

---

## What is emergency lockdown?

Fleet admins can issue an emergency lockdown to freeze all AI activity across
every harness installation in their org within 2 seconds. When a lockdown is
active:

- The **LockdownBanner** appears at the top of the UI with the admin-supplied
  reason (if any).
- The chat composer is visually disabled (WP05).
- All state-mutating RPC bindings (`Sessions_SendMessageWithBlocks`,
  `LLM_StartStream`, `Graph_StartRun`, `Graph_Resume`, `Workflows_Run`,
  `Workflows_RunNow`) return `fleet.ErrLockdownActive` immediately.
- In-progress LLM streams and tool calls already in flight are NOT cancelled —
  only new calls are blocked. In-flight work completes normally.

Lockdown is lifted instantly (within the next long-poll cycle, typically
< 2 seconds) when the admin clears the signal.

---

## Mechanism

The harness maintains a background **Watcher** goroutine that long-polls
`GET /api/v1/lockdown/wait` on the fleet server:

- The server holds the connection for up to 60 seconds. It responds:
  - **200 JSON** immediately when the lockdown state changes.
  - **204 No Content** after 60 seconds with no change (timeout; reconnect).
- The client uses a 65-second timeout (5 s headroom).
- On network failure, the Watcher backs off: 1 s → 5 s → 30 s.
- The lockdown state is stored in a **process-global `atomic.Bool`**
  (`lockdownActive`) readable by any goroutine without allocation.

On every state transition (false → true or true → false), the Watcher emits
a `fleet:lockdown:changed` Wails broker event. The frontend `LockdownBanner`
subscribes via `useEventStream` and updates instantly.

At process start, `BootstrapLockdownStatus` makes a one-shot call to
`GET /api/v1/lockdown/status` before any user-facing surface mounts, so the
harness boots into locked state when the fleet says so.

---

## Capability gate

The Watcher is gated behind `CapEmergencyLockdown` (`"emergency_lockdown"`).
When the capability is absent (e.g. on the Personal tier or when fleet is
disabled), the Watcher exits immediately and the lockdown flag is never set.

---

## Bypass for operators

In exceptional situations an operator can set the bypass env var to allow the
harness to run even when a lockdown is active:

```sh
HARNESS_FLEET_LOCKDOWN_BYPASS=1 ./kenaz-harness
```

**This must never be used in normal operation.** Every process start with the
bypass set emits a `fleet.lockdown_bypass_used` audit event so the bypass is
always traceable.

The bypass is evaluated lazily on every `CheckLockdown()` call, so changing
the env var after the process starts has no effect (the process reads it at
startup).

---

## Audit trail

Three audit kinds are defined in `core/context/audit`:

| Kind | When |
|------|------|
| `fleet.lockdown_received` | Lockdown state transitions to **active** |
| `fleet.lockdown_cleared` | Lockdown state transitions to **inactive** |
| `fleet.lockdown_bypass_used` | Process started with bypass env var set |

The `fleet.lockdown_bypass_used` event fires once per process start, regardless
of whether a lockdown is currently in effect.

---

## RPC surface

### `Settings_FleetLockdownStatus` → `LockdownStatusView`

Reads the current lockdown state from the process-global flag. Called by the
frontend `LockdownBanner` on mount and after every `fleet:lockdown:changed`
event.

```typescript
interface LockdownStatusView {
  active: boolean;
  reason: string; // empty when inactive or no reason given
}
```

### Error: `fleet.ErrLockdownActive`

State-mutating bindings return this error when a lockdown is active and the
bypass env var is not set. The frontend should surface this to the user as
the reason the action was rejected.

---

## Implementation layout

| File | Purpose |
|------|---------|
| `core/fleet/lockdown.go` | `Watcher`, `LockdownActive()`, `BrokerSink`, `BootstrapLockdownStatus` |
| `core/fleet/lockdown_bypass.go` | `LockdownBypassed()`, `AuditLockdownBypass()` |
| `core/fleet/errors.go` | `ErrLockdownActive` sentinel |
| `core/rpc/middleware/lockdown.go` | `CheckLockdown()` guard |
| `core/rpc/views/settings/fleet.go` | `SetLockdownBroker`, `FleetLockdownStatus`, `LockdownStatusView` |
| `core/context/audit/audit.go` | 3 new `Kind*` constants + payload types |
| `core/rpc/api.go` | Bootstrap call in `SetContext`, broker wiring |
| `core/rpc/bindings.go` | `Settings_FleetLockdownStatus` binding + guards |
| `frontend/src/components/ui/LockdownBanner.vue` | Fleet lockdown UI banner |

---

## Testing

```sh
# Go backend tests
go test ./core/fleet/... ./core/rpc/... ./core/rpc/middleware/... -race -count=1 -short

# Integration test
go test ./core/rpc/... -run TestLockdown -race -count=1 -short

# Frontend
cd frontend && ./node_modules/.bin/vitest run src/components/ui/__tests__/LockdownBanner.test.ts
```

---

## Known limitations

- In-flight LLM streams are not interrupted. The lockdown gate only blocks
  new requests; active streams run to completion (or until the existing
  stream-stop mechanism fires).
- The frontend composer disable (preventing new sends) is the primary UX
  signal; the RPC guard is the enforcement layer.
