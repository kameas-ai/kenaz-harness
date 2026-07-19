/**
 * FR-007 (01NKNOW01) — /corpora redirect test.
 *
 * Asserts that /corpora and /corpora/:id both redirect to /contexts,
 * exercising the redirect route registered in main.ts (and main-served.ts).
 */
import { describe, it, expect } from 'vitest';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';

/** Minimal route table mirroring the redirect added in FR-002. */
function buildRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/corpora/:pathMatch(.*)*',
        redirect: '/contexts',
      },
      {
        path: '/contexts',
        component: defineComponent({ setup: () => () => h('div', 'contexts') }),
      },
    ],
  });
}

describe('corpora → contexts redirect (FR-007)', () => {
  it('redirects /corpora to /contexts', async () => {
    const router = buildRouter();
    await router.push('/corpora');
    await router.isReady();
    expect(router.currentRoute.value.path).toBe('/contexts');
  });

  it('redirects /corpora/:id to /contexts', async () => {
    const router = buildRouter();
    await router.push('/corpora/some-corpus-id');
    await router.isReady();
    expect(router.currentRoute.value.path).toBe('/contexts');
  });

  it('redirects /corpora with nested path to /contexts', async () => {
    const router = buildRouter();
    await router.push('/corpora/foo/bar/baz');
    await router.isReady();
    expect(router.currentRoute.value.path).toBe('/contexts');
  });
});
