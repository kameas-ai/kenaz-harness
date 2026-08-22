/**
 * BundlesView.served.test.ts — served-mode-is-a-real-mode-01PMZ707 WP04
 * (E-705). All four Bundle_* methods this view calls (List/Get/Install/
 * Remove) are unrouted in served mode. `signedIn` already hid the
 * "Publish to team" button (a fleet-shaped fence), but that left the
 * non-fleet problem — the view's own list/install/remove flows — showing
 * a raw error string via refresh()'s catch instead of the boundary panel
 * every other fully-unported view gets. This pins the panel.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { ref } from 'vue';
import BundlesView from '@/views/bundles/BundlesView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

let servedModeFlag = true;
vi.mock('@/lib/useServedMode', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/useServedMode')>();
  return {
    ...actual,
    isServedMode: () => servedModeFlag,
    useServedMode: () => ref(servedModeFlag),
  };
});

afterEach(() => {
  servedModeFlag = true;
});

describe('BundlesView (served mode)', () => {
  it('renders the boundary panel and never calls Bundle_List', async () => {
    servedModeFlag = true;
    const list = vi.fn(async () => {
      throw new Error('must not be called in served mode — Bundle_List has no dispatch case');
    });
    const client = createFakeHarnessClient({
      bundle: {
        list,
        get: async () => {
          throw new Error('must not be called');
        },
        install: async () => {
          throw new Error('must not be called');
        },
        remove: async () => {
          throw new Error('must not be called');
        },
      } as any,
    });
    const w = mount(BundlesView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    expect(
      w.find('[data-testid="not-available-in-served-mode"]').exists(),
    ).toBe(true);
    expect(list).not.toHaveBeenCalled();
  });
});

describe('BundlesView (desktop mode regression)', () => {
  it('keeps rendering the real bundle list, not the panel', async () => {
    servedModeFlag = false;
    const client = createFakeHarnessClient({
      bundle: {
        list: async () => [],
        get: async (id: string) => ({
          id,
          name: id,
          version: '',
          tier: '',
          artifactCount: 0,
        }),
        install: async () => {
          throw new Error('not exercised');
        },
        remove: async () => undefined,
      } as any,
    });
    const w = mount(BundlesView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    expect(
      w.find('[data-testid="not-available-in-served-mode"]').exists(),
    ).toBe(false);
    expect(w.text()).toContain('No bundles installed');
  });
});
