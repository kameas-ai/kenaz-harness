# Fleet Audit Archival — Operator & User Guide

Mission: `fleet-audit-archival-01NDFSEX13`

## Background

The harness emits structured audit events for security-relevant operations
(Cedar policy decisions, ACP envelope dispatch, session lifecycle, etc.).
This mission adds an immutable archival pipeline that batches and signs those
events, posts them to an immudb ledger hosted by the fleet endpoint, and
enforces local retention.

**Capability gate:** `CapAuditLogImmuDB` — available on **Team+** subscription
tier only. The archiver, sweeper, and Compliance panel are all no-ops when the
capability is inactive.

## Status

| Work Package | Title | Status |
|---|---|---|
| WP01 | `audit.TailReader` interface + `MemoryTailReader` | Shipped |
| WP02 | Archiver: batcher + signing + POST + cursor | Shipped |
| WP03 | Hash-chain verifier + chain-break hard-stop | Shipped |
| WP04 | Retention sweeper + config knob | Shipped |
| WP05 | Compliance RPCs (`Status`, `ArchiveNow`, `SetRetention`) | Shipped |
| WP06 | `CompliancePanel.vue` + Settings → Security → Compliance tab | Shipped |
| WP07 | Integration tests + operator docs + fork recipe | Shipped |

---

## Architecture

### Event flow

```
Audit emitter
   │  (core/context/audit/audit.go)
   ▼
TailReader.Since(cursor, batchSize)
   │  (StoreTailReader wraps the audit store; MemoryTailReader for tests)
   ▼
BatchChainVerifier.VerifyBatch(events)
   │  Walks prev_hash → payload_hash links.
   │  Zero PrevHash = checkpoint (session start / migration boundary).
   │  On break → emits KindFleetAuditChainBreak, sets chainErr, halts.
   ▼
buildAuditBatch(events, signer)
   │  JSON body: device pubkey fingerprint + ed25519 signature over batch.
   ▼
AuditHTTPPoster.Post(ctx, "/api/v1/audit/append", ...)
   │  On HTTP 200: AdvancePredecessor + saveAuditCursor (atomic rename).
   │  On non-200: log + retry next interval; cursor NOT advanced.
   ▼
emitAudit(KindFleetAuditArchived, {count, cursor})
```

### Hash-chain fields

Every `TailEvent` carries:

| Field | Description |
|---|---|
| `PayloadHash` | `SHA-256(prev_hash_hex \| kind \| emitted_at_unix_ms \| payload_json)` |
| `PrevHash` | PayloadHash of the immediately preceding event; `[32]byte{}` for chain resets |

The verifier checks `event[i].PrevHash == event[i-1].PayloadHash`. It accepts a
zero `PrevHash` as a checkpoint (no predecessor required).

### Retention

The `AuditRetentionSweeper` runs hourly. It deletes rows satisfying **both**:

1. `event_id ≤ last_acked_cursor` (ACK'd by fleet)
2. `emitted_at < now - retention_window`

Default retention: 90 days. Configurable via
`fleet.audit_local_retention_days` in the fleet config bundle, or via
Settings → Security → Compliance.

---

## Operating the archiver

### Enabling archival

Archival requires:

1. A Team+ subscription with `CapAuditLogImmuDB` active for your org.
2. A fleet-authenticated session (Profile configured with a valid token).

When both are present, the archiver starts automatically on boot.

### Checking status

Open Settings → Security → Compliance. The panel shows:

- **Last archived**: timestamp of the most recent successful flush.
- **Pending events**: approximate count of events awaiting the next flush.
- **Archiver**: Running / Stopped.
- **Retention window**: current local retention setting.

### Triggering an immediate flush

Click **Archive now** in the Compliance panel, or call the RPC directly:

```
Compliance_ArchiveNow()
```

### Adjusting retention

Select 30, 60, 90, or 365 days in the Compliance panel retention picker. The
new setting takes effect on the next sweep pass (up to 1 hour later).

---

## Chain-break recovery

A hash-chain break means the archiver detected a gap in the `prev_hash` →
`payload_hash` continuity of events since the last flush. This can happen when:

- Events were deleted from the local audit store while unarchived.
- The local audit database was migrated without a chain reset checkpoint.
- Corruption in the event store.

### What happens

1. The archiver sets `chainBreak = true` and halts all further flushes.
2. A `fleet.audit_chain_break` audit event is emitted with the event ID of
   the first inconsistent event.
3. The Compliance panel shows a **red banner**: "Hash-chain continuity break
   detected."

### Recovery procedure

1. Identify the break point from the `fleet.audit_chain_break` event in the
   local audit log (Settings → Security → Audit).
2. Decide whether the gap is acceptable (e.g. a deliberate migration) or
   requires investigation.
3. If acceptable, advance the cursor past the break by calling:

   ```
   // Via fleet admin CLI or direct Go call in a maintenance binary:
   archiver.SkipToID(ctx, "<event_id_to_skip_to>")
   ```

   This emits `fleet.audit_chain_skipped` and clears the halt flag.
4. Archival resumes on the next flush cycle.

> **Note:** There is no UI control for `SkipToID` — this is intentional.
> Advancing past a break should require deliberate operator action, not
> a one-click button in the UI.

---

## Fork recipe

If you are forking the harness and **do not** want fleet audit archival:

1. Delete `core/fleet/audit_archive.go` and `core/fleet/audit_cursor.go`.
2. Delete `core/fleet/audit_chain_verifier.go`.
3. Delete `core/fleet/audit_retention.go`.
4. Delete `core/rpc/views/compliance/` (the entire directory).
5. Remove the `Compliance_*` bindings from `core/rpc/bindings.go`.
6. Remove the `Compliance()` method from `core/rpc/api.go` and
   `core/rpc/views/compliance` import.
7. Delete `frontend/src/views/settings/CompliancePanel.vue`.
8. Remove the Compliance tab from `frontend/src/views/settings/SettingsTabs.vue`
   (`{ label: 'Compliance', ... }`).
9. Remove the `showComplianceTab` computed + `CompliancePanel` component from
   `frontend/src/views/settings/SettingsView.vue`.
10. Remove `ComplianceClient` interface and `compliance:` field from
    `frontend/src/lib/harnessClient.ts`.
11. Remove `ComplianceStatus` from `frontend/src/lib/types.ts`.
12. Remove the `KindFleetAudit*` constants and `FleetAudit*Payload` structs
    from `core/context/audit/audit.go`.
13. Remove `TailReader`, `StoreTailReader`, and `MemoryTailReader` from
    `core/context/audit/tail.go` (or keep if useful for other purposes).

After the deletions, run `go build ./...` and `vue-tsc --noEmit` to catch any
remaining references.
