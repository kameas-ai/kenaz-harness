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

/** "November 21, 2026" in the user's locale. */
export function formatAcceptByDate(d: Date): string {
  return d.toLocaleDateString(undefined, { year: 'numeric', month: 'long', day: 'numeric' });
}
