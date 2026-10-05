/**
 * SectionSwitcher ↔ LibraryView switcher drift pin (knowledge-home-01DOGF0E
 * review F4).
 *
 * SectionSwitcher copies the inline switcher artifacts-as-units-01DOGF0C
 * shipped in LibraryView.vue. Until LibraryView is moved onto SectionSwitcher
 * (owner: knowledge-home-01DOGF0E, recorded 2026-10-04 — decision record D2),
 * the two must render the same classes for the active and inactive tab, or
 * the two merged homes drift apart visually. Delete this test in the commit
 * that does the extraction.
 */
import { describe, it, expect } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import SectionSwitcher from '@/components/ui/SectionSwitcher.vue';
import LibraryView from '@/views/library/LibraryView.vue';

const Stub = defineComponent({ props: { embedded: Boolean, query: String }, setup: () => () => h('div') });

async function routerAt(path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/library/:view(captured|authored)', component: Stub },
      { path: '/x/:s(a|b)', component: Stub },
    ],
  });
  await router.push(path);
  await router.isReady();
  return router;
}

const classes = (el: { classes(): string[] }) => [...el.classes()].sort();

describe('SectionSwitcher matches LibraryView switcher', () => {
  it('nav, active and inactive tabs carry identical classes', async () => {
    const lib = mount(LibraryView, {
      global: { plugins: [await routerAt('/library/captured')], stubs: { ArtifactsView: Stub, DocumentsView: Stub } },
    });
    await flushPromises();
    const sw = mount(SectionSwitcher, {
      props: {
        sections: [{ id: 'a', label: 'A' }, { id: 'b', label: 'B' }],
        active: 'a',
        basePath: '/x',
        navLabel: 'X',
        testIdPrefix: 'x',
      },
      global: { plugins: [await routerAt('/x/a')] },
    });
    await flushPromises();

    expect(classes(sw.find('[data-testid="x-switcher"]'))).toEqual(classes(lib.find('[data-testid="library-switcher"]')));
    expect(classes(sw.find('[data-testid="x-tab-a"]'))).toEqual(classes(lib.find('[data-testid="library-tab-captured"]')));
    expect(classes(sw.find('[data-testid="x-tab-b"]'))).toEqual(classes(lib.find('[data-testid="library-tab-authored"]')));
    lib.unmount();
    sw.unmount();
  });
});
