/**
 * SettingsView.servedAgents.spec.ts — served-mode Settings must render
 * AgentsView (contracts/agents-served-rpc.md), not just the Fleet
 * telemetry panel + a blanket "unavailable" notice.
 *
 * Mirrors the served-mode-flag mocking pattern from
 * views/agentgraph/__tests__/agentgraph.served.test.ts.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { ref } from 'vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { AgentProfileSummary } from '@/lib/types';

let servedModeFlag = true;
vi.mock('@/lib/useServedMode', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/useServedMode')>();
  return {
    ...actual,
    isServedMode: () => servedModeFlag,
    useServedMode: () => ref(servedModeFlag),
  };
});

import SettingsView from '@/views/settings/SettingsView.vue';

const summary: AgentProfileSummary = {
  id: 'explore',
  name: 'Explore',
  description: 'Read-only exploration.',
  model: 'anthropic/claude-sonnet-4.5',
  mergePolicy: 'auto',
  bundled: true,
};

function fakeClient(overrides: Partial<ReturnType<typeof createFakeHarnessClient>['agents']> = {}) {
  return createFakeHarnessClient({
    agents: {
      listProfiles: async () => [summary],
      loadProfile: async () => ({ ...summary, autonomyTier: 'default', allowedTools: [], deniedTools: [] }),
      saveProfile: async () => undefined,
      deleteProfile: async () => undefined,
      ...overrides,
    },
  });
}

describe('SettingsView in served mode', () => {
  it('renders AgentsView alongside Fleet telemetry, not the desktop-only wall alone', async () => {
    servedModeFlag = true;
    const client = fakeClient();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    expect(w.find('[data-testid="agents-view"]').exists()).toBe(true);
    expect(w.text()).toContain('Explore');
    // The residual notice must name Agent Profiles as covered, not just Fleet.
    expect(w.text()).toContain('Agent Profiles');
  });

  it('calls Agents_* through the client, not a desktop-only path', async () => {
    servedModeFlag = true;
    const listProfiles = vi.fn().mockResolvedValue([summary]);
    const client = fakeClient({ listProfiles });
    mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(listProfiles).toHaveBeenCalledTimes(1);
  });

  it('desktop mode still renders the full settings form, unaffected', async () => {
    servedModeFlag = false;
    const client = fakeClient();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(w.find('[data-testid="settings-form"]').exists()).toBe(true);
    expect(w.find('[data-testid="agents-view"]').exists()).toBe(false);
  });
});
