import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type {
  CompactionConfig,
  CompactionEffectiveConfig,
  CompactionLayer,
  CompactionManualResult,
} from '@/lib/types';

vi.mock('vue-router', () => ({
  useRoute: () => ({ params: {}, query: {} }),
  useRouter: () => ({ push: vi.fn() }),
}));

import CompactionSettings from '@/views/compaction/CompactionSettings.vue';

interface MountOpts {
  effective?: CompactionEffectiveConfig;
  config?: CompactionConfig;
  customStrategies?: Array<{ graphId: string; name: string }>;
  triggerImpl?: () => Promise<CompactionManualResult>;
  setImpl?: (
    layer: CompactionLayer,
    scopeID: string,
    cfg: CompactionConfig,
  ) => Promise<void>;
  sessionId?: string;
}

function defaultEffective(): CompactionEffectiveConfig {
  return {
    config: {
      sites: {
        pre_call: {
          enabled: true,
          strategy: 'drop_oldest',
          preCallThreshold: 0.85,
          maxRecursionDepth: 2,
        },
        post_tool: {
          enabled: true,
          strategy: 'drop_oldest',
          toolResultMaxBytes: 16 * 1024,
        },
        manual: {
          enabled: true,
          strategy: 'summary',
        },
      },
    },
    attribution: {
      pre_call: { strategy: 'global' },
      post_tool: { strategy: 'global' },
      manual: { strategy: 'global' },
    },
  };
}

function mountWith(opts: MountOpts = {}) {
  const getEffective = vi.fn(async () => opts.effective ?? defaultEffective());
  const getConfig = vi.fn(async () => opts.config ?? { sites: {} });
  const setConfig = vi.fn(opts.setImpl ?? (async () => undefined));
  const triggerManualCompaction = vi.fn(
    opts.triggerImpl ??
      (async () =>
        ({
          strategy: 'summary',
          bytesSaved: 1234,
        }) satisfies CompactionManualResult),
  );
  const listCustomStrategies = vi.fn(async () => opts.customStrategies ?? []);

  const client = createFakeHarnessClient({
    compaction: {
      getConfig,
      getEffective,
      setConfig,
      triggerManualCompaction,
      listCustomStrategies,
    },
  });

  const wrapper = mount(CompactionSettings, {
    props: { sessionId: opts.sessionId },
    global: {
      provide: { [HarnessClientKey as symbol]: client },
      stubs: {
        CanvasHead: {
          template: '<div class="canvas-head"><slot name="trailing" /></div>',
        },
      },
    },
  });
  return {
    wrapper,
    getConfig,
    getEffective,
    setConfig,
    triggerManualCompaction,
    listCustomStrategies,
  };
}

describe('CompactionSettings', () => {
  it('renders all three sites with their strategy pickers', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    expect(wrapper.find('[data-testid="compaction-site-pre_call"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="compaction-site-post_tool"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="compaction-site-manual"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="compaction-pre_call-strategy"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="compaction-post_tool-strategy"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="compaction-manual-strategy"]').exists()).toBe(true);
  });

  it('shows the effective strategy + attribution badge per site', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    const pre = wrapper.find('[data-testid="compaction-effective-pre_call"]');
    expect(pre.exists()).toBe(true);
    expect(pre.text()).toContain('Drop oldest');
    expect(
      wrapper
        .find('[data-testid="compaction-attribution-pre_call-strategy"]')
        .text(),
    ).toContain('global');
  });

  it('updates and persists site config via Save', async () => {
    const { wrapper, setConfig, getConfig, getEffective } = mountWith();
    await flushPromises();

    const select = wrapper.find('[data-testid="compaction-pre_call-strategy"]')
      .element as HTMLSelectElement;
    select.value = 'summary';
    await wrapper.find('[data-testid="compaction-pre_call-strategy"]').trigger('change');

    await wrapper.find('[data-testid="compaction-save"]').trigger('click');
    await flushPromises();

    expect(setConfig).toHaveBeenCalledTimes(1);
    const [layer, scopeID, cfg] = setConfig.mock.calls[0]!;
    expect(layer).toBe('global');
    expect(scopeID).toBe('');
    const cfgTyped = cfg as CompactionConfig;
    expect(cfgTyped.sites?.pre_call?.strategy).toBe('summary');
    // After save the view re-fetches both layer config + effective.
    expect(getConfig).toHaveBeenCalledTimes(2);
    expect(getEffective).toHaveBeenCalledTimes(2);
  });

  it('disables the manual button when no sessionId is provided', async () => {
    const { wrapper } = mountWith({ sessionId: undefined });
    await flushPromises();
    expect(wrapper.find('[data-testid="compaction-trigger-now"]').exists()).toBe(false);
  });

  it('fires triggerManualCompaction when the manual button is clicked', async () => {
    const { wrapper, triggerManualCompaction } = mountWith({ sessionId: 's1' });
    await flushPromises();
    const btn = wrapper.find('[data-testid="compaction-trigger-now"]');
    expect(btn.exists()).toBe(true);
    await btn.trigger('click');
    await flushPromises();
    expect(triggerManualCompaction).toHaveBeenCalledTimes(1);
    expect(triggerManualCompaction.mock.calls[0]![0]).toBe('s1');
    expect(
      wrapper.find('[data-testid="compaction-trigger-summary"]').text(),
    ).toContain('Compacted via summary');
  });

  it('lists custom strategies when strategy = custom_subgraph', async () => {
    const { wrapper } = mountWith({
      effective: defaultEffective(),
      config: {
        sites: {
          pre_call: { enabled: true, strategy: 'custom_subgraph' },
        },
      },
      customStrategies: [
        { graphId: 'g1', name: 'My Custom Compactor' },
      ],
    });
    await flushPromises();
    const select = wrapper.find('[data-testid="compaction-pre_call-custom-graph"]');
    expect(select.exists()).toBe(true);
    expect(select.text()).toContain('My Custom Compactor');
  });

  it('renders skipped reason when manual trigger reports skipped', async () => {
    const { wrapper } = mountWith({
      sessionId: 's1',
      triggerImpl: async () => ({
        strategy: 'summary',
        bytesSaved: 0,
        skipped: true,
        reason: 'site disabled',
      }),
    });
    await flushPromises();
    await wrapper.find('[data-testid="compaction-trigger-now"]').trigger('click');
    await flushPromises();
    expect(
      wrapper.find('[data-testid="compaction-trigger-summary"]').text(),
    ).toContain('Skipped');
  });

  it('rejects save with empty scope id when layer requires it', async () => {
    const { wrapper, setConfig } = mountWith();
    await flushPromises();
    const layer = wrapper.find('[data-testid="compaction-layer-select"]')
      .element as HTMLSelectElement;
    layer.value = 'project';
    await wrapper.find('[data-testid="compaction-layer-select"]').trigger('change');
    await flushPromises();
    await wrapper.find('[data-testid="compaction-save"]').trigger('click');
    await flushPromises();
    expect(setConfig).not.toHaveBeenCalled();
    expect(wrapper.find('[data-testid="compaction-error"]').text()).toContain(
      'Project ID',
    );
  });
});
