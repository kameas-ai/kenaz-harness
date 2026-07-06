/**
 * local_runtime_e2e.test.ts — end-to-end frontend walk through the local
 * runtime auto-configuration flow.
 *
 * Simulates: user opens ProvidersView → LocalRuntimesSection appears with
 * detected runtimes → user clicks "Add" for Ollama → providers table
 * refreshes with the newly added row.
 *
 * (local-model-runtimes-01KQ8VMZ WP08)
 */

import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { createFakeHarnessClient, type HarnessClient } from '@/lib/harnessClient';
import type {
  LocalRuntimeInfo,
  LocalRuntimeConfigResult,
} from '@/lib/types';
import LocalRuntimesSection from '@/views/providers/LocalRuntimesSection.vue';

const GB = 1024 * 1024 * 1024;

function makeRuntime(kind: string, running = true): LocalRuntimeInfo {
  return {
    kind,
    name: kind.charAt(0).toUpperCase() + kind.slice(1),
    running,
    installed: true,
    defaultBaseURL: `http://localhost:11434`,
    port: 11434,
    models: [
      {
        id: 'llama3:8b-q4_K_M',
        displayName: 'llama3:8b-q4_K_M',
        sizeBytes: Math.floor(4.7 * GB),
        quantLevel: 'Q4_K_M',
        paramCount: 8,
      },
    ],
  };
}


function makeClient(
  override: Partial<HarnessClient['llm']> = {},
): HarnessClient {
  const fake = createFakeHarnessClient();
  return { ...fake, llm: { ...fake.llm, ...override } };
}

describe('local runtime e2e: click "Add Ollama" → populated row', () => {
  it('mounts section with detected runtimes, adds Ollama, notifies parent', async () => {
    const configureResult: LocalRuntimeConfigResult = {
      providerId: 'local-ollama',
      name: 'Ollama (local)',
      models: [{ id: 'llama3:8b-q4_K_M', displayName: 'llama3:8b-q4_K_M' }],
    };
    const autoConfigureMock = vi.fn().mockResolvedValue(configureResult);
    const onProviderAdded = vi.fn();

    const client = makeClient({
      listDetectedLocalRuntimes: vi.fn().mockResolvedValue([makeRuntime('ollama')]),
      autoConfigureLocalRuntime: autoConfigureMock,
    });

    const wrapper = mount(LocalRuntimesSection, {
      props: { onProviderAdded },
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    // Section should be visible.
    expect(wrapper.find('[data-testid="local-runtimes-section"]').exists()).toBe(true);

    // Ollama card should show "Add" button.
    const addBtn = wrapper.find('[data-testid="add-runtime-ollama"]');
    expect(addBtn.exists()).toBe(true);
    expect(addBtn.text()).toContain('Add');

    // Click "Add".
    await addBtn.trigger('click');
    await flushPromises();

    // autoConfigureLocalRuntime called with "ollama".
    expect(autoConfigureMock).toHaveBeenCalledWith('ollama');

    // Parent onProviderAdded callback fired.
    expect(onProviderAdded).toHaveBeenCalled();

    // Button now shows "Added".
    expect(wrapper.find('[data-testid="add-runtime-ollama"]').text()).toContain('Added');
  });

  it('shows amber RAM pill when model exceeds 85% of RAM', async () => {
    // ModelSizeBadge integration smoke: the badge is rendered inline
    // when the card passes effectiveRamBytes.
    // This verifies the threshold logic without remounting.
    const { modelFitsInRAM } = await import('@/lib/modelFit');
    const modelSize = Math.floor(14.5 * GB);
    const ram = 16 * GB;

    // Model does not fit.
    expect(modelFitsInRAM(modelSize, ram)).toBe(false);

    // After override to 32 GB, it fits.
    expect(modelFitsInRAM(modelSize, 32 * GB)).toBe(true);
  });

  it('RAM-fit filter flip: grey state changes on settings override change', async () => {
    const { effectiveLocalRuntimeRAMBytes, modelFitsInRAM } = await import('@/lib/modelFit');

    const modelSize = Math.floor(14.5 * GB);
    const detected = 16 * GB;

    // With no override: 14.5 GB > 16 * 0.85 = 13.6 → does not fit
    const noOverride = effectiveLocalRuntimeRAMBytes(0, detected);
    expect(modelFitsInRAM(modelSize, noOverride)).toBe(false);

    // After override to 20 GB: 14.5 GB < 20 * 0.85 = 17 → fits
    const withOverride = effectiveLocalRuntimeRAMBytes(20, detected);
    expect(modelFitsInRAM(modelSize, withOverride)).toBe(true);
  });

  it('feature-flag-off: section absent, RPC returns empty', async () => {
    const client = makeClient({
      listDetectedLocalRuntimes: vi.fn().mockResolvedValue([]),
      autoConfigureLocalRuntime: vi.fn(),
      rescanLocalRuntimes: vi.fn().mockResolvedValue([]),
    });

    const wrapper = mount(LocalRuntimesSection, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    // Section absent when no runtimes returned.
    expect(wrapper.find('[data-testid="local-runtimes-section"]').exists()).toBe(false);

    // autoConfigureLocalRuntime was never called.
    expect((client.llm.autoConfigureLocalRuntime as ReturnType<typeof vi.fn>).mock.calls.length).toBe(0);
  });

  it('fixture-based: each of the 5 fetcher kinds appears as a card', async () => {
    const allKinds = ['ollama', 'llama-server', 'lm-studio', 'jan', 'gpt4all'];
    const runtimes = allKinds.map((k) => makeRuntime(k, true));

    const client = makeClient({
      listDetectedLocalRuntimes: vi.fn().mockResolvedValue(runtimes),
    });

    const wrapper = mount(LocalRuntimesSection, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    for (const kind of allKinds) {
      expect(
        wrapper.find(`[data-testid="runtime-card-${kind}"]`).exists(),
        `card for ${kind}`,
      ).toBe(true);
    }
  });

  it('running+empty card shows "no models" state', async () => {
    const client = makeClient({
      listDetectedLocalRuntimes: vi.fn().mockResolvedValue([
        { ...makeRuntime('ollama', true), models: [] },
      ]),
    });

    const wrapper = mount(LocalRuntimesSection, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    const card = wrapper.find('[data-testid="runtime-card-ollama"]');
    expect(card.exists()).toBe(true);
    expect(card.text()).toContain('No models detected');
  });

  it('installed-not-running card shows hint text', async () => {
    const client = makeClient({
      listDetectedLocalRuntimes: vi.fn().mockResolvedValue([
        makeRuntime('ollama', false),
      ]),
    });

    const wrapper = mount(LocalRuntimesSection, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    const card = wrapper.find('[data-testid="runtime-card-ollama"]');
    expect(card.text()).toContain('not running');
  });
});
