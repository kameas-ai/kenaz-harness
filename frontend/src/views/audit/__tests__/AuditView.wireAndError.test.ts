/**
 * Dogfood 2026-10-08 P1 — Audit Log was empty by default for every user.
 *
 * (1) richFilter sent date-only since/until strings that the Go side
 *     (time.Time) could not decode, so every Audit_Filter call rejected;
 * (2) refresh() turned that rejection into an empty list — a failed
 *     query rendered as a clean, empty compliance trail.
 *
 * These tests drive the real component: the filter call must carry
 * RFC3339 bounds, and a rejected call must render a visible error while
 * keeping the previously-shown entries.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import AuditView from '@/views/audit/AuditView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { AuditEntry, AuditFilterQuery } from '@/lib/types';

const seed: AuditEntry[] = [
  {
    id: 'e1',
    timestamp: '2026-10-08T00:50:00Z',
    category: 'STORAGE',
    subject: 'fleet.config.applied',
  },
];

function mountWith(filter: (q: AuditFilterQuery) => Promise<AuditEntry[]>) {
  const client = createFakeHarnessClient({
    audit: {
      listEntries: async () => [],
      verifyEntry: async () => true,
      verifyChain: async () => ({ verified: true, rows_checked: 0 }),
      filter,
      listSavedQueries: async () => [],
      saveQuery: async () => undefined,
      deleteQuery: async () => undefined,
      export: async () => '/tmp/x.jsonl',
      bulkPurge: async () => undefined,
      startStream: async () => 'sub',
      stopStream: async () => undefined,
    },
  });
  return mount(AuditView, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
}

describe('AuditView wire shape (dogfood 2026-10-08 P1)', () => {
  it('sends the default Since as an RFC3339 start-of-day bound', async () => {
    const spy = vi.fn(async (_q: AuditFilterQuery) => seed);
    mountWith(spy);
    await flushPromises();
    expect(spy).toHaveBeenCalled();
    const q = spy.mock.calls[0][0];
    expect(q.since).toMatch(/^\d{4}-\d{2}-\d{2}T00:00:00Z$/);
    expect(q.until).toBeUndefined();
  });

  it('typed dates go out as RFC3339; a partial date sends no bound', async () => {
    const spy = vi.fn(async (_q: AuditFilterQuery) => seed);
    const w = mountWith(spy);
    await flushPromises();
    const since = w.get('input[placeholder="YYYY-MM-DD"]');
    const until = w.get('input[placeholder="YYYY-MM-DD (open)"]');

    await since.setValue('2026-10-0');
    await flushPromises();
    expect(spy.mock.calls.at(-1)![0].since).toBeUndefined();

    await since.setValue('2026-10-08');
    await until.setValue('2026-10-08');
    await flushPromises();
    const last = spy.mock.calls.at(-1)![0];
    expect(last.since).toBe('2026-10-08T00:00:00Z');
    expect(last.until).toBe('2026-10-08T23:59:59.999999999Z');
  });
});

describe('AuditView error state (dogfood 2026-10-08 P1)', () => {
  it('a rejected query renders an error and keeps the previous entries', async () => {
    let fail = false;
    const filter = vi.fn(async (_q: AuditFilterQuery) => {
      if (fail) throw new Error('error parsing arguments');
      return seed;
    });
    const w = mountWith(filter);
    await flushPromises();
    expect(w.text()).toContain('fleet.config.applied');
    expect(w.find('[data-testid="audit-load-error"]').exists()).toBe(false);

    fail = true;
    await w.get('input[type="search"]').setValue('x');
    await flushPromises();

    const err = w.get('[data-testid="audit-load-error"]');
    expect(err.text()).toContain('Audit query failed: error parsing arguments');
    // Previous answer is preserved; the empty-state copy never appears.
    expect(w.text()).toContain('fleet.config.applied');
    expect(w.text()).not.toContain('No audit entries match');

    // Retry clears the error once the backend answers.
    fail = false;
    await w.get('[data-testid="audit-load-retry"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="audit-load-error"]').exists()).toBe(false);
  });

  it('a rejected first query shows the error, not the empty-state copy', async () => {
    const w = mountWith(async () => {
      throw new Error('boom');
    });
    await flushPromises();
    expect(w.get('[data-testid="audit-load-error"]').text()).toContain('boom');
    expect(w.text()).not.toContain('No audit entries match');
  });
});
