/**
 * approvalDeferral — Fleet's deferrable_until instants are 08:00Z (midnight
 * starting the next US-Pacific day). "Accept by" must name the last full
 * local day that ends by the deadline, never the instant's UTC date.
 */
import { afterEach, describe, expect, it } from 'vitest';
import { acceptByDay, deferredUntil, formatAcceptByDate } from '@/lib/approvalDeferral';
import type { PendingApprovalItem } from '@/lib/types';

const ORIGINAL_TZ = process.env.TZ;
afterEach(() => {
  process.env.TZ = ORIGINAL_TZ;
});


describe('formatAcceptByDate', () => {
  // Fleet's legal.DeferralDisplayDate: the UTC date of (deadline − 24 h).
  // Every zone shows the same legal date, matching Fleet's own item text.
  it.each([
    ['America/Los_Angeles', '2026-11-21T08:00:00Z', 'November 20, 2026'],
    ['America/New_York', '2026-11-21T08:00:00Z', 'November 20, 2026'],
    ['Pacific/Honolulu', '2026-11-21T08:00:00Z', 'November 20, 2026'],
    ['America/Anchorage', '2026-11-21T08:00:00Z', 'November 20, 2026'],
    ['Europe/London', '2026-11-21T08:00:00Z', 'November 20, 2026'],
    ['Asia/Tokyo', '2026-11-21T08:00:00Z', 'November 20, 2026'],
    ['America/Los_Angeles', '2026-12-02T08:00:00Z', 'December 1, 2026'],
  ])('%s, deadline %s → %s', (tz, instant, want) => {
    process.env.TZ = tz;
    expect(formatAcceptByDate(new Date(instant))).toBe(new Date(want + ' 12:00 UTC').toLocaleDateString(undefined, { year: 'numeric', month: 'long', day: 'numeric', timeZone: 'UTC' }));
    expect(acceptByDay(new Date(instant)).toISOString().slice(0, 10)).toBe(
      new Date(new Date(instant).getTime() - 86_400_000).toISOString().slice(0, 10),
    );
  });
});

describe('deferredUntil', () => {
  const item = (over: Partial<PendingApprovalItem>): PendingApprovalItem => ({
    id: 'legal:sa:v1.2', kind: 'legal_acceptance', title: 'SA', summary: '', bodyText: '',
    documentUrl: '', documentSha256: '', version: 'v1.2', blocking: 'Fleet', required: true,
    deferrableUntil: '', approveAllowed: true, ...over,
  });
  const now = new Date('2026-11-01T00:00:00Z');

  it('future instant on a required item → that instant', () => {
    expect(deferredUntil(item({ deferrableUntil: '2026-11-21T08:00:00Z' }), now)?.toISOString()).toBe('2026-11-21T08:00:00.000Z');
  });
  it('absent (Fleet says blocking now), passed, unparsable or informational → null', () => {
    expect(deferredUntil(item({}), now)).toBeNull();
    expect(deferredUntil(item({ deferrableUntil: '2026-10-01T08:00:00Z' }), now)).toBeNull();
    expect(deferredUntil(item({ deferrableUntil: 'soon' }), now)).toBeNull();
    expect(deferredUntil(item({ required: false, deferrableUntil: '2026-11-21T08:00:00Z' }), now)).toBeNull();
  });
});
