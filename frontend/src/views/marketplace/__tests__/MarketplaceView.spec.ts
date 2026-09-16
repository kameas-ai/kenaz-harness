/**
 * MarketplaceView.spec.ts — fleet-share-and-sync-01NDFSEX14 WP03
 *                         + fleet-skills-sync-01NDFSEX18 WP04
 *
 * Seven specs:
 *   1. shows not-signed-in gate when signedIn is false
 *   2. renders catalog items returned by client.catalog.list
 *   3. shows empty state when no items exist
 *   4. install button calls catalog.install and refreshes list
 *   5. uninstall button calls catalog.uninstall and refreshes list
 *   6. install of kind=skill routes to slashcmd.skillInstall
 *   7. uninstall of kind=skill routes to slashcmd.skillUninstall
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { ref } from 'vue';
import MarketplaceView from '../MarketplaceView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { CatalogItemView } from '@/lib/types';

// ── featureFlags mock ──────────────────────────────────────────────────────
// Use a real Vue ref so template auto-unwrapping works correctly.
const _signedIn = ref(true);
vi.mock('@/lib/featureFlags', () => ({
  get signedIn() { return _signedIn; },
  capability: vi.fn().mockReturnValue(true),
}));

// ── CanvasHead stub (no router dependency) ─────────────────────────────────
vi.mock('@/shell/CanvasHead.vue', () => ({
  default: { template: '<div />' },
}));

// ── fixture items ──────────────────────────────────────────────────────────
const WORKFLOW_ITEM: CatalogItemView = {
  id: 'cat-001',
  kind: 'workflow',
  slug: 'my-workflow',
  version: '1.0.0',
  description: 'A test workflow',
  visibility: 'team',
  installed: false,
};

const INSTALLED_ITEM: CatalogItemView = {
  id: 'cat-002',
  kind: 'bundle',
  slug: 'my-bundle',
  version: '2.1.0',
  description: 'A test bundle',
  visibility: 'org_public',
  installed: true,
};

const SKILL_ITEM: CatalogItemView = {
  id: 'skill-001',
  kind: 'skill',
  slug: 'pr-review',
  version: '1.0.0',
  description: 'Review PRs automatically',
  visibility: 'team',
  installed: false,
};

const INSTALLED_SKILL_ITEM: CatalogItemView = {
  id: 'skill-002',
  kind: 'skill',
  slug: 'standup',
  version: '2.0.0',
  description: 'Daily standup skill',
  visibility: 'team',
  installed: true,
};

function buildClient(items: CatalogItemView[] = [], skillInstallFn?: ReturnType<typeof vi.fn>, skillUninstallFn?: ReturnType<typeof vi.fn>) {
  const listFn = vi.fn(async () => items);
  const installFn = vi.fn(async () => {});
  const uninstallFn = vi.fn(async () => {});
  const skillInstall = skillInstallFn ?? vi.fn(async () => {});
  const skillUninstall = skillUninstallFn ?? vi.fn(async () => {});
  const client = createFakeHarnessClient({
    catalog: {
      publish: async () => ({ ...WORKFLOW_ITEM }),
      list: listFn,
      install: installFn,
      uninstall: uninstallFn,
      installed: async () => [],
      unpublish: vi.fn(async () => {}),
    },
    slashcmd: {
      list: async () => [],
      get: async (name: string) => ({
        name,
        scope: 'global' as const,
        kind: 'text' as const,
        description: '',
        modelInvokable: false,
      }),
      save: vi.fn(async () => {}),
      delete: vi.fn(async () => {}),
      run: async () => ({ kind: 'info' as const, text: '' }),
      skillList: async () => [],
      skillInstall,
      skillUninstall,
      skillPublish: vi.fn(async () => {}),
      skillRenameLocalTrigger: vi.fn(async () => {}),
    },
  });
  return { client, listFn, installFn, uninstallFn, skillInstall, skillUninstall };
}

function mountView(client = buildClient().client) {
  return mount(MarketplaceView, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
}

describe('MarketplaceView', () => {
  beforeEach(() => {
    _signedIn.value = true;
  });

  it('1. shows not-signed-in gate when user is not signed in', async () => {
    _signedIn.value = false;
    const { client } = buildClient();
    const wrapper = mountView(client);
    await flushPromises();

    expect(wrapper.find('[data-testid="marketplace-not-signed-in"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="marketplace-grid"]').exists()).toBe(false);
  });

  it('2. renders catalog items from client.catalog.list', async () => {
    const { client } = buildClient([WORKFLOW_ITEM, INSTALLED_ITEM]);
    const wrapper = mountView(client);
    await flushPromises();

    expect(wrapper.find('[data-testid="marketplace-grid"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="marketplace-item-my-workflow"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="marketplace-item-my-bundle"]').exists()).toBe(true);
    // installed badge should be shown for the installed item
    expect(wrapper.find('[data-testid="item-installed-badge"]').exists()).toBe(true);
  });

  it('3. shows empty state when catalog returns no items', async () => {
    const { client } = buildClient([]);
    const wrapper = mountView(client);
    await flushPromises();

    expect(wrapper.find('[data-testid="marketplace-empty"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="marketplace-grid"]').exists()).toBe(false);
  });

  it('4. install button calls catalog.install and refreshes the list', async () => {
    const listFn = vi.fn()
      .mockResolvedValueOnce([WORKFLOW_ITEM])
      .mockResolvedValueOnce([{ ...WORKFLOW_ITEM, installed: true }]);
    const installFn = vi.fn(async () => {});
    const client = createFakeHarnessClient({
      catalog: {
        publish: async () => ({ ...WORKFLOW_ITEM }),
        list: listFn,
        install: installFn,
        uninstall: async () => {},
        installed: async () => [],
        unpublish: vi.fn(async () => {}),
      },
    });

    const wrapper = mountView(client);
    await flushPromises();

    const installBtn = wrapper.find('[data-testid="item-install-btn-my-workflow"]');
    expect(installBtn.exists()).toBe(true);
    await installBtn.trigger('click');
    await flushPromises();

    expect(installFn).toHaveBeenCalledWith('cat-001', '1.0.0');
    // list should have been refreshed (called twice total)
    expect(listFn).toHaveBeenCalledTimes(2);
  });

  it('5. uninstall button calls catalog.uninstall and refreshes the list', async () => {
    const listFn = vi.fn()
      .mockResolvedValueOnce([INSTALLED_ITEM])
      .mockResolvedValueOnce([{ ...INSTALLED_ITEM, installed: false }]);
    const uninstallFn = vi.fn(async () => {});
    const client = createFakeHarnessClient({
      catalog: {
        publish: async () => ({ ...WORKFLOW_ITEM }),
        list: listFn,
        install: async () => {},
        uninstall: uninstallFn,
        installed: async () => [],
        unpublish: vi.fn(async () => {}),
      },
    });

    const wrapper = mountView(client);
    await flushPromises();

    const uninstallBtn = wrapper.find('[data-testid="item-uninstall-btn-my-bundle"]');
    expect(uninstallBtn.exists()).toBe(true);
    await uninstallBtn.trigger('click');
    await flushPromises();

    expect(uninstallFn).toHaveBeenCalledWith('bundle', 'cat-002', '2.1.0');
    expect(listFn).toHaveBeenCalledTimes(2);
  });

  it('6. install of kind=skill routes to slashcmd.skillInstall (FR-202)', async () => {
    // Skills must not go through catalog.install — they need live-registration.
    const skillInstallFn = vi.fn(async () => {});
    const listFn = vi.fn()
      .mockResolvedValueOnce([SKILL_ITEM])
      .mockResolvedValueOnce([{ ...SKILL_ITEM, installed: true }]);
    const installFn = vi.fn(async () => {}); // must NOT be called
    const client = createFakeHarnessClient({
      catalog: {
        publish: async () => ({ ...WORKFLOW_ITEM }),
        list: listFn,
        install: installFn,
        uninstall: async () => {},
        installed: async () => [],
        unpublish: vi.fn(async () => {}),
      },
      slashcmd: {
        list: async () => [],
        get: async (name: string) => ({
          name,
          scope: 'global' as const,
          kind: 'text' as const,
          description: '',
          modelInvokable: false,
        }),
        save: vi.fn(async () => {}),
        delete: vi.fn(async () => {}),
        run: async () => ({ kind: 'info' as const, text: '' }),
        skillList: async () => [],
        skillInstall: skillInstallFn,
        skillUninstall: vi.fn(async () => {}),
        skillPublish: vi.fn(async () => {}),
        skillRenameLocalTrigger: vi.fn(async () => {}),
      },
    });

    const wrapper = mountView(client);
    await flushPromises();

    const installBtn = wrapper.find('[data-testid="item-install-btn-pr-review"]');
    expect(installBtn.exists()).toBe(true);
    await installBtn.trigger('click');
    await flushPromises();

    expect(skillInstallFn).toHaveBeenCalledWith('skill-001', '1.0.0');
    expect(installFn).not.toHaveBeenCalled(); // catalog.install must not be called for skills
    expect(listFn).toHaveBeenCalledTimes(2);
  });

  it('7. uninstall of kind=skill routes to slashcmd.skillUninstall', async () => {
    const skillUninstallFn = vi.fn(async () => {});
    const listFn = vi.fn()
      .mockResolvedValueOnce([INSTALLED_SKILL_ITEM])
      .mockResolvedValueOnce([{ ...INSTALLED_SKILL_ITEM, installed: false }]);
    const uninstallFn = vi.fn(async () => {}); // must NOT be called
    const client = createFakeHarnessClient({
      catalog: {
        publish: async () => ({ ...WORKFLOW_ITEM }),
        list: listFn,
        install: async () => {},
        uninstall: uninstallFn,
        installed: async () => [],
        unpublish: vi.fn(async () => {}),
      },
      slashcmd: {
        list: async () => [],
        get: async (name: string) => ({
          name,
          scope: 'global' as const,
          kind: 'text' as const,
          description: '',
          modelInvokable: false,
        }),
        save: vi.fn(async () => {}),
        delete: vi.fn(async () => {}),
        run: async () => ({ kind: 'info' as const, text: '' }),
        skillList: async () => [],
        skillInstall: vi.fn(async () => {}),
        skillUninstall: skillUninstallFn,
        skillPublish: vi.fn(async () => {}),
        skillRenameLocalTrigger: vi.fn(async () => {}),
      },
    });

    const wrapper = mountView(client);
    await flushPromises();

    const uninstallBtn = wrapper.find('[data-testid="item-uninstall-btn-standup"]');
    expect(uninstallBtn.exists()).toBe(true);
    await uninstallBtn.trigger('click');
    await flushPromises();

    expect(skillUninstallFn).toHaveBeenCalledWith('skill-002');
    expect(uninstallFn).not.toHaveBeenCalled(); // catalog.uninstall must not be called for skills
    expect(listFn).toHaveBeenCalledTimes(2);
  });

  // ── withdraw (fleet-enforcement-truth-01PMZ505 WP11) ──────────────────────

  it('8. shows a Withdraw action on every item, regardless of installed state', async () => {
    const { client } = buildClient([WORKFLOW_ITEM, INSTALLED_ITEM]);
    const wrapper = mountView(client);
    await flushPromises();

    expect(wrapper.find('[data-testid="item-withdraw-btn-my-workflow"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="item-withdraw-btn-my-bundle"]').exists()).toBe(true);
  });

  it('9. withdraw opens a confirm modal naming the org listing, distinct from uninstall', async () => {
    const { client } = buildClient([WORKFLOW_ITEM]);
    const wrapper = mountView(client);
    await flushPromises();

    await wrapper.find('[data-testid="item-withdraw-btn-my-workflow"]').trigger('click');
    expect(wrapper.find('[data-testid="withdraw-confirm-modal"]').exists()).toBe(true);
    const copy = wrapper.find('[data-testid="withdraw-confirm-copy"]').text();
    expect(copy).toContain('org catalog listing');
    // The same copy explicitly distinguishes itself from Uninstall's
    // effect — Uninstall itself renders no confirm dialog at all, so this
    // one string is the only copy in the component, and it names both
    // halves distinctly. AC-021's failure mode ("both rows share one
    // dialog component with one string") does not apply: there is exactly
    // one dialog, for Withdraw, and it does not conflate the two actions.
    expect(copy).toContain('Uninstall');
    expect(copy).toContain('local copy');
  });

  it('10. withdraw confirm calls catalog.unpublish and refreshes the list', async () => {
    const unpublishFn = vi.fn(async () => {});
    const listFn = vi.fn()
      .mockResolvedValueOnce([WORKFLOW_ITEM])
      .mockResolvedValueOnce([]);
    const client = createFakeHarnessClient({
      catalog: {
        publish: async () => ({ ...WORKFLOW_ITEM }),
        list: listFn,
        install: async () => {},
        uninstall: async () => {},
        installed: async () => [],
        unpublish: unpublishFn,
      },
    });
    const wrapper = mountView(client);
    await flushPromises();

    await wrapper.find('[data-testid="item-withdraw-btn-my-workflow"]').trigger('click');
    await wrapper.find('[data-testid="withdraw-confirm"]').trigger('click');
    await flushPromises();

    expect(unpublishFn).toHaveBeenCalledWith('cat-001');
    expect(listFn).toHaveBeenCalledTimes(2);
    expect(wrapper.find('[data-testid="withdraw-confirm-modal"]').exists()).toBe(false);
  });

  it('11. withdraw cancel dismisses without calling unpublish', async () => {
    const unpublishFn = vi.fn(async () => {});
    const client = createFakeHarnessClient({
      catalog: {
        publish: async () => ({ ...WORKFLOW_ITEM }),
        list: async () => [WORKFLOW_ITEM],
        install: async () => {},
        uninstall: async () => {},
        installed: async () => [],
        unpublish: unpublishFn,
      },
    });
    const wrapper = mountView(client);
    await flushPromises();

    await wrapper.find('[data-testid="item-withdraw-btn-my-workflow"]').trigger('click');
    await wrapper.find('[data-testid="withdraw-cancel"]').trigger('click');
    expect(unpublishFn).not.toHaveBeenCalled();
    expect(wrapper.find('[data-testid="withdraw-confirm-modal"]').exists()).toBe(false);
  });

  it('12. AC-020(b): a 403 on withdraw surfaces a forbidden error, never a tier message', async () => {
    // Mirrors the server mapping fixed in core/fleet/catalog.go WP11:
    // ErrCatalogForbidden's rendered text names ownership, not subscription
    // tier. This is a mutation-check on the UI's error passthrough — a
    // regression that swallowed the error and showed a generic "failed"
    // string, or one that re-introduced a tier-flavoured message, fails
    // this assertion.
    const unpublishFn = vi.fn(async () => {
      throw new Error("fleet/catalog: not the item's owner or a fleet admin");
    });
    const client = createFakeHarnessClient({
      catalog: {
        publish: async () => ({ ...WORKFLOW_ITEM }),
        list: async () => [WORKFLOW_ITEM],
        install: async () => {},
        uninstall: async () => {},
        installed: async () => [],
        unpublish: unpublishFn,
      },
    });
    const wrapper = mountView(client);
    await flushPromises();

    await wrapper.find('[data-testid="item-withdraw-btn-my-workflow"]').trigger('click');
    await wrapper.find('[data-testid="withdraw-confirm"]').trigger('click');
    await flushPromises();

    const err = wrapper.find('[data-testid="withdraw-error"]');
    expect(err.exists()).toBe(true);
    expect(err.text()).toContain("not the item's owner or a fleet admin");
    expect(err.text().toLowerCase()).not.toContain('tier');
    expect(err.text().toLowerCase()).not.toContain('subscription');
    // Modal stays open on failure so the user can retry or cancel.
    expect(wrapper.find('[data-testid="withdraw-confirm-modal"]').exists()).toBe(true);
  });
});
