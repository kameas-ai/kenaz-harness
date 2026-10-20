/**
 * BashPermissionModal — "Allow always" is disabled when the backend says
 * the pattern cannot be persisted (surface.bash.allow_always_unavailable).
 * The backend refuses to write such a grant, so offering the button would
 * let "Allow always" quietly behave as "Allow once".
 *
 * Mutation: drop the allowAlwaysUnavailable branch in
 * BashPermissionModal.vue → the first test fails (button enabled).
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import BashPermissionModal from '@/components/permissions/BashPermissionModal.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { PermissionRequest } from '@/lib/types';

function pending(unavailable: boolean): PermissionRequest {
  return {
    request_id: 'r-bash-1',
    session_id: 's-1',
    family: 'bash',
    resource_display: 'weird-name',
    dangerous_tier: false,
    surface: { bash: { allow_always_unavailable: unavailable } },
  };
}

async function mountWith(req: PermissionRequest) {
  const client = createFakeHarnessClient();
  vi.spyOn(client.permissions, 'listPending').mockResolvedValue([req]);
  mount(BashPermissionModal, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  await flushPromises();
  await flushPromises();
  return document.querySelector<HTMLButtonElement>('[data-testid="perm-modal-allow-always"]');
}

describe('BashPermissionModal — unpersistable pattern', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    document.body.innerHTML = '';
  });

  it('disables Allow always and says why', async () => {
    const btn = await mountWith(pending(true));
    expect(btn).not.toBeNull();
    expect(btn!.disabled).toBe(true);
    expect(btn!.textContent).toContain('unavailable for this command name');
  });

  it('keeps Allow always enabled for an ordinary pattern', async () => {
    const btn = await mountWith(pending(false));
    expect(btn).not.toBeNull();
    expect(btn!.disabled).toBe(false);
    expect(btn!.textContent!.trim()).toBe('Allow always');
  });
});
