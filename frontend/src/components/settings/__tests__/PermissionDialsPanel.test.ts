/**
 * PermissionDialsPanel.test.ts — AC-713, served-mode-is-a-real-mode-01PMZ707
 * WP05 (spec.md §1.2, §5.5).
 *
 * Before this WP, a rejected getPermissionMode() call landed on
 * `permissionMode.value = 'normal'` — the panel painted a posture it never
 * read. Per the spec's correction, deleting the catch alone fixes nothing
 * (PermissionMode is already recorded inert in docs/unwired-ledger.md and
 * `normal` was already the ref's declared default); the actual remedy is
 * that the panel must not render a selected dial when the read failed.
 * These tests assert on the RENDERED unavailable state, not on the ref —
 * asserting on the ref alone would be satisfied by the old default and
 * prove nothing (spec's explicit warning).
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import PermissionDialsPanel from '@/components/settings/PermissionDialsPanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

function provide(overrides: {
  getPermissionMode?: () => Promise<'strict' | 'normal' | 'permissive'>;
  getPermissionCacheDangerousOps?: () => Promise<boolean>;
} = {}) {
  const client = createFakeHarnessClient({
    settings: {
      getPermissionMode:
        overrides.getPermissionMode ?? (async () => 'strict' as const),
      setPermissionMode: vi.fn(async () => undefined),
      getPermissionCacheDangerousOps:
        overrides.getPermissionCacheDangerousOps ?? (async () => false),
      setPermissionCacheDangerousOps: vi.fn(async () => undefined),
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any,
  });
  return { client };
}

describe('PermissionDialsPanel (read succeeds)', () => {
  it('renders the RadioStrip with the mode it read, no unavailable state', async () => {
    const { client } = provide({ getPermissionMode: async () => 'strict' });
    const w = mount(PermissionDialsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    expect(
      w.find('[data-testid="permission-mode-unavailable"]').exists(),
    ).toBe(false);
    expect(
      w.find('[data-testid="permission-mode-strict"]').exists(),
    ).toBe(true);
  });
});

describe('PermissionDialsPanel (read fails — AC-713)', () => {
  it('renders an explicit unavailable state and no RadioStrip at all', async () => {
    const { client } = provide({
      getPermissionMode: async () => {
        throw new Error('Settings_GetPermissionMode not ported to served mode');
      },
    });
    const w = mount(PermissionDialsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    // AC-713's own falsification target: restoring
    // `permissionMode.value = 'normal'` in the catch would make
    // permission-mode-normal exist and pass a ref-only assertion while
    // this rendered-DOM assertion correctly still fails.
    expect(
      w.find('[data-testid="permission-mode-unavailable"]').exists(),
    ).toBe(true);
    expect(w.find('[data-testid="permission-mode-normal"]').exists()).toBe(
      false,
    );
    expect(w.find('[data-testid="permission-mode-strict"]').exists()).toBe(
      false,
    );
    expect(
      w.find('[data-testid="permission-mode-permissive"]').exists(),
    ).toBe(false);
    // No RadioStrip option rendered as selected — the panel must not
    // claim any posture, not even the pre-fix default of "normal".
    expect(w.find('[role="radiogroup"]').exists()).toBe(false);
  });

  it('does not show the permissive warning when the mode was never read', async () => {
    const { client } = provide({
      getPermissionMode: async () => {
        throw new Error('rejected');
      },
    });
    const w = mount(PermissionDialsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(
      w.find('[data-testid="permissive-active-warning"]').exists(),
    ).toBe(false);
  });

  it('still surfaces the dangerous-ops cache dial independently of the permission-mode read', async () => {
    // The two reads are independent try/catches — a failure on one must
    // not fabricate or hide the other's real (successful) result.
    const { client } = provide({
      getPermissionMode: async () => {
        throw new Error('rejected');
      },
      getPermissionCacheDangerousOps: async () => true,
    });
    const w = mount(PermissionDialsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    expect(
      w.find('[data-testid="permission-mode-unavailable"]').exists(),
    ).toBe(true);
    expect(
      w.find('[data-testid="permission-cache-dangerous-toggle"]').attributes(
        'aria-checked',
      ),
    ).toBe('true');
  });
});
