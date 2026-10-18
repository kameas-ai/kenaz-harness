/**
 * ContextHealthCard.spec.ts — WP07 (context-bootstrap-harness-integration);
 * chip mode + honesty fixes from knowledge-home-01DOGF0E WP06 (spec FR-8,
 * pin P-8).
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ContextHealthCard from '@/components/context/ContextHealthCard.vue';
import { CONTEXT_HEALTH_EXPANDED_KEY } from '@/components/context/contextHealthPref';
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

async function expand(wrapper: ReturnType<typeof setup>['wrapper']) {
  await wrapper.find('[data-testid="context-health-chip"]').trigger('click');
  await flushPromises();
}

const sampleHealth: ContextHealth = {
  total_nodes: 42,
  nodes_by_source_kind: { email: 30, chat_message: 12 },
  last_sync: '2026-07-05T00:00:00Z',
  connected_sources: ['gmail', 'slack'],
  latest_run: { run_id: 'run-1', status: 'completed', finished_at: '2026-07-05T00:00:00Z' },
};

const emptyHealth: ContextHealth = {
  total_nodes: 0,
  nodes_by_source_kind: {},
  connected_sources: [],
  latest_run: { run_id: 'run-0', status: 'completed' },
};

beforeEach(() => {
  localStorage.removeItem(CONTEXT_HEALTH_EXPANDED_KEY);
});

describe('ContextHealthCard', () => {
  it('renders the health rollup once expanded', async () => {
    const { wrapper, healthFn } = setup(sampleHealth);
    await flushPromises();
    expect(healthFn).toHaveBeenCalledOnce();
    await expand(wrapper);
    expect(wrapper.find('[data-testid="context-health-card"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="context-health-total"]').text()).toBe('42');
    expect(wrapper.text()).toContain('email');
    expect(wrapper.text()).toContain('gmail, slack');
    expect(wrapper.find('[data-testid="context-health-latest-run"]').text()).toContain('completed');
  });

  it('re-run kicks a bootstrap run over the connected sources', async () => {
    const { wrapper, startFn } = setup(sampleHealth);
    await flushPromises();
    await expand(wrapper);
    await wrapper.find('[data-testid="context-health-rerun"]').trigger('click');
    await flushPromises();
    expect(startFn).toHaveBeenCalledWith({ consented_sources: ['gmail', 'slack'] });
  });

  it('renders an empty state when the graph is empty', async () => {
    const { wrapper } = setup({ total_nodes: 0, nodes_by_source_kind: {}, connected_sources: [] });
    await flushPromises();
    await expand(wrapper);
    expect(wrapper.find('[data-testid="context-health-total"]').text()).toBe('0');
    expect(wrapper.text()).toContain('none');
  });
});

describe('ContextHealthCard chip (knowledge-home-01DOGF0E WP06, P-8)', () => {
  it('is a collapsed one-line chip by default — no wall of zeros', async () => {
    const { wrapper } = setup(emptyHealth);
    await flushPromises();
    const chip = wrapper.find('[data-testid="context-health-chip"]');
    expect(chip.attributes('aria-expanded')).toBe('false');
    expect(wrapper.find('[data-testid="context-health-chip-summary"]').text()).toBe('0 nodes · never synced');
    expect(wrapper.find('[data-testid="context-health-card"]').exists()).toBe(false);
  });

  it('remembers the expanded choice across mounts', async () => {
    const first = setup(sampleHealth);
    await flushPromises();
    await expand(first.wrapper);
    expect(localStorage.getItem(CONTEXT_HEALTH_EXPANDED_KEY)).toBe('1');
    first.wrapper.unmount();

    const second = setup(sampleHealth);
    await flushPromises();
    expect(second.wrapper.find('[data-testid="context-health-card"]').exists()).toBe(true);
    await second.wrapper.find('[data-testid="context-health-chip"]').trigger('click');
    expect(localStorage.getItem(CONTEXT_HEALTH_EXPANDED_KEY)).toBe('0');
  });

  it('Re-run is disabled with a reason when no source is connected, and never starts a zero-source scan', async () => {
    const { wrapper, startFn } = setup(emptyHealth);
    await flushPromises();
    await expand(wrapper);
    const btn = wrapper.find('[data-testid="context-health-rerun"]');
    expect((btn.element as HTMLButtonElement).disabled).toBe(true);
    expect(wrapper.find('[data-testid="context-health-rerun-reason"]').text()).toBe(
      'No sources connected — connect a source to scan.',
    );
    await btn.trigger('click');
    await flushPromises();
    expect(startFn).not.toHaveBeenCalled();
  });

  it('labels the latest run as a bootstrap scan, not a bare "run"', async () => {
    const { wrapper } = setup(emptyHealth);
    await flushPromises();
    await expand(wrapper);
    const row = wrapper.find('[data-testid="context-health-latest-run"]').text();
    expect(row).toBe('Latest bootstrap scan: completed');
    expect(row).not.toMatch(/^Latest run:/);
  });
});
