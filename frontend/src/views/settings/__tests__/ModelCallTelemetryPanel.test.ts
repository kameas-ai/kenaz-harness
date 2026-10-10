/**
 * ModelCallTelemetryPanel (llm-call-telemetry-01LLMTEL01 WP02): reads the
 * three model-call settings, writes each change through
 * Settings_SetModelCallTelemetry, defaults sharing OFF and says plainly
 * that content is never sent.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ModelCallTelemetryPanel from '@/views/settings/ModelCallTelemetryPanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { ModelCallTelemetrySettings } from '@/lib/types';

function setup(initial?: Partial<ModelCallTelemetrySettings>) {
  const base = createFakeHarnessClient();
  let stored: ModelCallTelemetrySettings = {
    statsPullEnabled: true,
    retentionDays: 0,
    effectiveRetentionDays: 30,
    shareWithFleet: false,
    ...initial,
  };
  const get = vi.fn(async () => ({ ...stored }));
  const set = vi.fn(async (mc: ModelCallTelemetrySettings) => {
    stored = { ...mc, effectiveRetentionDays: mc.retentionDays || 30 };
  });
  const client = {
    ...base,
    settings: { ...base.settings, getModelCallTelemetry: get, setModelCallTelemetry: set },
  };
  const w = mount(ModelCallTelemetryPanel, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  return { w, get, set };
}

describe('ModelCallTelemetryPanel', () => {
  it('loads the defaults: stats pull on, sharing off, 30 days', async () => {
    const { w } = setup();
    await flushPromises();
    const pull = w.find('[data-testid="model-call-stats-pull"]').element as HTMLInputElement;
    const share = w.find('[data-testid="model-call-share-fleet"]').element as HTMLInputElement;
    expect(pull.checked).toBe(true);
    expect(share.checked).toBe(false);
    expect(w.text()).toContain('now 30');
    expect(w.find('[data-testid="model-call-share-explainer"]').text()).toContain(
      'Prompts, responses, tool arguments and tool results are never sent',
    );
  });

  it('writes the Fleet opt-in and the stats-pull toggle through the setter', async () => {
    const { w, set } = setup();
    await flushPromises();
    await w.find('[data-testid="model-call-share-fleet"]').setValue(true);
    await flushPromises();
    expect(set).toHaveBeenLastCalledWith(expect.objectContaining({ shareWithFleet: true, statsPullEnabled: true }));
    await w.find('[data-testid="model-call-stats-pull"]').setValue(false);
    await flushPromises();
    expect(set).toHaveBeenLastCalledWith(expect.objectContaining({ shareWithFleet: true, statsPullEnabled: false }));
  });

  it('writes retention days, blank meaning the default, and refuses junk', async () => {
    const { w, set } = setup();
    await flushPromises();
    const input = w.find('[data-testid="model-call-retention"]');
    await input.setValue('7');
    await input.trigger('change');
    await flushPromises();
    expect(set).toHaveBeenLastCalledWith(expect.objectContaining({ retentionDays: 7 }));
    expect(w.text()).toContain('now 7');
    const calls = set.mock.calls.length;
    await input.setValue('-4');
    await input.trigger('change');
    await flushPromises();
    expect(set.mock.calls.length).toBe(calls);
    expect(w.find('[data-testid="model-call-telemetry-error"]').exists()).toBe(true);
    await input.setValue('');
    await input.trigger('change');
    await flushPromises();
    expect(set).toHaveBeenLastCalledWith(expect.objectContaining({ retentionDays: 0 }));
  });
});
