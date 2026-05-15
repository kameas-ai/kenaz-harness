/**
 * SessionsView.fallback.test.ts
 *
 * Integration smoke tests verifying that the FallbackActivePill is mounted
 * in SessionsView and responds correctly to the 'llm:fallback-attempted'
 * broker event.
 *
 * model-fallback-routing-01NDFSEX04 WP06.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import SessionsView from '@/views/sessions/SessionsView.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { setConnectionState } from '@/lib/useConnectionState';
import type { FallbackAttemptedPayload } from '@/lib/types';

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

const FALLBACK_PAYLOAD: FallbackAttemptedPayload = {
  session_id: 'sess-fallback-test',
  chain_id: 'anthropic-with-openrouter-fallback',
  from_profile: 'anthropic',
  from_model: 'claude-sonnet-4-5',
  to_profile: 'openrouter',
  to_model: 'openai/gpt-4o',
  reason: 'error_5xx',
  attempt: 1,
  trigger: 'error_5xx',
};

async function mountSessionsView() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/sessions/:id?', component: SessionsView },
      { path: '/providers', component: defineComponent({ render: () => h('div', 'providers') }) },
    ],
  });
  await router.push('/sessions/sess-fallback-test');
  await router.isReady();

  const w = mount(SessionsView, {
    global: {
      plugins: [
        router,
        {
          install(app) {
            provideFakeClient(app, {});
          },
        },
      ],
    },
    attachTo: document.body,
  });
  await flushPromises();
  return w;
}

describe('SessionsView — FallbackActivePill integration', () => {
  let rt: FakeRuntime;

  beforeEach(() => {
    rt = installFakeRuntime();
    setConnectionState('ready');
  });

  afterEach(() => {
    uninstallRuntime();
  });

  it('FallbackActivePill is absent when no fallback event has fired', async () => {
    const w = await mountSessionsView();
    // Pill must not be visible by default.
    expect(w.find('[data-testid="fallback-active-pill"]').exists()).toBe(false);
    w.unmount();
  });

  it('FallbackActivePill appears after llm:fallback-attempted fires', async () => {
    const w = await mountSessionsView();

    rt.emit('llm:fallback-attempted', FALLBACK_PAYLOAD);
    await flushPromises();

    const pill = w.find('[data-testid="fallback-active-pill"]');
    expect(pill.exists()).toBe(true);
    expect(pill.text()).toContain('openrouter');

    w.unmount();
  });

  it('FallbackActivePill is dismissible', async () => {
    const w = await mountSessionsView();

    rt.emit('llm:fallback-attempted', FALLBACK_PAYLOAD);
    await flushPromises();

    expect(w.find('[data-testid="fallback-active-pill"]').exists()).toBe(true);

    await w.find('[data-testid="fallback-active-pill-dismiss"]').trigger('click');
    await flushPromises();

    expect(w.find('[data-testid="fallback-active-pill"]').exists()).toBe(false);

    w.unmount();
  });
});
