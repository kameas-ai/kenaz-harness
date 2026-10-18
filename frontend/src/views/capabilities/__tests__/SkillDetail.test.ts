/**
 * SkillDetail — the skill plugin of the "Add capability" surface
 * (install-framework-01DOGF0B WP05). Pins the per-kind skill flow (FR-4
 * "skill live-registration") reachable from the surface: Install goes
 * through Capability_Install (→ framework → LiveRegister), the honesty
 * notice about unverified catalog installs is shown, and an org-required
 * skill offers no install.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import SkillDetail from '@/views/capabilities/plugins/SkillDetail.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { CapabilityItem } from '@/lib/types';

const skill: CapabilityItem = {
  kind: 'skill',
  id: 'cat-standup',
  name: 'standup',
  description: 'Daily standup notes',
  version: '1.0.0',
  source: 'org_catalog',
  state: { installed: false, consumer: 'slash registry' },
};

function mountSkill(item: CapabilityItem, install = vi.fn(async () => ({ ...item, state: { installed: true } }))) {
  const base = createFakeHarnessClient();
  const client = createFakeHarnessClient({ capabilities: { ...base.capabilities, install } });
  return { w: mount(SkillDetail, { props: { item }, global: { provide: { [HarnessClientKey as symbol]: client } } }), install };
}

describe('SkillDetail', () => {
  it('installs through Capability_Install with the catalog version', async () => {
    const { w, install } = mountSkill(skill);
    await w.get('[data-testid=skill-detail-install-cat-standup]').trigger('click');
    await flushPromises();
    expect(install).toHaveBeenCalledWith('skill', 'cat-standup', '1.0.0');
    expect(w.emitted('changed')).toBeTruthy();
    expect(w.get('[data-testid=skill-detail-unverified]').text()).toContain('not signature-verified');
  });

  it('an installed skill says it is live and offers no install', async () => {
    const { w } = mountSkill({ ...skill, state: { installed: true, version: '1.0.0' } });
    expect(w.get('[data-testid=skill-detail-state]').text()).toContain('available as a slash command');
    expect(w.find('[data-testid=skill-detail-install-cat-standup]').exists()).toBe(false);
  });

  it('an org-required skill offers no install', async () => {
    const { w } = mountSkill({ ...skill, read_only: true, read_only_reason: 'Required by your org' });
    expect(w.find('[data-testid=skill-detail-install-cat-standup]').exists()).toBe(false);
  });
});
