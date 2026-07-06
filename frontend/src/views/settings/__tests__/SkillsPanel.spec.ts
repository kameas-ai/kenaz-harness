/**
 * SkillsPanel.spec.ts — fleet-skills-sync-01NDFSEX18 WP04/WP06
 *
 * Tests:
 *   1. shows empty state when no skills installed
 *   2. renders skill rows with effective trigger
 *   3. shows "Shadowed" badge when skill.shadowed is true (FR-401)
 *   4. shows "Org-managed" badge and disables uninstall for mandated skills (FR-302)
 *   5. shows local trigger alias when localTrigger is set
 *   6. uninstall button calls slashcmd.skillUninstall
 *   7. rename flow: opens inline form, saves, and refreshes
 *   8. rename with empty value clears the local alias
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import SkillsPanel from '../SkillsPanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { SkillItem } from '@/lib/types';

// Suppress toast side effects in tests
vi.mock('@/composables/useToastQueue', () => ({
  push: vi.fn(),
}));

// ── fixtures ────────────────────────────────────────────────────────────────

const CATALOG_SKILL: SkillItem = {
  id: 'cat-skill-001',
  catalogId: 'cat-skill-001',
  version: '1.2.0',
  source: 'catalog',
  trigger: 'pr-review',
  kind: 'text',
  description: 'Review a pull request',
  orgManaged: false,
  shadowed: false,
};

const SHADOWED_SKILL: SkillItem = {
  id: 'cat-skill-002',
  catalogId: 'cat-skill-002',
  version: '1.0.0',
  source: 'catalog',
  trigger: 'review',
  kind: 'text',
  description: 'Team review',
  orgManaged: false,
  shadowed: true, // conflict with personal /review
};

const MANDATED_SKILL: SkillItem = {
  id: 'org/security-review',
  source: 'mandated',
  trigger: 'security-review',
  kind: 'text',
  description: 'Mandatory security review runbook',
  orgManaged: true,
  shadowed: false,
};

const ALIASED_SKILL: SkillItem = {
  id: 'cat-skill-003',
  catalogId: 'cat-skill-003',
  version: '1.0.0',
  source: 'catalog',
  trigger: 'review',
  localTrigger: 'team-review',
  kind: 'text',
  description: 'Team review (aliased)',
  orgManaged: false,
  shadowed: false,
};

// ── mount helper ─────────────────────────────────────────────────────────────

function buildClient(overrides: {
  skills?: SkillItem[];
  skillUninstallFn?: ReturnType<typeof vi.fn>;
  skillRenameLocalTriggerFn?: ReturnType<typeof vi.fn>;
}) {
  const skillUninstallFn = overrides.skillUninstallFn ?? vi.fn(async () => undefined);
  const skillRenameLocalTriggerFn = overrides.skillRenameLocalTriggerFn ?? vi.fn(async () => undefined);
  const skillList = vi.fn(async () => overrides.skills ?? []);

  const client = createFakeHarnessClient({
    slashcmd: {
      list: async () => [],
      get: async (name) => ({
        name,
        scope: 'global' as const,
        kind: 'text' as const,
        description: '',
        modelInvokable: false,
      }),
      save: vi.fn(async () => undefined),
      delete: vi.fn(async () => undefined),
      run: async () => ({ kind: 'info' as const, text: '' }),
      skillList,
      skillInstall: vi.fn(async () => undefined),
      skillUninstall: skillUninstallFn,
      skillPublish: vi.fn(async () => undefined),
      skillRenameLocalTrigger: skillRenameLocalTriggerFn,
    },
  });
  return { client, skillUninstallFn, skillRenameLocalTriggerFn, skillList };
}

function mountPanel(overrides: Parameters<typeof buildClient>[0] = {}) {
  const { client, skillUninstallFn, skillRenameLocalTriggerFn, skillList } = buildClient(overrides);
  const wrapper = mount(SkillsPanel, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
    },
  });
  return { wrapper, skillUninstallFn, skillRenameLocalTriggerFn, skillList };
}

// ── tests ────────────────────────────────────────────────────────────────────

describe('SkillsPanel', () => {
  it('1. shows empty state when no skills installed', async () => {
    const { wrapper } = mountPanel({ skills: [] });
    await flushPromises();
    expect(wrapper.find('[data-testid="skills-empty"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="skills-table"]').exists()).toBe(false);
  });

  it('2. renders skill rows with effective trigger', async () => {
    const { wrapper } = mountPanel({ skills: [CATALOG_SKILL] });
    await flushPromises();
    expect(wrapper.find('[data-testid="skill-row-cat-skill-001"]').exists()).toBe(true);
    const rowText = wrapper.find('[data-testid="skill-row-cat-skill-001"]').text();
    expect(rowText).toContain('/pr-review');
  });

  it('3. shows Shadowed badge when skill.shadowed is true (FR-401)', async () => {
    const { wrapper } = mountPanel({ skills: [SHADOWED_SKILL] });
    await flushPromises();
    expect(
      wrapper.find('[data-testid="skill-shadowed-badge-cat-skill-002"]').exists(),
    ).toBe(true);
  });

  it('4. shows Org-managed badge and hides uninstall for mandated skills (FR-302)', async () => {
    const { wrapper } = mountPanel({ skills: [MANDATED_SKILL] });
    await flushPromises();
    expect(
      wrapper.find('[data-testid="skill-orgmanaged-badge-org_security-review"]').exists() ||
      wrapper.find('[data-testid="skill-orgmanaged-badge-org/security-review"]').exists()
    ).toBe(true);
    // Uninstall button should NOT exist for org-managed skill
    expect(
      wrapper.find('[data-testid^="skill-uninstall-btn-org"]').exists(),
    ).toBe(false);
  });

  it('5. shows canonical trigger annotation when localTrigger is set', async () => {
    const { wrapper } = mountPanel({ skills: [ALIASED_SKILL] });
    await flushPromises();
    const row = wrapper.find('[data-testid="skill-row-cat-skill-003"]');
    expect(row.text()).toContain('/team-review');
    expect(
      wrapper.find('[data-testid="skill-canonical-trigger-cat-skill-003"]').exists(),
    ).toBe(true);
  });

  it('6. uninstall button calls slashcmd.skillUninstall and refreshes', async () => {
    const skillUninstallFn = vi.fn(async () => undefined);
    const skillList = vi.fn()
      .mockResolvedValueOnce([CATALOG_SKILL])
      .mockResolvedValueOnce([]);
    const client = createFakeHarnessClient({
      slashcmd: {
        list: async () => [],
        get: async (name) => ({
          name,
          scope: 'global' as const,
          kind: 'text' as const,
          description: '',
          modelInvokable: false,
        }),
        save: vi.fn(async () => undefined),
        delete: vi.fn(async () => undefined),
        run: async () => ({ kind: 'info' as const, text: '' }),
        skillList,
        skillInstall: vi.fn(async () => undefined),
        skillUninstall: skillUninstallFn,
        skillPublish: vi.fn(async () => undefined),
        skillRenameLocalTrigger: vi.fn(async () => undefined),
      },
    });
    const wrapper = mount(SkillsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    const btn = wrapper.find('[data-testid="skill-uninstall-btn-cat-skill-001"]');
    expect(btn.exists()).toBe(true);
    await btn.trigger('click');
    await flushPromises();

    expect(skillUninstallFn).toHaveBeenCalledWith('cat-skill-001');
    expect(skillList).toHaveBeenCalledTimes(2);
  });

  it('7. rename flow: opens inline form, saves, and refreshes', async () => {
    const skillRenameLocalTriggerFn = vi.fn(async () => undefined);
    const { wrapper } = mountPanel({
      skills: [CATALOG_SKILL],
      skillRenameLocalTriggerFn,
    });
    await flushPromises();

    // Click "Rename trigger"
    await wrapper.find('[data-testid="skill-rename-btn-cat-skill-001"]').trigger('click');
    await wrapper.vm.$nextTick();

    expect(wrapper.find('[data-testid="skill-rename-form-cat-skill-001"]').exists()).toBe(true);

    // Type a new trigger
    await wrapper.find('[data-testid="skill-rename-input-cat-skill-001"]').setValue('my-pr-review');

    // Save
    await wrapper.find('[data-testid="skill-rename-save-btn-cat-skill-001"]').trigger('click');
    await flushPromises();

    expect(skillRenameLocalTriggerFn).toHaveBeenCalledWith('cat-skill-001', 'my-pr-review');
  });

  it('8. cancel closes the rename form without saving', async () => {
    const skillRenameLocalTriggerFn = vi.fn(async () => undefined);
    const { wrapper } = mountPanel({
      skills: [CATALOG_SKILL],
      skillRenameLocalTriggerFn,
    });
    await flushPromises();

    await wrapper.find('[data-testid="skill-rename-btn-cat-skill-001"]').trigger('click');
    await wrapper.vm.$nextTick();

    expect(wrapper.find('[data-testid="skill-rename-form-cat-skill-001"]').exists()).toBe(true);

    await wrapper.find('[data-testid="skill-rename-cancel-btn-cat-skill-001"]').trigger('click');
    await wrapper.vm.$nextTick();

    expect(wrapper.find('[data-testid="skill-rename-form-cat-skill-001"]').exists()).toBe(false);
    expect(skillRenameLocalTriggerFn).not.toHaveBeenCalled();
  });
});
