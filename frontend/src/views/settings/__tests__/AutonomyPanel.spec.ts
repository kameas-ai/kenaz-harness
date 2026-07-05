import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';

import AutonomyPanel from '@/views/settings/AutonomyPanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import {
  emptyAutonomyLayer,
  type AutonomyLayer,
} from '@/lib/types';

function buildClient(initial: AutonomyLayer) {
  let layer = initial;
  const setAutonomy = vi.fn(async (l: AutonomyLayer) => {
    layer = l;
  });
  const getAutonomy = vi.fn(async () => layer);
  return {
    client: createFakeHarnessClient({
      settings: {
        get: async () => ({
          schemaVersion: 1,
          lastRoute: '/sessions',
          theme: 'system',
          accent: 'default',
          windowSize: { width: 1, height: 1 },
          memoryEnabled: false,
          confirmEachDisabled: false,
        }),
        set: async () => undefined,
        loadRoute: async () => '/sessions',
        saveRoute: async () => undefined,
        logRouteChange: async () => undefined,
        loadTheme: async () => 'system' as const,
        saveTheme: async () => undefined,
        getMemory: async () => false,
        setMemory: async () => undefined,
        getWebSearch: async () => false,
        setWebSearch: async () => undefined,
        getBash: async () => false,
        setBash: async () => undefined,
        getSaveArtifact: async () => true,
        setSaveArtifact: async () => undefined,
        getMaxAgentTurns: async () => 0,
        setMaxAgentTurns: async () => undefined,
        getPermissionMode: async () => 'normal' as const,
        setPermissionMode: async () => undefined,
        getPermissionCacheDangerousOps: async () => false,
        setPermissionCacheDangerousOps: async () => undefined,
        getBashAllowlistMigrated: async () => false,
        setBashAllowlistMigrated: async () => undefined,
        getPermissionsMigrationToastShown: async () => false,
        setPermissionsMigrationToastShown: async () => undefined,
        getCedarStrictCredentialMode: async () => false,
        setCedarStrictCredentialMode: async () => undefined,
        getFSRequestAccessEnabled: async () => true,
        setFSRequestAccessEnabled: async () => undefined,
        getAutonomy,
        setAutonomy,
      } as any,
    }),
    setAutonomy,
    getAutonomy,
  };
}

function mountWith(initial: AutonomyLayer) {
  const ctx = buildClient(initial);
  const wrapper = mount(AutonomyPanel, {
    props: { layerOverride: initial, skipFetch: true },
    global: { provide: { [HarnessClientKey as symbol]: ctx.client } },
  });
  return { wrapper, ...ctx };
}

describe('AutonomyPanel', () => {
  it('renders the three primary tier buttons', async () => {
    const { wrapper } = mountWith(emptyAutonomyLayer());
    await flushPromises();
    expect(wrapper.find('[data-testid="autonomy-tier-strict"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="autonomy-tier-default"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="autonomy-tier-autonomous"]').exists()).toBe(true);
  });

  it('expands to all five tiers when "Show all" clicked', async () => {
    const { wrapper } = mountWith(emptyAutonomyLayer());
    await wrapper.find('[data-testid="autonomy-show-all-tiers"]').trigger('click');
    expect(wrapper.find('[data-testid="autonomy-tier-cautious"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="autonomy-tier-bold"]').exists()).toBe(true);
  });

  it('persists tier selection through setAutonomy', async () => {
    const { wrapper, setAutonomy } = mountWith(emptyAutonomyLayer());
    await wrapper.find('[data-testid="autonomy-tier-default"]').trigger('click');
    await flushPromises();
    expect(setAutonomy).toHaveBeenCalled();
    const last = setAutonomy.mock.calls.at(-1)![0] as AutonomyLayer;
    expect(last.level).toBe('default');
  });

  it('shows Custom badge when an override exists', async () => {
    const layer: AutonomyLayer = {
      level: 'default',
      overrides: { maxIterations: 5 },
    };
    const { wrapper } = mountWith(layer);
    await flushPromises();
    expect(wrapper.find('[data-testid="autonomy-custom-label"]').exists()).toBe(true);
  });

  it('clears an override via the Reset button', async () => {
    const layer: AutonomyLayer = {
      level: 'default',
      overrides: { maxIterations: 5 },
    };
    const { wrapper, setAutonomy } = mountWith(layer);
    await wrapper.find('[data-testid="autonomy-advanced-toggle"]').trigger('click');
    await flushPromises();
    await wrapper
      .find('[data-testid="autonomy-knob-reset-maxIterations"]')
      .trigger('click');
    await flushPromises();
    const last = setAutonomy.mock.calls.at(-1)![0] as AutonomyLayer;
    expect(last.overrides.maxIterations).toBeUndefined();
  });
});
