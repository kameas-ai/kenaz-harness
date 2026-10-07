/**
 * skill-library-01SKLIB01 WP02 — catalog lifecycle in the Capabilities UI.
 *
 * Pins: a Deprecated chip (reason in the tooltip) on catalog rows and in the
 * skill / workflow detail; a Revoked state whose Install is disabled with
 * the FR-1 copy (and never calls Capability_Install); a deprecated
 * org-required copy stays installed, read-only and labelled; active and
 * pre-0114 (no lifecycle) rows carry no chip; unknown states show verbatim.
 *
 * In-memory fake client, DELIBERATELY (WP-PI AC-PI-2): these pin pixels and
 * which RPCs the UI calls, not storage.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { defineComponent } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import CapabilitySurface from '@/views/capabilities/CapabilitySurface.vue';
import SkillDetail from '@/views/capabilities/plugins/SkillDetail.vue';
import WorkflowDetail from '@/views/capabilities/plugins/WorkflowDetail.vue';
import { REVOKED_INSTALL_COPY, lifecycleChip } from '@/views/capabilities/lifecycle';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { CapabilityItem, CapabilityListing } from '@/lib/types';

function skillItem(id: string, overrides: Partial<CapabilityItem> = {}): CapabilityItem {
  return {
    kind: 'skill',
    id,
    name: id,
    version: '1.0.0',
    source: 'team_catalog',
    state: { installed: false, consumer: 'slash registry' },
    ...overrides,
  };
}

async function mountSurface(listing: CapabilityListing) {
  const list = vi.fn(async () => listing);
  const install = vi.fn(async (_k: string, id: string) => skillItem(id, { state: { installed: true } }));
  const base = createFakeHarnessClient();
  const client = createFakeHarnessClient({ capabilities: { ...base.capabilities, list, install } });
  const r = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: defineComponent({ render: () => null }) }],
  });
  await r.push('/?kind=skill');
  await r.isReady();
  const w = mount(CapabilitySurface, {
    global: { plugins: [r], provide: { [HarnessClientKey as symbol]: client } },
    attachTo: document.body,
  });
  await flushPromises();
  return { w, install };
}

describe('lifecycleChip', () => {
  it('labels deviations only, keeps unknown states verbatim', () => {
    expect(lifecycleChip({})).toBeNull();
    expect(lifecycleChip({ lifecycle: 'active' })).toBeNull();
    expect(lifecycleChip({ lifecycle: 'deprecated', lifecycle_reason: 'use v2' })?.title).toContain('use v2');
    expect(lifecycleChip({ lifecycle: 'revoked' })?.label).toBe('Revoked');
    expect(lifecycleChip({ lifecycle: 'quarantined' })?.label).toBe('quarantined');
  });
});

describe('CapabilitySurface — catalog lifecycle', () => {
  it('chips deprecated / revoked rows; active and pre-0114 rows carry none', async () => {
    const { w } = await mountSurface({
      items: [
        skillItem('c-active', { lifecycle: undefined }),
        skillItem('c-dep', { lifecycle: 'deprecated', lifecycle_reason: 'superseded by v2' }),
        skillItem('c-rev', { lifecycle: 'revoked' }),
      ],
    });
    expect(w.find('[data-testid=capability-lifecycle-skill-c-active]').exists()).toBe(false);
    const dep = w.get('[data-testid=capability-lifecycle-skill-c-dep]');
    expect(dep.text()).toBe('Deprecated');
    expect(dep.attributes('title')).toContain('superseded by v2');
    expect(w.get('[data-testid=capability-lifecycle-skill-c-rev]').text()).toBe('Revoked');
    // Deprecated stays installable.
    expect(w.get('[data-testid=capability-install-skill-c-dep]').attributes('disabled')).toBeUndefined();
    w.unmount();
  });

  it('a revoked version cannot be installed: disabled with the FR-1 copy, no RPC', async () => {
    const { w, install } = await mountSurface({ items: [skillItem('c-rev', { lifecycle: 'revoked' })] });
    const btn = w.get('[data-testid=capability-install-skill-c-rev]');
    expect(btn.attributes('disabled')).toBeDefined();
    expect(w.get('[data-testid=capability-revoked-skill-c-rev]').text()).toBe(REVOKED_INSTALL_COPY);
    await btn.trigger('click');
    await flushPromises();
    expect(install).not.toHaveBeenCalled();
    w.unmount();
  });

  it('a deprecated org-required copy stays installed, read-only and labelled', async () => {
    const { w } = await mountSurface({
      items: [skillItem('c-req', {
        lifecycle: 'deprecated', read_only: true, read_only_reason: 'Required by your org', source: 'org_catalog',
        state: { installed: true, version: '2' },
      })],
    });
    expect(w.get('[data-testid=capability-state-skill-c-req]').text()).toBe('Installed');
    expect(w.get('[data-testid=capability-lifecycle-skill-c-req]').text()).toBe('Deprecated');
    w.unmount();
  });
});

describe('detail plugins — lifecycle', () => {
  function provide(install = vi.fn(async () => ({}))) {
    const base = createFakeHarnessClient();
    const client = createFakeHarnessClient({ capabilities: { ...base.capabilities, install } as any });
    return { global: { provide: { [HarnessClientKey as symbol]: client } }, install };
  }

  it('SkillDetail: revoked → disabled install with the copy; required+deprecated says it stays', async () => {
    const p = provide();
    const w = mount(SkillDetail, { props: { item: skillItem('c-rev', { lifecycle: 'revoked' }) }, global: p.global });
    expect(w.get('[data-testid=skill-detail-install-c-rev]').attributes('disabled')).toBeDefined();
    expect(w.get('[data-testid=skill-detail-revoked-c-rev]').text()).toBe(REVOKED_INSTALL_COPY);
    await w.get('[data-testid=skill-detail-install-c-rev]').trigger('click');
    expect(p.install).not.toHaveBeenCalled();

    const req = mount(SkillDetail, {
      props: { item: skillItem('c-req', { lifecycle: 'deprecated', read_only: true, state: { installed: true } }) },
      global: provide().global,
    });
    expect(req.get('[data-testid=skill-detail-lifecycle-c-req]').text()).toContain('stays installed');
  });

  it('WorkflowDetail: a revoked fleet workflow cannot be installed', async () => {
    const p = provide();
    const w = mount(WorkflowDetail, {
      props: { item: { ...skillItem('c-wrev', { lifecycle: 'revoked' }), kind: 'workflow' } },
      global: p.global,
    });
    await flushPromises();
    expect(w.get('[data-testid=workflow-detail-install-c-wrev]').attributes('disabled')).toBeDefined();
    expect(w.get('[data-testid=workflow-detail-revoked-c-wrev]').text()).toBe(REVOKED_INSTALL_COPY);
  });
});
