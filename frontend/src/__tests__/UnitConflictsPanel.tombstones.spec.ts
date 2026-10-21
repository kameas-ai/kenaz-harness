/**
 * UnitConflictsPanel.tombstones.spec.ts
 *
 * The Unit Sync panel reports what pulled tombstones did on this device:
 * shared items removed, the user's own items kept as personal, and items
 * kept because the signed-in account is unknown.
 */
import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import UnitConflictsPanel from '@/views/settings/UnitConflictsPanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { UnitSyncStatusView } from '@/lib/types';

const base: UnitSyncStatusView = {
  cursor: '',
  lastPullAt: '',
  lastPullErr: '',
  lastPushErr: '',
  pushCount: 0,
  pullCount: 0,
  conflictCount: 0,
};

async function mountWith(st: Partial<UnitSyncStatusView>) {
  const client = createFakeHarnessClient({
    Unit_SyncStatus: async () => ({ ...base, ...st }),
  });
  const wrapper = mount(UnitConflictsPanel, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  await new Promise((r) => setTimeout(r, 0));
  return wrapper;
}

describe('UnitConflictsPanel tombstone counters', () => {
  it('shows nothing when no tombstone was applied', async () => {
    const w = await mountWith({});
    expect(w.find('[data-testid="unit-sync-tombstones-removed"]').exists()).toBe(false);
    expect(w.find('[data-testid="unit-sync-tombstones-demoted"]').exists()).toBe(false);
    expect(w.find('[data-testid="unit-sync-tombstones-held"]').exists()).toBe(false);
  });

  it('reports removed, demoted and held items', async () => {
    const w = await mountWith({
      tombstonesApplied: 3,
      tombstoneDroppedLocalEdits: 1,
      tombstonesDemoted: 2,
      tombstonesHeldIdentityUnknown: 4,
    });
    const removed = w.find('[data-testid="unit-sync-tombstones-removed"]').text();
    expect(removed).toContain('3 shared item(s) removed');
    expect(removed).toContain('including 1 with local edits');
    expect(w.find('[data-testid="unit-sync-tombstones-demoted"]').text()).toContain('2 of your own item(s)');
    const held = w.find('[data-testid="unit-sync-tombstones-held"]').text();
    expect(held).toContain('4 item(s)');
    expect(held).toContain('retries automatically');
  });
});
