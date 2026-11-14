/**
 * FilesystemPermissionModal — "Points to:" for a path that is a link.
 *
 * A prompt for `read /tmp/k` must not hide that /tmp/k is a link to
 * ~/.ssh/id_ed25519: the backend sends surface.fs.resolved_path whenever
 * the operation really acts somewhere other than the path asked for.
 *
 * Mutation: drop the `fs-points-to` paragraph → the first test fails.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import FilesystemPermissionModal from '@/components/permissions/FilesystemPermissionModal.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { PermissionRequest } from '@/lib/types';

function pendingFS(fs: NonNullable<NonNullable<PermissionRequest['surface']>['fs']>): PermissionRequest {
  return {
    request_id: 'r-fs-link',
    session_id: 's-1',
    family: 'fs',
    resource_display: 'read /tmp/k',
    op: 'read',
    dangerous_tier: true,
    surface: { fs },
  } as PermissionRequest;
}

async function mountWith(rows: PermissionRequest[]) {
  const client = createFakeHarnessClient();
  vi.spyOn(client.permissions, 'listPending').mockResolvedValue(rows);
  mount(FilesystemPermissionModal, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  await flushPromises();
  await flushPromises();
}

const q = (id: string) => document.querySelector(`[data-testid="${id}"]`);

describe('FilesystemPermissionModal — link target', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    document.body.innerHTML = '';
  });

  it('shows where a link really points', async () => {
    await mountWith([pendingFS({ canonical_path: '/tmp/k', resolved_path: '/Users/a/.ssh/id_ed25519' })]);
    const el = q('fs-points-to');
    expect(el).not.toBeNull();
    expect(el!.textContent).toContain('Points to:');
    expect(el!.textContent).toContain('/Users/a/.ssh/id_ed25519');
  });

  it('says nothing when the path is not a link', async () => {
    await mountWith([pendingFS({ canonical_path: '/tmp/k' })]);
    expect(q('fs-points-to')).toBeNull();
  });

  it('says nothing when the resolved path is the path itself', async () => {
    await mountWith([pendingFS({ canonical_path: '/tmp/k', resolved_path: '/tmp/k' })]);
    expect(q('fs-points-to')).toBeNull();
  });
});
