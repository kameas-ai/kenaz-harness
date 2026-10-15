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
 * The "Accept by" day for the deadline `d`: the UTC calendar date of
 * (deadline − 24 h). This is exactly Fleet's legal.DeferralDisplayDate
 * (kenaz-fleet service/legal/versions.go), which also renders the item's
 * own text ("Nothing is paused until November 20, 2026"), so every client
 * shows the same legal date in every zone. For a 2026-11-21T08:00Z deadline
 * that is November 20.
 */
export function acceptByDay(d: Date): Date {
  return new Date(d.getTime() - 24 * 60 * 60 * 1000);
}

/** "November 20, 2026" for the deadline `d`, in the user's locale; the date is Fleet's UTC one. */
export function formatAcceptByDate(d: Date): string {
  return acceptByDay(d).toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
    timeZone: 'UTC',
  });
}
