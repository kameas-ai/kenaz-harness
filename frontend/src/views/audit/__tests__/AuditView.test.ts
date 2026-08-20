import { describe, it, expect } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import AuditView from '@/views/audit/AuditView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { AuditEntry } from '@/lib/types';

function provide(seedEntries: AuditEntry[] = []) {
  const client = createFakeHarnessClient({
    audit: {
      listEntries: async () => seedEntries,
      verifyEntry: async () => true,
      verifyChain: async () => ({ verified: true, rows_checked: 0 }),
      // audit-that-tells-the-truth-01PMZA10 UNIT-6 (WP07): AuditView's
      // seeded/historical fetch now goes through filter() (the complete
      // backend), not listEntries() — see AuditView.vue's refresh().
      filter: async () => seedEntries,
      listSavedQueries: async () => [],
      saveQuery: async () => undefined,
      deleteQuery: async () => undefined,
      export: async () => '/tmp/test.csv',
      bulkPurge: async () => undefined,
      startStream: async () => 'fake-audit-sub',
      stopStream: async () => undefined,
    },
  });
  return { client };
}

describe('AuditView (FR-001b numbered-section header)', () => {
  it('renders the canvas head with section number 05', async () => {
    const { client } = provide();
    const w = mount(AuditView, {
      global: {
        provide: {
          [HarnessClientKey as symbol]: client,
        },
      },
    });
    await flushPromises();
    expect(w.text()).toContain('05');
    expect(w.text()).toContain('AUDIT');
    expect(w.text()).toContain('Append-only event log');
  });

  it('renders an empty-state message when no entries match', async () => {
    const { client } = provide([]);
    const w = mount(AuditView, {
      global: {
        provide: {
          [HarnessClientKey as symbol]: client,
        },
      },
    });
    await flushPromises();
    expect(w.text()).toContain('No audit entries');
  });

  it('renders one EventStreamRow per seeded entry', async () => {
    const seed: AuditEntry[] = [
      {
        id: '01H',
        timestamp: '2026-04-25T00:00:00Z',
        category: 'LLM',
        subject: 'llm.request.started',
        trailing: 'a1b2',
      },
      {
        id: '02H',
        timestamp: '2026-04-25T00:01:00Z',
        category: 'MCP',
        subject: 'mcp.tool.invoked',
      },
    ];
    const { client } = provide(seed);
    const w = mount(AuditView, {
      global: {
        provide: {
          [HarnessClientKey as symbol]: client,
        },
      },
    });
    await flushPromises();

    expect(w.text()).toContain('llm.request.started');
    expect(w.text()).toContain('mcp.tool.invoked');
  });

  it('exposes a pause/resume toggle and verify-chain action', async () => {
    const { client } = provide();
    const w = mount(AuditView, {
      global: {
        provide: {
          [HarnessClientKey as symbol]: client,
        },
      },
    });
    await flushPromises();
    expect(w.text()).toContain('Pause');
    expect(w.text()).toContain('Verify chain');
  });

  it('uses only design tokens — no raw hex/rgba in rendered HTML', async () => {
    const { client } = provide();
    const w = mount(AuditView, {
      global: {
        provide: {
          [HarnessClientKey as symbol]: client,
        },
      },
    });
    await flushPromises();
    const html = w.html();
    expect(html).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(html).not.toMatch(/rgba?\s*\(/i);
  });
});
