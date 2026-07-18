/**
 * auditDateFilters — factory helpers for the audit-log default filter state.
 *
 * Extracted from AuditView so that the default-range logic is unit-testable
 * independently of the Vue component (FR-001 correctness fix).
 *
 * Invariant: defaultUntil() >= defaultSince() always holds.
 */

/**
 * Returns an ISO date string (YYYY-MM-DD, UTC) for N days before `now`.
 * `now` defaults to the current wall-clock time — tests can inject a fixed
 * Date to make the output deterministic.
 */
export function nDaysAgoISO(n: number, now: Date = new Date()): string {
  const d = new Date(now);
  d.setUTCDate(d.getUTCDate() - n);
  d.setUTCHours(0, 0, 0, 0);
  return d.toISOString().slice(0, 10); // "YYYY-MM-DD"
}

/**
 * Default Since value for the audit filter: midnight UTC, 7 days ago.
 */
export function defaultAuditSince(now?: Date): string {
  return nDaysAgoISO(7, now);
}

/**
 * Default Until value for the audit filter: empty string (open-ended / "now").
 * An empty Until maps to `undefined` in the filter object, meaning the server
 * returns all events up to the present moment — always >= Since.
 */
export function defaultAuditUntil(): string {
  return '';
}
