/**
 * auditDateFilters — unit tests (FR-001 correctness fix).
 *
 * Verifies that the default audit filter date range is always valid:
 *   defaultAuditUntil() >= defaultAuditSince()
 * i.e. Until is always empty (open-ended) or a date on/after Since.
 */
import { describe, it, expect } from 'vitest';
import { nDaysAgoISO, defaultAuditSince, defaultAuditUntil } from '@/lib/auditDateFilters';

describe('nDaysAgoISO', () => {
  it('returns a YYYY-MM-DD string', () => {
    const result = nDaysAgoISO(7, new Date('2026-07-15T12:00:00Z'));
    expect(result).toMatch(/^\d{4}-\d{2}-\d{2}$/);
  });

  it('returns exactly 7 days before the given date (UTC midnight)', () => {
    const anchor = new Date('2026-07-15T12:34:56Z');
    expect(nDaysAgoISO(7, anchor)).toBe('2026-07-08');
  });

  it('returns the same day for 0 days ago (today UTC midnight)', () => {
    const anchor = new Date('2026-07-15T23:59:59Z');
    expect(nDaysAgoISO(0, anchor)).toBe('2026-07-15');
  });

  it('handles month boundary correctly', () => {
    // July 3 − 7 days = June 26
    const anchor = new Date('2026-07-03T00:00:00Z');
    expect(nDaysAgoISO(7, anchor)).toBe('2026-06-26');
  });
});

describe('defaultAuditSince', () => {
  it('returns a YYYY-MM-DD string 7 days before now', () => {
    const anchor = new Date('2026-07-15T09:00:00Z');
    expect(defaultAuditSince(anchor)).toBe('2026-07-08');
  });
});

describe('defaultAuditUntil', () => {
  it('returns empty string (open-ended upper bound)', () => {
    expect(defaultAuditUntil()).toBe('');
  });
});

describe('FR-001 invariant: Until >= Since', () => {
  it('empty Until is always open-ended (>= any Since)', () => {
    // An empty string in the filter maps to undefined on the server,
    // which means "up to now" — always after any past Since date.
    const until = defaultAuditUntil();
    expect(until).toBe('');
    // If Until were a non-empty string it must be >= Since.
    // (vacuously satisfied since Until === '' here)
  });

  it('Since is a valid ISO date no later than today', () => {
    const anchor = new Date();
    const since = defaultAuditSince(anchor);
    const today = anchor.toISOString().slice(0, 10);
    // Since must be <= today (7 days in the past or earlier)
    expect(since <= today).toBe(true);
    // And no earlier than 8 days ago (within 1-day margin for UTC edge)
    const eightDaysAgo = nDaysAgoISO(8, anchor);
    expect(since >= eightDaysAgo).toBe(true);
  });
});
