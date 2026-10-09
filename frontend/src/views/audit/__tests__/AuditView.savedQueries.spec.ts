import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import AuditView from '@/views/audit/AuditView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { AuditEntry, AuditFilterQuery, SavedAuditQuery } from '@/lib/types';

// audit-that-tells-the-truth-01PMZA10 UNIT-6 (WP08): AuditView.vue:179-180
// used to keep only element [0] of a saved query's `kinds` and
// `actor_ids` on load (applySavedQuery), and the save path re-narrowed
// symmetrically — so a two-kind, two-actor saved query was silently
// destroyed on the very first load→save round trip while the UI
// reported it restored (no error, no warning). This file drives that
// exact round trip through the real component (select the saved query
// from its dropdown, click Save) and asserts BOTH terms survive.

const twoTermQuery: SavedAuditQuery = {
  id: 'sq-multi',
  name: 'multi-term',
  query: {
    kinds: ['LLM', 'MCP'],
    actor_ids: ['actor-a', 'actor-b'],
    free_text: 'needle',
    verbose: true,
  },
  created_at: '2026-08-20T00:00:00Z',
};

function provide(
  seedEntries: AuditEntry[] = [],
  saveQuerySpy = vi.fn(async (_q: SavedAuditQuery) => undefined),
) {
  const client = createFakeHarnessClient({
    audit: {
      listEntries: async () => seedEntries,
      verifyEntry: async () => true,
      verifyChain: async () => ({ verified: true, rows_checked: 0 }),
      filter: async () => seedEntries,
      listSavedQueries: async () => [twoTermQuery],
      saveQuery: saveQuerySpy,
      deleteQuery: async () => undefined,
      export: async () => '/tmp/test.csv',
      bulkPurge: async () => undefined,
      startStream: async () => 'fake-audit-sub',
      stopStream: async () => undefined,
    },
  });
  return { client, saveQuerySpy };
}

describe('AuditView saved-query round trip (WP08 truncation fix)', () => {
  it('applying a two-kind, two-actor saved query keeps every term in the UI state', async () => {
    const { client } = provide();
    const w = mount(AuditView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    const select = w.find('select[aria-label]');
    expect(select.exists()).toBe(true);
    // Simulate selecting the saved query from the "Saved queries…" dropdown.
    const savedQuerySelect = w.findAll('select').find((s) =>
      s.findAll('option').some((o) => o.text() === 'multi-term'),
    );
    expect(savedQuerySelect).toBeTruthy();
    await savedQuerySelect!.setValue('sq-multi');
    await flushPromises();

    // The Kind multi-select must now show BOTH LLM and MCP selected —
    // not just the first one.
    const kindSelect = w.get('select[multiple]');
    const selectedOptions = kindSelect.findAll('option').filter((o) => (o.element as HTMLOptionElement).selected);
    const selectedValues = selectedOptions.map((o) => o.element.getAttribute('value'));
    expect(selectedValues).toEqual(expect.arrayContaining(['LLM', 'MCP']));
    expect(selectedValues).toHaveLength(2);

    // The Actor field must reflect both actor ids.
    const actorInput = w.find('input[placeholder="emitter-id, emitter-id2"]');
    expect(actorInput.exists()).toBe(true);
    expect((actorInput.element as HTMLInputElement).value).toContain('actor-a');
    expect((actorInput.element as HTMLInputElement).value).toContain('actor-b');
  });

  it('re-saving an applied two-kind, two-actor query persists BOTH terms, not just the first', async () => {
    const saveQuerySpy = vi.fn(async (_q: SavedAuditQuery) => undefined);
    const { client } = provide([], saveQuerySpy);
    const w = mount(AuditView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    const savedQuerySelect = w.findAll('select').find((s) =>
      s.findAll('option').some((o) => o.text() === 'multi-term'),
    );
    await savedQuerySelect!.setValue('sq-multi');
    await flushPromises();

    // Name the re-save and click Save.
    const nameInput = w.find('input[placeholder="Save current filter as…"]');
    await nameInput.setValue('resaved');
    const saveButton = w.findAll('button').find((b) => b.text() === 'Save');
    expect(saveButton).toBeTruthy();
    await saveButton!.trigger('click');
    await flushPromises();

    expect(saveQuerySpy).toHaveBeenCalledTimes(1);
    const persisted = saveQuerySpy.mock.calls[0][0];
    expect(persisted.query.kinds).toEqual(expect.arrayContaining(['LLM', 'MCP']));
    expect(persisted.query.kinds).toHaveLength(2);
    expect(persisted.query.actor_ids).toEqual(expect.arrayContaining(['actor-a', 'actor-b']));
    expect(persisted.query.actor_ids).toHaveLength(2);
  });

  // Audit rows record no emitter, so an applied actor term would match
  // nothing and empty the trail. A loaded saved query's actor_ids are
  // ignored for the query (with a notice) but kept for re-saving.
  it('loading a saved query with actor_ids queries without them, says so, and still lists rows', async () => {
    const seed: AuditEntry[] = [
      { id: 'r1', timestamp: '2026-10-08T00:50:00Z', category: 'LLM', subject: 'llm.request.started' },
    ];
    const filterSpy = vi.fn(async (_q: AuditFilterQuery) => seed);
    const client = createFakeHarnessClient({
      audit: {
        listEntries: async () => seed,
        verifyEntry: async () => true,
        verifyChain: async () => ({ verified: true, rows_checked: 0 }),
        filter: filterSpy,
        listSavedQueries: async () => [{ ...twoTermQuery, query: { ...twoTermQuery.query, kinds: ['LLM'], free_text: undefined } }],
        saveQuery: async () => undefined,
        deleteQuery: async () => undefined,
        export: async () => '/tmp/test.csv',
        bulkPurge: async () => undefined,
        startStream: async () => 'fake-audit-sub',
        stopStream: async () => undefined,
      },
    });
    const w = mount(AuditView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(w.find('[data-testid="audit-actor-ignored"]').exists()).toBe(false);

    const savedQuerySelect = w.findAll('select').find((s) =>
      s.findAll('option').some((o) => o.text() === 'multi-term'),
    );
    await savedQuerySelect!.setValue('sq-multi');
    await flushPromises();

    const last = filterSpy.mock.calls.at(-1)![0];
    expect(last.kinds).toEqual(['LLM']);
    expect(last.actor_ids).toBeUndefined();
    expect(w.get('[data-testid="audit-actor-ignored"]').text()).toContain(
      "actor filter can't be applied yet — ignored",
    );
    expect(w.findAll('[data-testid="audit-row"]')).toHaveLength(1);
    expect(w.text()).not.toContain('No audit entries match');
  });
});
