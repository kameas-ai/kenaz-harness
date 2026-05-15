// default_audit_bulk_purge_policy.cedar — Audit bulk-purge default policy
// for mission `audit-log-enhancement-01KX5R8F` (WP08).
//
// Resource family: `AuditLog::"events"` (singleton)
// Actions:
//   `Action::"audit.bulk_purge"` — bulk-deletes selected event rows from
//                                  the append-only audit log store.
//                                  Irreversible, destructive operation.
//
// Default posture: FORBID for all principals. Operators who want to allow
// bulk purge must layer an explicit permit rule in their own policy file:
//
//   permit (
//       principal == User::"local",
//       action == Action::"audit.bulk_purge",
//       resource == AuditLog::"events"
//   );
//
// This is the only shipped Cedar policy that defaults to forbid rather
// than permit. Audit integrity is a security invariant; destructive
// write access to the event log must be explicitly opted in.

forbid (
    principal,
    action == Action::"audit.bulk_purge",
    resource is AuditLog
);
