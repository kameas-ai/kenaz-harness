/**
 * approvalDeferral — Fleet's `deferrable_until` on pending-approval hub items
 * (kenaz-fleet docs/contract-pending-approvals.md). A required legal item an
 * existing customer may defer carries the instant it starts blocking; until
 * then it is still required, but nothing is paused. Shared by the Settings
 * banner (settingsIssues) and the step-through modal.
 */
import type { PendingApprovalItem } from '@/lib/types';

/**
 * When a required item is still deferrable — Fleet's `deferrable_until`
 * (legal items for existing customers) lies in the future — the date it
 * starts blocking; otherwise null (blocking now, or not required).
 */
export function deferredUntil(it: PendingApprovalItem, now: Date = new Date()): Date | null {
  if (!it.required || !it.deferrableUntil) return null;
  const d = new Date(it.deferrableUntil);
  if (Number.isNaN(d.getTime()) || d.getTime() <= now.getTime()) return null;
  return d;
}

/**
 * The last full calendar day, in the user's own time zone, that ends at or
 * before the deadline `d`. That day is shown as "Accept by <day>".
 *
 * Fleet's instants are 08:00Z, i.e. midnight at the start of the next
 * US-Pacific day, so the instant's own date is one day late for everyone
 * west of UTC+8 and later still further east. The day before the local day
 * that contains the instant always ends by the deadline: Pacific/Eastern
 * 2026-11-21T08:00Z → November 20; Tokyo → November 20 (conservative).
 */
export function acceptByDay(d: Date): Date {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate() - 1);
}

/** "November 20, 2026" for the deadline `d`, in the user's locale and zone. */
export function formatAcceptByDate(d: Date): string {
  return acceptByDay(d).toLocaleDateString(undefined, { year: 'numeric', month: 'long', day: 'numeric' });
}
