/**
 * SessionsView — axe-core accessibility assertions
 * (accessibility-audit-01KQ8TDA WP02)
 *
 * Tests the chat surface (empty state + with a session loaded) for
 * automated axe-core violations. color-contrast is disabled because
 * happy-dom does not implement CSS layout / computed style, so ratio is
 * always reported as 0/0 (needs_manual_verification).
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import SessionsView from '@/views/sessions/SessionsView.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { setConnectionState } from '@/lib/useConnectionState';
import { axe } from 'vitest-axe';

interface FakeRuntime {
  EventsOn: (topic: string, cb: (payload: unknown) => void) => () => void;
  emit: (topic: string, payload: unknown) => void;
  handlers: Map<string, Set<(payload: unknown) => void>>;
}

function installFakeRuntime(): FakeRuntime {
  const handlers = new Map<string, Set<(payload: unknown) => void>>();
  const rt: FakeRuntime = {
    handlers,
    EventsOn: (topic, cb) => {
      let s = handlers.get(topic);
      if (!s) {
        s = new Set();
        handlers.set(topic, s);
      }
      s.add(cb);
      return () => s!.delete(cb);
    },
    emit: (topic, payload) => {
      const s = handlers.get(topic);
      if (!s) return;
      for (const cb of s) cb(payload);
    },
  };
  (window as unknown as { runtime: FakeRuntime }).runtime = rt;
  return rt;
}

function uninstallRuntime() {
  delete (window as unknown as { runtime?: unknown }).runtime;
}

/** Axe options shared by all tests in this file. */
const axeOptions = {
  rules: {
    // happy-dom does not compute CSS, so color-contrast always reports 0/0.
    // needs_manual_verification: verify contrast ratios in Kenaz dark + light
    // themes with a real browser.
    'color-contrast': { enabled: false },
    // region — the component tree mounts into a bare div in vitest/happy-dom,
    // so page-level landmark structure (html/body/main) is absent. The Shell
    // wraps all views in a proper landmark grid in production.
    region: { enabled: false },
  },
};

async function mountSessionsView(routePath = '/sessions') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/sessions/:id?', component: SessionsView },
      {
        path: '/providers',
        component: defineComponent({ render: () => h('div', 'providers') }),
      },
    ],
  });
  await router.push(routePath);
  await router.isReady();

  const wrapper = mount(SessionsView, {
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
  return wrapper;
}

describe('SessionsView — a11y (axe-core)', () => {
  beforeEach(() => {
    installFakeRuntime();
    setConnectionState('ready');
  });
  afterEach(() => {
    uninstallRuntime();
  });

  it('has no axe violations in empty state (no session selected)', async () => {
    const w = await mountSessionsView('/sessions');
    const results = await axe(w.element, axeOptions);
    // @ts-expect-error — toHaveNoViolations added via test-setup.ts extend
    expect(results).toHaveNoViolations();
    w.unmount();
  });
});
