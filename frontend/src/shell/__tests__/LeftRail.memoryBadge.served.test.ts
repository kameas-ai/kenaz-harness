/**
 * LeftRail.memoryBadge.served.test.ts — served-mode-is-a-real-mode-01PMZ707
 * WP07.
 *
 * Found during per-method caller-site triage, not per-view: MemoryBadge.vue
 * is mounted from LeftRail.vue — shell chrome present on every routed page,
 * not inside any of the boundary-panelled views WP03/WP05 covered. Its
 * fetchCount() calls Memory_HealthSnapshot / Memory_ListChunks, both
 * unrouted in served mode; because `chunkCount` starts `null` and the catch
 * leaves it there ("keep the previous value"), the badge rendered
 * "Loading memory count…" FOREVER instead of an honest unavailable state —
 * a per-view boundary-panel scan would never see this, since LeftRail is
 * not itself a "view".
 */
import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h, nextTick, readonly, ref } from 'vue';
import LeftRail from '@/shell/LeftRail.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';

let servedFlag = ref(false);
vi.mock('@/lib/useServedMode', () => ({
  isServedMode: () => servedFlag.value,
  useServedMode: () => readonly(servedFlag),
}));

async function mountRail() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/sessions/:id?',
        name: 'sessions',
        component: defineComponent({ render: () => h('div', 'sessions') }),
      },
      {
        path: '/:pathMatch(.*)*',
        name: 'not-found',
        component: defineComponent({ render: () => h('div', 'not found') }),
      },
    ],
  });
  await router.push('/sessions');
  await router.isReady();
  const w = mount(LeftRail, {
    global: {
      plugins: [
        router,
        {
          install(app) {
            provideFakeClient(app);
          },
        },
      ],
    },
  });
  await flushPromises();
  await nextTick();
  return w;
}

describe('LeftRail — MemoryBadge served-mode gate', () => {
  afterEach(() => {
    servedFlag.value = false;
  });

  it('renders the badge in desktop mode (control)', async () => {
    servedFlag.value = false;
    const w = await mountRail();
    expect(w.find('[data-testid="memory-badge"]').exists()).toBe(true);
  });

  it('hides the badge under served mode instead of a permanent "Loading…" state', async () => {
    servedFlag.value = true;
    const w = await mountRail();
    // *Falsify*: drop `v-if="!served"` from LeftRail's MemoryBadge mount →
    // this goes red because the badge renders with its stuck-null "…" label.
    expect(w.find('[data-testid="memory-badge"]').exists()).toBe(false);
  });
});
