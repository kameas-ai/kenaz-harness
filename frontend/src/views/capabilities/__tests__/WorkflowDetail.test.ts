/**
 * WorkflowDetail — the workflow plugin of the "Add capability" surface
 * (install-framework-01DOGF0B WP05). Ported from the retired
 * CatalogPreviewDrawer.spec.ts: the same preview (YAML, grants, missing MCP
 * servers, cost) and the same Workflows_CatalogInstall call (now routed
 * through the install framework), plus the fleet-workflow branch.
 *
 * In-memory fake clients, DELIBERATELY (WP-PI AC-PI-2): these pin which
 * RPCs the plugin calls; the real-sqlite round trip is
 * TestInstallProvider_Workflow_ConsumerSeesInstall.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import WorkflowDetail from '@/views/capabilities/plugins/WorkflowDetail.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import {
  WorkflowsClientKey,
  createFakeWorkflowsClient,
  type WorkflowsCatalogPreview,
} from '@/lib/workflowsClient';
import type { CapabilityItem } from '@/lib/types';

const template: CapabilityItem = {
  kind: 'workflow',
  id: 'plan_implement_review',
  name: 'Plan → Implement → Review',
  description: 'The canonical loop.',
  version: 'v1',
  source: 'builtin',
  state: { installed: false, consumer: 'workflows store' },
};

const previewDoc: WorkflowsCatalogPreview = {
  entry: {
    id: 'plan_implement_review',
    name: 'Plan → Implement → Review',
    source: 'builtin',
    version: 'v1',
    requiresCedarGrants: ['network'],
    requiresCredentials: ['gmail'],
    estimatedCostUSD: 0.006,
    installStatus: 'not_installed',
  },
  yamlSource: 'id: plan_implement_review\nname: Plan...\n',
};

function mountDetail(item: CapabilityItem, opts: { install?: ReturnType<typeof vi.fn>; capInstall?: ReturnType<typeof vi.fn>; get?: () => Promise<WorkflowsCatalogPreview> } = {}) {
  const install = opts.install ?? vi.fn(async () => ({ workflowId: item.id, scheduled: false }));
  const get = vi.fn(opts.get ?? (async () => previewDoc));
  const workflows = createFakeWorkflowsClient({}, { get, install });
  const base = createFakeHarnessClient();
  const capInstall = opts.capInstall ?? vi.fn(async () => ({ ...item, state: { installed: true } }));
  const client = createFakeHarnessClient({ capabilities: { ...base.capabilities, install: capInstall } });
  const w = mount(WorkflowDetail, {
    props: { item },
    global: { provide: { [HarnessClientKey as symbol]: client, [WorkflowsClientKey as symbol]: workflows } },
  });
  return { w, install, get, capInstall };
}

describe('WorkflowDetail — shipped template (the former Workflows › Catalog)', () => {
  it('renders the preview: YAML, permissions, missing MCP servers, cost', async () => {
    const { w, get } = mountDetail(template);
    await flushPromises();
    expect(get).toHaveBeenCalledWith('plan_implement_review');
    expect(w.get('[data-testid=workflow-detail-yaml]').text()).toContain('plan_implement_review');
    expect(w.get('[data-testid=workflow-detail-grants]').text()).toContain('network');
    expect(w.get('[data-testid=workflow-detail-creds]').text()).toContain('gmail');
    expect(w.get('[data-testid=workflow-detail-cost]').text()).toContain('$');
  });

  it('Install goes through Workflows_CatalogInstall (framework-routed) and reports schedule + missing servers', async () => {
    const install = vi.fn(async () => ({ workflowId: 'plan_implement_review', scheduled: true, missingCredentials: ['gmail'] }));
    const { w, capInstall } = mountDetail(template, { install });
    await flushPromises();
    await w.get('[data-testid=workflow-detail-install-plan_implement_review]').trigger('click');
    await flushPromises();
    expect(install).toHaveBeenCalledWith('plan_implement_review');
    expect(capInstall).not.toHaveBeenCalled();
    expect(w.get('[data-testid=workflow-detail-result]').text()).toContain('scheduled');
    expect(w.get('[data-testid=workflow-detail-result]').text()).toContain('gmail');
    expect(w.emitted('changed')).toBeTruthy();
  });

  it('an install error surfaces', async () => {
    const install = vi.fn(async () => {
      throw new Error('install: the install did not reach its consumer');
    });
    const { w } = mountDetail(template, { install });
    await flushPromises();
    await w.get('[data-testid=workflow-detail-install-plan_implement_review]').trigger('click');
    await flushPromises();
    expect(w.get('[data-testid=workflow-detail-install-error]').text()).toContain('did not reach its consumer');
  });

  it('an installed template offers Open in Workflows instead of Install', async () => {
    const { w } = mountDetail({ ...template, state: { installed: true } });
    await flushPromises();
    expect(w.find('[data-testid=workflow-detail-install-plan_implement_review]').exists()).toBe(false);
    expect(w.find('[data-testid=workflow-detail-open]').exists()).toBe(true);
  });
});

describe('WorkflowDetail — template update (review H4)', () => {
  it('an outdated installed template says Update overwrites edits and keeps the schedule', async () => {
    const { w } = mountDetail({ ...template, state: { installed: true, update_available: true } });
    await flushPromises();
    const note = w.get('[data-testid=workflow-detail-update-note]').text();
    expect(note).toContain('overwritten');
    expect(note).toContain('schedule is kept');
  });
});

describe('WorkflowDetail — fleet catalog workflow', () => {
  it('installs through Capability_Install with its catalog version, and says it is unverified', async () => {
    const fleet: CapabilityItem = { ...template, id: 'cat-td', name: 'team-digest', source: 'team_catalog', version: '1.0.0' };
    const { w, get, install, capInstall } = mountDetail(fleet);
    await flushPromises();
    expect(get).not.toHaveBeenCalled(); // no template preview for a fleet item
    expect(w.find('[data-testid=workflow-detail-unverified]').exists()).toBe(true);
    await w.get('[data-testid=workflow-detail-install-cat-td]').trigger('click');
    await flushPromises();
    expect(capInstall).toHaveBeenCalledWith('workflow', 'cat-td', '1.0.0');
    expect(install).not.toHaveBeenCalled();
  });
});
