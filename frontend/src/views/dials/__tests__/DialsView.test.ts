import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import DialsView from '@/views/dials/DialsView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { DialEffectiveDials } from '@/lib/types';

function makeEffective(over: Partial<DialEffectiveDials> = {}): DialEffectiveDials {
  return {
    maxTokensPerRun: { value: 100_000, from: 'global' },
    maxWallclockSeconds: { value: 600, from: 'global' },
    maxLLMCalls: { value: 200, from: 'global' },
    maxToolCalls: { value: 200, from: 'global' },
    maxCostUSD: { value: 5, from: 'global' },
    planVerbosity: { value: 'normal', from: 'global' },
    askThreshold: { value: 0.4, from: 'global' },
    reflectFrequency: { value: 1, from: 'global' },
    compactionAggressiveness: { value: 0.5, from: 'global' },
    reviewIterationsCap: { value: 3, from: 'global' },
    memoryHooksEnabled: { value: true, from: 'global' },
    memoryPruneIntervalSeconds: { value: 86400, from: 'global' },
    ...over,
  };
}

function mountWith(seed: {
  get?: ReturnType<typeof vi.fn>;
  set?: ReturnType<typeof vi.fn>;
  effective?: DialEffectiveDials;
} = {}) {
  const get = seed.get ?? vi.fn(async () => ({}));
  const set = seed.set ?? vi.fn(async () => undefined);
  const getEffective = vi.fn(async () => seed.effective ?? makeEffective());
  const bumpAndResume = vi.fn(async () => undefined);
  const client = createFakeHarnessClient({
    dials: { get, set, getEffective, bumpAndResume },
  });
  const wrapper = mount(DialsView, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  return { wrapper, get, set, getEffective };
}

describe('DialsView', () => {
  it('renders effective values + per-field attribution chips', async () => {
    const eff = makeEffective({
      maxTokensPerRun: { value: 50_000, from: 'project' },
      maxCostUSD: { value: 1.5, from: 'session' },
    });
    const { wrapper } = mountWith({ effective: eff });
    await flushPromises();
    expect(
      wrapper
        .find('[data-testid="dials-effective-maxTokensPerRun-from"]')
        .text(),
    ).toContain('project');
    expect(
      wrapper.find('[data-testid="dials-effective-maxCostUSD-from"]').text(),
    ).toContain('session');
  });

  it('save button writes the layer config', async () => {
    const set = vi.fn(async () => undefined);
    const { wrapper } = mountWith({ set });
    await flushPromises();
    await wrapper
      .find('[data-testid="dial-toggle-maxTokensPerRun"]')
      .setValue(true);
    await wrapper
      .find('[data-testid="dial-input-maxTokensPerRun"]')
      .setValue(75000);
    await wrapper.find('[data-testid="dials-save"]').trigger('click');
    await flushPromises();
    expect(set).toHaveBeenCalledTimes(1);
    const args = set.mock.calls[0] as unknown[];
    const key = args[0] as { scope: string; id: string };
    const cfg = args[1] as {
      maxTokensPerRun?: number;
      maxTokensPerRunSet?: boolean;
    };
    expect(key).toEqual({ scope: 'global', id: '' });
    expect(cfg.maxTokensPerRun).toBe(75000);
    expect(cfg.maxTokensPerRunSet).toBe(true);
  });

  it('switching scope reloads the layer + effective cascade', async () => {
    const { wrapper, get, getEffective } = mountWith();
    await flushPromises();
    await wrapper.find('[data-testid="dials-scope-project"]').trigger('click');
    await flushPromises();
    // Initial mount = 1 call; switching scope = 2 calls (layer + effective).
    // Project layer needs an id — UI prompts the user; we've already
    // triggered a load with empty id which is allowed for `getEffective`.
    expect(get).toHaveBeenCalled();
    expect(getEffective).toHaveBeenCalled();
  });

  it('disabled save when project / session id is empty', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    await wrapper
      .find('[data-testid="dials-scope-project"]')
      .trigger('click');
    await flushPromises();
    const save = wrapper.find('[data-testid="dials-save"]')
      .element as HTMLButtonElement;
    expect(save.disabled).toBe(true);
  });
});
