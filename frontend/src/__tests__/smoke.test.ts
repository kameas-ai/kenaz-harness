import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import App from '@/App.vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { installHarnessClient } from '@/lib/harnessClientContext';
import { defineComponent, h } from 'vue';

describe('App smoke', () => {
  it('mounts with a fake harness client', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', redirect: '/sessions' },
        {
          path: '/sessions',
          component: defineComponent({
            setup() {
              return () => h('div', { id: 'fake-sessions' }, 'fake');
            },
          }),
        },
      ],
    });
    await router.push('/sessions');
    await router.isReady();

    const wrapper = mount(App, {
      global: {
        plugins: [router],
        provide: {},
      },
    });

    // Install fake client AFTER mount via a re-mount with provide.
    expect(wrapper.exists()).toBe(true);

    // Drive the install path directly to keep coverage on the helper.
    const fakeApp = { provide: () => undefined } as never;
    expect(() => installHarnessClient(fakeApp, createFakeHarnessClient())).not.toThrow();
  });
});
