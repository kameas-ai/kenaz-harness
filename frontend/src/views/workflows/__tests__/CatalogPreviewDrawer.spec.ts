/**
 * CatalogPreviewDrawer.spec.ts — unit tests for the WP03 preview drawer.
 *
 * Covers:
 *  1. Drawer hidden when entry prop is null
 *  2. Drawer renders YAML + grants + creds + cost when entry is set
 *  3. Install button calls client.catalog.install and emits `installed`
 *  4. Install with missing credentials emits `redirect-providers`
 *  5. Install error surfaces in the footer
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import CatalogPreviewDrawer from '../CatalogPreviewDrawer.vue';
import {
  createFakeWorkflowsClient,
  type WorkflowsClient,
  type WorkflowsCatalogEntry,
  type WorkflowsCatalogPreview,
} from '@/lib/workflowsClient';

const entry: WorkflowsCatalogEntry = {
  id: 'plan_implement_review',
  name: 'Plan → Implement → Review',
  description: 'The canonical loop.',
  source: 'builtin',
  version: 'v1',
  requiresCedarGrants: ['network'],
  requiresCredentials: ['gmail'],
  estimatedCostUSD: 0.006,
  installStatus: 'not_installed',
};

const previewDoc: WorkflowsCatalogPreview = {
  entry,
  yamlSource: 'id: plan_implement_review\nname: Plan...\n',
};

function makeClient(
  overrides: Partial<WorkflowsClient> = {},
  catalogOverrides: {
    get?: (id: string) => Promise<WorkflowsCatalogPreview>;
    install?: (id: string) => Promise<{ workflowId: string; scheduled: boolean; missingCredentials?: string[] }>;
  } = {},
): WorkflowsClient {
  return createFakeWorkflowsClient(overrides, {
    get: catalogOverrides.get ?? (() => Promise.resolve(previewDoc)),
    install:
      catalogOverrides.install ??
      (() => Promise.resolve({ workflowId: 'plan_implement_review', scheduled: false })),
  });
}

describe('CatalogPreviewDrawer', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('is not visible when entry prop is null', () => {
    const wrapper = mount(CatalogPreviewDrawer, {
      props: { client: makeClient(), entry: null },
    });
    expect(wrapper.find('[data-testid="catalog-preview-drawer"]').exists()).toBe(false);
  });

  it('renders YAML and metadata when entry is set', async () => {
    const wrapper = mount(CatalogPreviewDrawer, {
      props: { client: makeClient(), entry },
    });
    await flushPromises();

    expect(wrapper.find('[data-testid="catalog-preview-drawer"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="catalog-drawer-yaml"]').text()).toContain(
      'plan_implement_review',
    );
    expect(wrapper.find('[data-testid="catalog-grant-network"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="catalog-cred-gmail"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="catalog-drawer-cost"]').text()).toContain('$');
  });

  it('emits `installed` when Install is clicked and install succeeds', async () => {
    const installFn = vi.fn().mockResolvedValue({
      workflowId: 'plan_implement_review',
      scheduled: false,
    });
    const wrapper = mount(CatalogPreviewDrawer, {
      props: {
        client: makeClient({}, { install: installFn }),
        entry,
      },
    });
    await flushPromises();

    await wrapper.find('[data-testid="catalog-drawer-install"]').trigger('click');
    await flushPromises();

    expect(installFn).toHaveBeenCalledWith('plan_implement_review');
    expect(wrapper.emitted('installed')).toHaveLength(1);
    expect(wrapper.emitted('installed')![0]).toEqual(['plan_implement_review']);
  });

  it('emits `redirect-providers` when install returns missing credentials', async () => {
    const installFn = vi.fn().mockResolvedValue({
      workflowId: 'plan_implement_review',
      scheduled: false,
      missingCredentials: ['gmail'],
    });
    const wrapper = mount(CatalogPreviewDrawer, {
      props: {
        client: makeClient({}, { install: installFn }),
        entry,
      },
    });
    await flushPromises();

    await wrapper.find('[data-testid="catalog-drawer-install"]').trigger('click');
    await flushPromises();

    expect(wrapper.emitted('redirect-providers')).toHaveLength(1);
  });

  it('shows an error in the footer when install fails', async () => {
    const installFn = vi.fn().mockRejectedValue(new Error('storage unavailable'));
    const wrapper = mount(CatalogPreviewDrawer, {
      props: {
        client: makeClient({}, { install: installFn }),
        entry,
      },
    });
    await flushPromises();

    await wrapper.find('[data-testid="catalog-drawer-install"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-testid="catalog-drawer-install-error"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('storage unavailable');
  });

  it('emits `close` when the close button is clicked', async () => {
    const wrapper = mount(CatalogPreviewDrawer, {
      props: { client: makeClient(), entry },
    });
    await flushPromises();

    await wrapper.find('[data-testid="catalog-drawer-close"]').trigger('click');
    expect(wrapper.emitted('close')).toHaveLength(1);
  });
});
