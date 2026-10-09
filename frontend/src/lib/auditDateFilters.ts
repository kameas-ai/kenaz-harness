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

// ── Wire bounds (dogfood 2026-10-08 P1) ─────────────────────────────────
// The Since/Until inputs hold date-only `YYYY-MM-DD` strings, but the Go
// side decodes `eventlog.FilterQuery.Since/Until` as `time.Time`, which
// only accepts RFC3339. Sending the bare date made every Audit_Filter
// call fail argument decoding ("cannot parse \"\" as \"T\"") — and a
// half-typed date failed once per keystroke. These helpers are the ONE
// place a date input becomes a wire bound; anything that is not a
// complete, real calendar date yields `undefined` (no bound) rather than
// a string the backend will reject.

const DATE_ONLY = /^\d{4}-\d{2}-\d{2}$/;

function isRealDate(d: string): boolean {
  if (!DATE_ONLY.test(d)) return false;
  const parsed = new Date(`${d}T00:00:00Z`);
  return !Number.isNaN(parsed.getTime()) && parsed.toISOString().slice(0, 10) === d;
}

/** Inclusive lower bound: start of the given UTC day, RFC3339. */
export function auditSinceBound(input: string): string | undefined {
  const d = input.trim();
  return isRealDate(d) ? `${d}T00:00:00Z` : undefined;
}

/** Inclusive upper bound: last nanosecond of the given UTC day, RFC3339. */
export function auditUntilBound(input: string): string | undefined {
  const d = input.trim();
  return isRealDate(d) ? `${d}T23:59:59.999999999Z` : undefined;
}

/**
 * Converts a persisted bound (a saved query's since/until, which may be an
 * RFC3339 timestamp, a legacy date-only string, or Go's zero time
 * `0001-01-01T00:00:00Z` for "no bound") back into the date input's
 * `YYYY-MM-DD` form. Unknown shapes become '' (no bound).
 */
export function auditDateInputFromBound(bound: string | undefined | null): string {
  if (!bound) return '';
  const d = bound.slice(0, 10);
  if (!isRealDate(d) || d.startsWith('0001-')) return '';
  return d;
}
