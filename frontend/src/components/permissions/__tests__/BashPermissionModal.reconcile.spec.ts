/**
 * BashPermissionModal.reconcile.spec.ts — mount-time rehydration
 * (consent-surfaces-truth-01PMTR01 WP03 / dead-code-audit A11).
 *
 * ConfirmToolModal and AskUserQuestion already prove this pattern for
 * their families; this is the same proof for the permission-prompt
 * family that had zero callers of *_ListPending before this WP. A
 * prompt registered server-side before the modal ever mounted (the
 * reload / hot-reload / WS-reconnect case) must render on mount, not
 * only on the next LIVE `bash:permission-pending` push.
 *
 * BasePermissionModal renders through BaseDialog, which Teleports its
 * panel to document.body, so assertions query `document` directly
 * (same pattern as fleetSurfaces.newlyVisible.spec.ts).
 *
 * Mutation: delete the `onMounted(() => { void reconcile(); })` call in
 * BashPermissionModal.vue → the first test below fails (no row renders
 * until a live event arrives, which never comes in this test).
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import BashPermissionModal from '@/components/permissions/BashPermissionModal.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { PermissionRequest } from '@/lib/types';

function pendingBash(overrides: Partial<PermissionRequest> = {}): PermissionRequest {
  return {
    request_id: 'r-bash-1',
    session_id: 's-1',
    family: 'bash',
    resource_display: 'git push --force',
    dangerous_tier: true,
    ...overrides,
  } as PermissionRequest;
}

function mountModal(client: ReturnType<typeof createFakeHarnessClient>) {
  return mount(BashPermissionModal, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
    },
  });
}

describe('BashPermissionModal — mount-time reconciliation', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    document.body.innerHTML = '';
  });

  it('a prompt registered server-side before mount renders on a fresh mount', async () => {
    const client = createFakeHarnessClient();
    vi.spyOn(client.permissions, 'listPending').mockResolvedValue([pendingBash()]);

    mountModal(client);
    await flushPromises();
    await flushPromises();

    const resource = document.querySelector('[data-testid="perm-modal-resource"]');
    expect(resource).not.toBeNull();
    expect(resource!.textContent!.trim()).toBe('git push --force');
  });

  it('ignores rows from other families in the same listPending snapshot', async () => {
    const client = createFakeHarnessClient();
    vi.spyOn(client.permissions, 'listPending').mockResolvedValue([
      pendingBash({ request_id: 'r-fs', family: 'fs', resource_display: '/tmp/x' }),
    ]);

    mountModal(client);
    await flushPromises();
    await flushPromises();

    expect(document.querySelector('[data-testid="base-permission-modal"]')).toBeNull();
  });

  // Fail-silent contract: "I could not reach the harness" must not render
  // as "nothing is pending" and must not throw during mount.
  it('listPending rejecting mounts cleanly with no request shown', async () => {
    const client = createFakeHarnessClient();
    vi.spyOn(client.permissions, 'listPending').mockRejectedValue(new Error('offline'));

    mountModal(client);
    await flushPromises();
    await flushPromises();

    expect(document.querySelector('[data-testid="base-permission-modal"]')).toBeNull();
  });

  // Resolving the rehydrated row calls through to the real resolve path —
  // proof that the rehydrated request_id round-trips, not just that a
  // label rendered.
  it('resolving the rehydrated prompt calls permissions.resolve with its request_id', async () => {
    const client = createFakeHarnessClient();
    vi.spyOn(client.permissions, 'listPending').mockResolvedValue([pendingBash()]);
    const resolveSpy = vi.spyOn(client.permissions, 'resolve').mockResolvedValue(undefined);

    mountModal(client);
    await flushPromises();
    await flushPromises();

    const allowOnce = document.querySelector<HTMLButtonElement>('[data-testid="perm-modal-allow-once"]');
    expect(allowOnce).not.toBeNull();
    allowOnce!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await flushPromises();

    expect(resolveSpy).toHaveBeenCalledWith('r-bash-1', 'allow_once');
  });
});
