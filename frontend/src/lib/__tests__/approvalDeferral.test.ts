/**
 * approvalDeferral — Fleet's deferrable_until instants are 08:00Z (midnight
 * starting the next US-Pacific day). "Accept by" must name the last full
 * local day that ends by the deadline, never the instant's UTC date.
 */
import { afterEach, describe, expect, it } from 'vitest';
import { acceptByDay, deferredUntil } from '@/lib/approvalDeferral';
import type { PendingApprovalItem } from '@/lib/types';

const ORIGINAL_TZ = process.env.TZ;
afterEach(() => {
  process.env.TZ = ORIGINAL_TZ;
});

const ymd = (d: Date) => [d.getFullYear(), d.getMonth() + 1, d.getDate()];

describe('acceptByDay', () => {
  it.each([
    ['America/Los_Angeles', '2026-11-21T08:00:00Z', [2026, 11, 20]],
    ['America/New_York', '2026-11-21T08:00:00Z', [2026, 11, 20]],
    ['Europe/London', '2026-11-21T08:00:00Z', [2026, 11, 20]],
    ['Asia/Tokyo', '2026-11-21T08:00:00Z', [2026, 11, 20]],
    ['America/Los_Angeles', '2026-12-02T08:00:00Z', [2026, 12, 1]],
    ['America/New_York', '2026-12-02T08:00:00Z', [2026, 12, 1]],
  ])('%s, deadline %s → %j', (tz, instant, want) => {
    process.env.TZ = tz;
    const deadline = new Date(instant);
    const day = acceptByDay(deadline);
    expect(ymd(day)).toEqual(want);
    // The whole named day ends at or before the deadline.
    const endOfDay = new Date(day.getFullYear(), day.getMonth(), day.getDate() + 1);
    expect(endOfDay.getTime()).toBeLessThanOrEqual(deadline.getTime());
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
