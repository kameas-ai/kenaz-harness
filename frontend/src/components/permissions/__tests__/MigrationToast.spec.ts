import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import MigrationToast from '@/components/permissions/MigrationToast.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

/**
 * Helper: mount MigrationToast with a controlled settings client.
 *
 * @param toastShown - initial value of getPermissionsMigrationToastShown()
 */
function mountToast(toastShown: boolean) {
  const setPermissionsMigrationToastShown = vi.fn(async () => undefined);
  const client = createFakeHarnessClient({
    settings: {
      // Override only the two methods MigrationToast touches.
      getPermissionsMigrationToastShown: vi.fn(async () => toastShown),
      setPermissionsMigrationToastShown,
      // All other settings methods fall through to safe stubs from
      // createFakeHarnessClient — we don't need to specify them here
      // because the spread/merge inside createFakeHarnessClient fills
      // them in automatically.
      get: async () => ({} as never),
      set: async () => undefined,
      loadRoute: async () => '/',
      saveRoute: async () => undefined,
      logRouteChange: async () => undefined,
      loadTheme: async () => 'system' as never,
      saveTheme: async () => undefined,
      getMemory: async () => false,
      setMemory: async () => undefined,
      getConfirmEach: async () => true,
      setConfirmEach: async () => undefined,
      getWebFetchEnabled: async () => false,
      setWebFetchEnabled: async () => undefined,
      getWebSearch: async () => false,
      setWebSearch: async () => undefined,
      getBash: async () => false,
      setBash: async () => undefined,
      getSaveArtifact: async () => true,
      setSaveArtifact: async () => undefined,
      getMaxAgentTurns: async () => 0,
      setMaxAgentTurns: async () => undefined,
      getPermissionMode: async () => 'normal',
      setPermissionMode: async () => undefined,
      getPermissionCacheDangerousOps: async () => false,
      setPermissionCacheDangerousOps: async () => undefined,
      getBashAllowlistMigrated: async () => false,
      setBashAllowlistMigrated: async () => undefined,
      getCedarStrictCredentialMode: async () => false,
      setCedarStrictCredentialMode: async () => undefined,
      getFSRequestAccessEnabled: async () => true,
      setFSRequestAccessEnabled: async () => undefined,
    },
  });

  const wrapper = mount(MigrationToast, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });

  return { wrapper, setPermissionsMigrationToastShown };
}

describe('MigrationToast', () => {
  it('renders when toast has not been shown yet', async () => {
    const { wrapper } = mountToast(false);
    await flushPromises();

    expect(wrapper.find('[data-testid="migration-toast"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="migration-toast-body"]').text()).toContain(
      'Bash allowlist migrated to Cedar policies',
    );
  });

  it('does not render when toast has already been shown', async () => {
    const { wrapper } = mountToast(true);
    await flushPromises();

    expect(wrapper.find('[data-testid="migration-toast"]').exists()).toBe(false);
  });

  it('calls setPermissionsMigrationToastShown(true) on dismiss', async () => {
    const { wrapper, setPermissionsMigrationToastShown } = mountToast(false);
    await flushPromises();

    expect(wrapper.find('[data-testid="migration-toast"]').exists()).toBe(true);

    await wrapper.find('[data-testid="migration-toast-dismiss"]').trigger('click');
    await flushPromises();

    expect(setPermissionsMigrationToastShown).toHaveBeenCalledWith(true);
  });

  it('hides the toast after dismiss', async () => {
    const { wrapper } = mountToast(false);
    await flushPromises();

    await wrapper.find('[data-testid="migration-toast-dismiss"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-testid="migration-toast"]').exists()).toBe(false);
  });
});
