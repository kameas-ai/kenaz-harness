/**
 * CatalogView.spec.ts — unit tests for the WP03 catalog grid component.
 *
 * Covers:
 *  1. Renders catalog cards returned by client.catalog.list()
 *  2. Click on a card emits `select` with the entry
 *  3. Search box filters visible cards
 *  4. Load error surfaces an error banner
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import CatalogView from '../CatalogView.vue';
import {
  createFakeWorkflowsClient,
  type WorkflowsClient,
  type WorkflowsCatalogEntry,
} from '@/lib/workflowsClient';

const entryA: WorkflowsCatalogEntry = {
  id: 'plan_implement_review',
  name: 'Plan → Implement → Review',
  description: 'The canonical agentic loop.',
  source: 'builtin',
  version: 'v1',
  estimatedCostUSD: 0.01,
  installStatus: 'not_installed',
};

const entryB: WorkflowsCatalogEntry = {
  id: 'web-research',
  name: 'Web Research',
  description: 'Fetches and summarises web pages.',
  source: 'builtin',
  version: 'v1',
  requiresCedarGrants: ['network'],
  estimatedCostUSD: 0.002,
  installStatus: 'installed',
};

function makeClient(overrides: Partial<WorkflowsClient> = {}): WorkflowsClient {
  return createFakeWorkflowsClient(overrides, {
    list: () => Promise.resolve([entryA, entryB]),
  });
}

describe('CatalogView', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders a card for each catalog entry', async () => {
    const wrapper = mount(CatalogView, {
      props: { client: makeClient() },
    });
    await flushPromises();

    expect(wrapper.find('[data-testid="catalog-card-plan_implement_review"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="catalog-card-web-research"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('Plan → Implement → Review');
    expect(wrapper.text()).toContain('Web Research');
  });

  it('emits `select` with the entry when a card is clicked', async () => {
    const wrapper = mount(CatalogView, {
      props: { client: makeClient() },
    });
    await flushPromises();

    await wrapper.find('[data-testid="catalog-card-plan_implement_review"]').trigger('click');

    expect(wrapper.emitted('select')).toHaveLength(1);
    expect((wrapper.emitted('select')![0] as WorkflowsCatalogEntry[])[0].id).toBe(
      'plan_implement_review',
    );
  });

  it('filters cards by name when the search box has input', async () => {
    const wrapper = mount(CatalogView, {
      props: { client: makeClient() },
    });
    await flushPromises();

    await wrapper.find('[data-testid="catalog-search"]').setValue('web');
    await flushPromises();

    expect(wrapper.find('[data-testid="catalog-card-web-research"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="catalog-card-plan_implement_review"]').exists()).toBe(false);
  });

  it('shows the install status pill for each card', async () => {
    const wrapper = mount(CatalogView, {
      props: { client: makeClient() },
    });
    await flushPromises();

    const statusA = wrapper.find('[data-testid="catalog-status-plan_implement_review"]');
    const statusB = wrapper.find('[data-testid="catalog-status-web-research"]');
    expect(statusA.text()).toContain('Not installed');
    expect(statusB.text()).toContain('Installed');
  });

  it('surfaces a load error banner when the client rejects', async () => {
    const failingClient = createFakeWorkflowsClient({}, {
      list: () => Promise.reject(new Error('network failure')),
    });
    const wrapper = mount(CatalogView, {
      props: { client: failingClient },
    });
    await flushPromises();

    expect(wrapper.find('[data-testid="catalog-load-error"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('network failure');
  });
});
