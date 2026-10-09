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
  it('a rejected query for a NEW filter renders an error and never shows the old filter’s rows', async () => {
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
    // The old rows are not an answer to the new filter, and the empty-state
    // copy never stands in for an error.
    expect(w.findAll('[data-testid="audit-row"]')).toHaveLength(0);
    expect(w.text()).not.toContain('No audit entries match');

    // Retry clears the error once the backend answers.
    fail = false;
    await w.get('[data-testid="audit-load-retry"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="audit-load-error"]').exists()).toBe(false);
    expect(w.text()).toContain('fleet.config.applied');
  });

  it('a slow earlier response never overwrites a later one', async () => {
    let releaseFirst!: (v: AuditEntry[]) => void;
    const later: AuditEntry[] = [
      { id: 'e2', timestamp: '2026-10-08T01:00:00Z', category: 'STORAGE', subject: 'later.answer' },
    ];
    let n = 0;
    const filter = vi.fn((_q: AuditFilterQuery) => {
      n++;
      if (n === 1) return new Promise<AuditEntry[]>((r) => { releaseFirst = r; });
      return Promise.resolve(later);
    });
    const w = mountWith(filter);
    await flushPromises();
    await w.get('input[type="search"]').setValue('later');
    await flushPromises();
    expect(w.text()).toContain('later.answer');

    releaseFirst(seed); // the stale first answer lands last
    await flushPromises();
    expect(w.text()).toContain('later.answer');
    expect(w.text()).not.toContain('fleet.config.applied');
  });

  it('after a purge, a failed refetch clears the purged rows', async () => {
    let fail = false;
    const filter = vi.fn(async (_q: AuditFilterQuery) => {
      if (fail) throw new Error('store busy');
      return seed;
    });
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
    const w = mount(AuditView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
      attachTo: document.body,
    });
    await flushPromises();
    await w.get('[data-testid="audit-row"] input[type="checkbox"]').setValue(true);
    await w.get('[data-testid="audit-purge-selected"]').trigger('click');
    fail = true;
    (document.querySelector('[data-testid="purge-modal-confirm"]') as HTMLButtonElement).click();
    await flushPromises();

    expect(w.get('[data-testid="audit-load-error"]').text()).toContain('store busy');
    expect(w.findAll('[data-testid="audit-row"]')).toHaveLength(0);
    w.unmount();
  });

  it('labels the date inputs as UTC days and disables the Actor filter with a reason', async () => {
    const w = mountWith(async () => seed);
    await flushPromises();
    expect(w.text()).toContain('Since (UTC day)');
    expect(w.text()).toContain('Until (UTC day)');
    const actor = w.get('[data-testid="audit-actor-input"]');
    expect(actor.attributes('disabled')).toBeDefined();
    expect(actor.attributes('title')).toContain('do not record which emitter');
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
