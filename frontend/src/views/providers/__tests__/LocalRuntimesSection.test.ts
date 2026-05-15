import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import LocalRuntimesSection from '@/views/providers/LocalRuntimesSection.vue';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { createFakeHarnessClient, type HarnessClient } from '@/lib/harnessClient';
import type { LocalRuntimeInfo, LocalRuntimeConfigResult } from '@/lib/types';

function makeClient(
  override: Partial<HarnessClient['llm']> = {},
): HarnessClient {
  const fake = createFakeHarnessClient();
  return { ...fake, llm: { ...fake.llm, ...override } };
}

function makeRuntime(kind: string, running: boolean, modelCount = 0): LocalRuntimeInfo {
  return {
    kind,
    name: kind.charAt(0).toUpperCase() + kind.slice(1),
    running,
    installed: true,
    defaultBaseURL: `http://localhost:1234`,
    port: 1234,
    models: Array.from({ length: modelCount }, (_, i) => ({
      id: `model-${i}`,
      displayName: `Model ${i}`,
      sizeBytes: 4_000_000_000,
      quantLevel: 'Q4_K_M',
    })),
  };
}

describe('LocalRuntimesSection', () => {
  it('is absent when no runtimes detected (feature flag off)', async () => {
    const client = makeClient({
      listDetectedLocalRuntimes: async () => [],
    });
    const wrapper = mount(LocalRuntimesSection, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="local-runtimes-section"]').exists()).toBe(false);
  });

  it('renders one card per running runtime', async () => {
    const client = makeClient({
      listDetectedLocalRuntimes: async () => [
        makeRuntime('ollama', true, 3),
        makeRuntime('lm-studio', true, 0),
      ],
    });
    const wrapper = mount(LocalRuntimesSection, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="runtime-card-ollama"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="runtime-card-lm-studio"]').exists()).toBe(true);
    // Ollama card shows model count
    expect(wrapper.find('[data-testid="runtime-card-ollama"]').text()).toContain('3 models');
  });

  it('renders installed-not-running card without Add button', async () => {
    const client = makeClient({
      listDetectedLocalRuntimes: async () => [
        makeRuntime('jan', false, 0),
      ],
    });
    const wrapper = mount(LocalRuntimesSection, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const card = wrapper.find('[data-testid="runtime-card-jan"]');
    expect(card.exists()).toBe(true);
    expect(card.find('[data-testid="add-runtime-jan"]').exists()).toBe(false);
    expect(card.text()).toContain('not running');
  });

  it('calls autoConfigureLocalRuntime on Add and notifies parent', async () => {
    const mockResult: LocalRuntimeConfigResult = {
      providerId: 'local-ollama',
      name: 'Ollama (local)',
      models: [],
    };
    const configure = vi.fn().mockResolvedValue(mockResult);
    const onProviderAdded = vi.fn();

    const client = makeClient({
      listDetectedLocalRuntimes: async () => [makeRuntime('ollama', true, 2)],
      autoConfigureLocalRuntime: configure,
    });
    const wrapper = mount(LocalRuntimesSection, {
      props: { onProviderAdded },
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    await wrapper.find('[data-testid="add-runtime-ollama"]').trigger('click');
    await flushPromises();

    expect(configure).toHaveBeenCalledWith('ollama');
    expect(onProviderAdded).toHaveBeenCalled();
  });

  it('shows error message when autoConfigureLocalRuntime fails', async () => {
    const client = makeClient({
      listDetectedLocalRuntimes: async () => [makeRuntime('ollama', true, 1)],
      autoConfigureLocalRuntime: async () => {
        throw new Error('Runtime not running');
      },
    });
    const wrapper = mount(LocalRuntimesSection, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    await wrapper.find('[data-testid="add-runtime-ollama"]').trigger('click');
    await flushPromises();

    const err = wrapper.find('[data-testid="runtime-error-ollama"]');
    expect(err.exists()).toBe(true);
    expect(err.text()).toContain('Runtime not running');
  });

  it('calls rescanLocalRuntimes on Re-scan button click', async () => {
    const rescan = vi.fn().mockResolvedValue([]);
    const client = makeClient({
      listDetectedLocalRuntimes: async () => [makeRuntime('ollama', true)],
      rescanLocalRuntimes: rescan,
    });
    const wrapper = mount(LocalRuntimesSection, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    await wrapper.find('[data-testid="rescan-runtimes"]').trigger('click');
    await flushPromises();

    expect(rescan).toHaveBeenCalled();
  });
});
