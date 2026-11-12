/**
 * ModelSlotsPanel.spec.ts — served Settings model slots (fleet-workbench-next-plan B11).
 */
import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import ModelSlotsPanel from '@/views/settings/ModelSlotsPanel.vue';
import { createFakeHarnessClient, type ModelSlotsView } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

async function mountWith(modelSlots?: () => Promise<ModelSlotsView>) {
  const base = createFakeHarnessClient();
  const client = createFakeHarnessClient({
    settings: { ...base.settings, ...(modelSlots ? { modelSlots } : {}) },
  });
  const wrapper = mount(ModelSlotsPanel, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  await new Promise((r) => setTimeout(r, 0));
  return wrapper;
}

describe('ModelSlotsPanel', () => {
  it('renders each slot with its model and source', async () => {
    const w = await mountWith(async () => ({
      providerId: 'kenaz-host',
      slots: [
        { slot: 'subagent_default', providerId: 'kenaz-host', modelId: 'model-a', source: 'derived' },
        { slot: 'compaction', providerId: 'kenaz-host', modelId: 'model-b', source: 'host' },
        { slot: 'advisor', providerId: 'kenaz-host', modelId: 'model-c', source: 'org' },
      ],
      orgErrors: ['advisor: "x": model not offered by the provider'],
    }));
    const sub = w.get('[data-testid="model-slot-subagent_default"]');
    expect(sub.text()).toContain('Default sub-agent');
    expect(sub.text()).toContain('model-a');
    expect(sub.text()).toContain('Derived from session provider');
    expect(w.get('[data-testid="model-slot-compaction"]').text()).toContain('Kenaz host');
    expect(w.get('[data-testid="model-slot-advisor"]').text()).toContain('Organization (Fleet)');
    expect(w.get('[data-testid="model-slots-org-errors"]').text()).toContain('not offered');
    // read-only: no inputs
    expect(w.findAll('input, select').length).toBe(0);
  });

  it('renders nothing on a client without the served method (desktop)', async () => {
    const w = await mountWith();
    expect(w.find('[data-testid="model-slots-panel"]').exists()).toBe(false);
  });
});
