import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';

import AutonomyPanel from '@/views/settings/AutonomyPanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import {
  emptyAutonomyLayer,
  type AutonomyLayer,
} from '@/lib/types';

function buildClient(
  initial: AutonomyLayer,
  opts: { maxAgentTurns?: number; fsRequestAccessEnabled?: boolean } = {},
) {
  let layer = initial;
  const setAutonomy = vi.fn(async (l: AutonomyLayer) => {
    layer = l;
  });
  const getAutonomy = vi.fn(async () => layer);
  const getMaxAgentTurns = vi.fn(async () => opts.maxAgentTurns ?? 0);
  const setMaxAgentTurns = vi.fn(async () => undefined);
  const getFSRequestAccessEnabled = vi.fn(
    async () => opts.fsRequestAccessEnabled ?? true,
  );
  const setFSRequestAccessEnabled = vi.fn(async () => undefined);
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
        getWebFetchEnabled: async () => false,
        setWebFetchEnabled: async () => undefined,
        getWebSearch: async () => false,
        setWebSearch: async () => undefined,
        getBash: async () => false,
        setBash: async () => undefined,
        getSaveArtifact: async () => true,
        setSaveArtifact: async () => undefined,
        getMaxAgentTurns,
        setMaxAgentTurns,
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
        getFSRequestAccessEnabled,
        setFSRequestAccessEnabled,
        getAutonomy,
        setAutonomy,
      } as any,
    }),
    setAutonomy,
    getAutonomy,
    getMaxAgentTurns,
    setMaxAgentTurns,
    getFSRequestAccessEnabled,
    setFSRequestAccessEnabled,
  };
}

function mountWith(
  initial: AutonomyLayer,
  fetchOpts: {
    skipFetch?: boolean;
    maxAgentTurns?: number;
    fsRequestAccessEnabled?: boolean;
  } = {},
) {
  const { skipFetch = true, ...clientOpts } = fetchOpts;
  const ctx = buildClient(initial, clientOpts);
  const wrapper = mount(AutonomyPanel, {
    props: { layerOverride: initial, skipFetch },
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

  // trust-surfaces-that-fire-01PMZ202 WP25 C2V-04: MaxAgentTurns and
  // FSRequestAccessEnabled previously had zero .vue callers. These
  // assert the panel actually calls the real branch-bearing bindings —
  // not just that a value round-trips through local component state.
  it('loads and persists the legacy max-agent-turns fallback', async () => {
    const { wrapper, getMaxAgentTurns, setMaxAgentTurns } = mountWith(
      emptyAutonomyLayer(),
      { skipFetch: false, maxAgentTurns: 12 },
    );
    await flushPromises();
    expect(getMaxAgentTurns).toHaveBeenCalled();
    const input = wrapper.find('[data-testid="autonomy-max-agent-turns"]');
    expect((input.element as HTMLInputElement).value).toBe('12');

    await input.setValue('7');
    await flushPromises();
    expect(setMaxAgentTurns).toHaveBeenCalledWith(7);
  });

  it('normalises a negative/non-numeric max-agent-turns input to zero', async () => {
    const { wrapper, setMaxAgentTurns } = mountWith(emptyAutonomyLayer(), {
      skipFetch: false,
      maxAgentTurns: 5,
    });
    await flushPromises();
    const input = wrapper.find('[data-testid="autonomy-max-agent-turns"]');
    await input.setValue('-3');
    await flushPromises();
    expect(setMaxAgentTurns).toHaveBeenCalledWith(0);
  });

  it('loads and persists the FS-request-access permission toggle', async () => {
    const { wrapper, getFSRequestAccessEnabled, setFSRequestAccessEnabled } =
      mountWith(emptyAutonomyLayer(), {
        skipFetch: false,
        fsRequestAccessEnabled: true,
      });
    await flushPromises();
    expect(getFSRequestAccessEnabled).toHaveBeenCalled();
    const toggle = wrapper.find(
      '[data-testid="autonomy-fs-request-access-toggle"]',
    );
    expect((toggle.element as HTMLInputElement).checked).toBe(true);

    await toggle.setValue(false);
    await flushPromises();
    expect(setFSRequestAccessEnabled).toHaveBeenCalledWith(false);
  });
});
