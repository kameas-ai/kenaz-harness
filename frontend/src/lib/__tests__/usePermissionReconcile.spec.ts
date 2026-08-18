/**
 * usePermissionReconcile.spec.ts — unit tests for the shared
 * reconnect/reload reconciliation logic the four permission modals
 * consume (consent-surfaces-truth-01PMTR01 WP03 / dead-code-audit A11).
 */
import { describe, it, expect, vi } from 'vitest';
import { ref } from 'vue';
import { usePermissionReconcile } from '@/lib/usePermissionReconcile';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import type { PermissionRequest } from '@/lib/types';

function req(overrides: Partial<PermissionRequest> = {}): PermissionRequest {
  return {
    request_id: 'r-1',
    session_id: 's-1',
    family: 'bash',
    resource_display: 'git push --force',
    ...overrides,
  } as PermissionRequest;
}

describe('usePermissionReconcile', () => {
  it('adds a pending row the queue does not have yet', async () => {
    const client = createFakeHarnessClient();
    vi.spyOn(client.permissions, 'listPending').mockResolvedValue([req()]);
    const queue = ref<PermissionRequest[]>([]);
    const reconcile = usePermissionReconcile(client, 'bash', queue, 5);

    await reconcile();

    expect(queue.value).toHaveLength(1);
    expect(queue.value[0].request_id).toBe('r-1');
  });

  it('filters by family — a pending row for another family is ignored', async () => {
    const client = createFakeHarnessClient();
    vi.spyOn(client.permissions, 'listPending').mockResolvedValue([
      req({ request_id: 'r-fs', family: 'fs' }),
    ]);
    const queue = ref<PermissionRequest[]>([]);
    const reconcile = usePermissionReconcile(client, 'bash', queue, 5);

    await reconcile();

    expect(queue.value).toHaveLength(0);
  });

  // Mutation: change the diff to a union (only ever append, never filter)
  // → this test fails, because the stale row (resolved elsewhere) would
  // still be present after reconcile.
  it('drops a queued row the server no longer has (resolved elsewhere)', async () => {
    const client = createFakeHarnessClient();
    vi.spyOn(client.permissions, 'listPending').mockResolvedValue([]);
    const queue = ref<PermissionRequest[]>([req({ request_id: 'stale' })]);
    const reconcile = usePermissionReconcile(client, 'bash', queue, 5);

    await reconcile();

    expect(queue.value).toHaveLength(0);
  });

  it('de-dupes: a row already in the queue is not duplicated', async () => {
    const client = createFakeHarnessClient();
    vi.spyOn(client.permissions, 'listPending').mockResolvedValue([req()]);
    const queue = ref<PermissionRequest[]>([req()]);
    const reconcile = usePermissionReconcile(client, 'bash', queue, 5);

    await reconcile();

    expect(queue.value).toHaveLength(1);
  });

  // Mutation: drop the `next.length >= maxQueue` guard → this test fails,
  // because the third row would be queued (length 3) instead of auto-denied.
  it('auto-denies overflow beyond maxQueue instead of growing the queue', async () => {
    const client = createFakeHarnessClient();
    const resolveSpy = vi.spyOn(client.permissions, 'resolve').mockResolvedValue(undefined);
    vi.spyOn(client.permissions, 'listPending').mockResolvedValue([
      req({ request_id: 'r-1' }),
      req({ request_id: 'r-2' }),
      req({ request_id: 'r-overflow' }),
    ]);
    const queue = ref<PermissionRequest[]>([]);
    const reconcile = usePermissionReconcile(client, 'bash', queue, 2);

    await reconcile();

    expect(queue.value).toHaveLength(2);
    expect(queue.value.map((r) => r.request_id)).toEqual(['r-1', 'r-2']);
    expect(resolveSpy).toHaveBeenCalledWith('r-overflow', 'deny');
  });

  // Mutation: clear the queue in the catch branch → this test fails, since
  // the pre-existing row would be gone instead of untouched. Matches the
  // ConfirmToolModal / AskUserQuestion contract: "I could not reach the
  // harness" must never be read as "nothing is pending".
  it('listPending rejecting leaves the queue completely unchanged', async () => {
    const client = createFakeHarnessClient();
    vi.spyOn(client.permissions, 'listPending').mockRejectedValue(new Error('offline'));
    const existing = req({ request_id: 'still-here' });
    const queue = ref<PermissionRequest[]>([existing]);
    const reconcile = usePermissionReconcile(client, 'bash', queue, 5);

    await reconcile();

    expect(queue.value).toEqual([existing]);
  });
});
