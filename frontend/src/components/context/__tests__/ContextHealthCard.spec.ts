/**
 * ContextHealthCard.spec.ts — WP07 (context-bootstrap-harness-integration).
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ContextHealthCard from '@/components/context/ContextHealthCard.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { ContextHealth } from '@/lib/harnessClient';

function setup(health: ContextHealth) {
  const client = createFakeHarnessClient();
  const healthFn = vi.spyOn(client.contextBootstrap, 'health').mockResolvedValue(health);
  const startFn = vi
    .spyOn(client.contextBootstrap, 'start')
    .mockResolvedValue({ run_id: 'r1', recipe_version: '1', status: 'completed', fleet_backed: true });
  const wrapper = mount(ContextHealthCard, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  return { wrapper, healthFn, startFn };
}

const sampleHealth: ContextHealth = {
  total_nodes: 42,
  nodes_by_source_kind: { email: 30, chat_message: 12 },
  last_sync: '2026-07-05T00:00:00Z',
  connected_sources: ['gmail', 'slack'],
  latest_run: { run_id: 'run-1', status: 'completed', finished_at: '2026-07-05T00:00:00Z' },
};

describe('ContextHealthCard', () => {
  it('renders the health rollup', async () => {
    const { wrapper, healthFn } = setup(sampleHealth);
    await flushPromises();
    expect(healthFn).toHaveBeenCalledOnce();
    expect(wrapper.find('[data-testid="context-health-card"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="context-health-total"]').text()).toBe('42');
    expect(wrapper.text()).toContain('email');
    expect(wrapper.text()).toContain('gmail, slack');
    expect(wrapper.find('[data-testid="context-health-latest-run"]').text()).toContain('completed');
  });

  it('re-run kicks a bootstrap run over the connected sources', async () => {
    const { wrapper, startFn } = setup(sampleHealth);
    await flushPromises();
    await wrapper.find('[data-testid="context-health-rerun"]').trigger('click');
    await flushPromises();
    expect(startFn).toHaveBeenCalledWith({ consented_sources: ['gmail', 'slack'] });
  });

  it('renders an empty state when the graph is empty', async () => {
    const { wrapper } = setup({ total_nodes: 0, nodes_by_source_kind: {}, connected_sources: [] });
    await flushPromises();
    expect(wrapper.find('[data-testid="context-health-total"]').text()).toBe('0');
    expect(wrapper.text()).toContain('none');
  });
});
