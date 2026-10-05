/**
 * CrossReferenceLink unit tests (WP06).
 *
 * Verifies:
 * 1. Known kinds render as buttons (navigable).
 * 2. Unknown kinds render as spans (no navigation).
 */
import { describe, it, expect } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import CrossReferenceLink from '@/components/audit/CrossReferenceLink.vue';

describe('CrossReferenceLink', () => {
  it('renders a button for known session_id kind', () => {
    const w = mount(CrossReferenceLink, {
      props: { kind: 'session_id', id: '01HFXY8B5VJ6T6T7AXJF9JT9F1' },
      global: { stubs: { RouterLink: true } },
    });
    expect(w.find('button').exists()).toBe(true);
    expect(w.text()).toContain('session');
  });

  it('renders a span for unknown kind', () => {
    const w = mount(CrossReferenceLink, {
      props: { kind: 'unknown_thing_id', id: 'abc123' },
    });
    expect(w.find('span').exists()).toBe(true);
    expect(w.find('button').exists()).toBe(false);
  });

  it('renders a button for artifact_id kind', () => {
    const w = mount(CrossReferenceLink, {
      props: { kind: 'artifact_id', id: 'art-456' },
      global: { stubs: { RouterLink: true } },
    });
    expect(w.find('button').exists()).toBe(true);
  });

  // knowledge-home-01DOGF0E review F3a: memory chunks link straight into
  // Knowledge › Learned with the chunk targeted, not via the legacy
  // /memory/<id> redirect.
  it('memory_chunk_id navigates to /knowledge/learned?chunk=<id>', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', component: defineComponent({ render: () => h('div') }) },
        { path: '/knowledge/learned', component: defineComponent({ render: () => h('div') }) },
      ],
    });
    await router.push('/');
    await router.isReady();
    const w = mount(CrossReferenceLink, {
      props: { kind: 'memory_chunk_id', id: 'chunk/42' },
      global: { plugins: [router] },
    });
    await w.find('button').trigger('click');
    await flushPromises();
    expect(router.currentRoute.value.path).toBe('/knowledge/learned');
    expect(router.currentRoute.value.query).toEqual({ chunk: 'chunk/42' });
  });
});
