/**
 * AppConfirmToolMount.spec.ts — the anti-regression pin for the
 * newly-introduced workflow-tool-permission-gate hang.
 *
 * ConfirmToolModal.vue is the ONLY UI surface that can answer a
 * `confirm_each` park (see its own header doc: a goroutine is blocked
 * server-side on a channel with no deadline). It used to mount
 * exclusively inside SessionsView.vue. `/workflows` is a sibling
 * top-level route (frontend/src/main.ts), so navigating there unmounted
 * SessionsView and, with it, the only `useEventStream('tool:confirm-
 * pending')` subscriber — while `ConfirmBus.HasChannel()` stayed true,
 * because the bus is process-global (b.publish != nil), not scoped to
 * any one view. A user who clicked "Run now" on a workflow using a
 * confirm_each tool, having never opened Sessions, hung the run forever
 * with no visible way to resolve it.
 *
 * These tests mount App.vue (mirroring AppElicitationMount.spec.ts's
 * pattern for the sibling elicitation dialog) on routes OTHER than
 * /sessions, to prove the fix is "reachable from every route" — that
 * the dialog itself works was never in question; ConfirmToolModal.test.ts
 * mounts the component directly and always passed.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createRouter, createMemoryHistory } from 'vue-router';
import App from '@/App.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { setConnectionState } from '@/lib/useConnectionState';
import type { ToolConfirmPending } from '@/lib/types';

interface FakeRuntime {
  EventsOn: (topic: string, cb: (payload: unknown) => void) => () => void;
  EventsOff: (topic: string) => void;
  emit: (topic: string, payload?: unknown) => void;
  handlers: Map<string, Set<(payload: unknown) => void>>;
}

function installFakeRuntime(): FakeRuntime {
  const handlers = new Map<string, Set<(payload: unknown) => void>>();
  const rt: FakeRuntime = {
    handlers,
    EventsOn(topic, cb) {
      let s = handlers.get(topic);
      if (!s) {
        s = new Set();
        handlers.set(topic, s);
      }
      s.add(cb);
      return () => {
        s!.delete(cb);
      };
    },
    EventsOff(topic) {
      handlers.delete(topic);
    },
    emit(topic, payload) {
      const s = handlers.get(topic);
      if (!s) return;
      for (const cb of s) cb(payload ?? null);
    },
  };
  (window as unknown as { runtime: FakeRuntime }).runtime = rt;
  return rt;
}

const stubComponent = { template: '<div />' };

// Mirrors the real route table's shape for the routes this suite cares
// about (frontend/src/main.ts) — named 'sessions' with an optional :id
// param, plus the sibling top-level routes the bug report names
// (/workflows, a stand-in "scheduled" route for ScheduledInbox).
function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', redirect: '/sessions' },
      { path: '/sessions/:id?', name: 'sessions', component: stubComponent },
      { path: '/workflows', name: 'workflows', component: stubComponent },
      { path: '/scheduled', name: 'scheduled-inbox', component: stubComponent },
    ],
  });
}

function row(overrides: Partial<ToolConfirmPending> = {}): ToolConfirmPending {
  return {
    session_id: 'sess-1',
    call_id: 'call-1',
    batch_id: 'batch-1',
    server: 'github',
    tool: 'create_issue',
    args_summary: '2 arguments: body (string), title (string)',
    ...overrides,
  };
}

describe('App.vue — the confirm-each tool modal is globally mounted', () => {
  let rt: FakeRuntime;
  let router: ReturnType<typeof makeRouter>;
  let client: ReturnType<typeof createFakeHarnessClient>;

  beforeEach(() => {
    rt = installFakeRuntime();
    setConnectionState('ready');
    router = makeRouter();
    client = createFakeHarnessClient();
  });

  afterEach(() => {
    delete (window as unknown as { runtime?: unknown }).runtime;
  });

  function mountApp() {
    return mount(App, {
      attachTo: document.body,
      global: {
        plugins: [router],
        provide: { [HarnessClientKey as symbol]: client },
        stubs: {
          Shell: stubComponent,
          CommandPalette: stubComponent,
          ToastRoot: stubComponent,
          OnboardingDialog: stubComponent,
          AboutDialog: stubComponent,
        },
      },
    });
  }

  it('answers a confirm_each park from the Workflows route — SessionsView was never mounted', async () => {
    await router.push('/workflows');
    // Pin loadRoute to the CURRENT route so App.vue's onMounted
    // restoreLastRoute() (which defaults to '/sessions' in the fake
    // client) is a no-op and does not fight this test's premise.
    vi.spyOn(client.settings, 'loadRoute').mockResolvedValue('/workflows');

    const wrapper = mountApp();
    await flushPromises();

    expect(router.currentRoute.value.name).toBe('workflows');
    // Nothing parked yet: no overlay in front of the user.
    expect(wrapper.find('[data-testid="confirm-tool-modal"]').exists()).toBe(false);
    // The subscription must exist BEFORE the event, and it must exist
    // on THIS route — an unmounted dialog is exactly the bug.
    expect(rt.handlers.get('tool:confirm-pending')?.size ?? 0).toBeGreaterThan(0);

    rt.emit('tool:confirm-pending', row());
    await flushPromises();

    expect(wrapper.find('[data-testid="confirm-tool-modal"]').exists()).toBe(true);
    wrapper.unmount();
  });

  it('approving from the Workflows route reaches client.confirm.resolve — the call that unparks the run', async () => {
    await router.push('/workflows');
    vi.spyOn(client.settings, 'loadRoute').mockResolvedValue('/workflows');
    const resolveSpy = vi.spyOn(client.confirm, 'resolve').mockResolvedValue(undefined);

    const wrapper = mountApp();
    await flushPromises();

    rt.emit('tool:confirm-pending', row());
    await flushPromises();

    await wrapper.find('[data-testid="confirm-tool-approve-call-1"]').trigger('click');
    await flushPromises();

    expect(resolveSpy).toHaveBeenCalledWith('sess-1', 'call-1', true, 'approved by user', false);
    wrapper.unmount();
  });

  it('renders over the Scheduled Inbox route the same way', async () => {
    await router.push('/scheduled');
    vi.spyOn(client.settings, 'loadRoute').mockResolvedValue('/scheduled');

    const wrapper = mountApp();
    await flushPromises();

    rt.emit('tool:confirm-pending', row());
    await flushPromises();

    expect(wrapper.find('[data-testid="confirm-tool-modal"]').exists()).toBe(true);
    wrapper.unmount();
  });

  it('mounts exactly once — no duplicate subscriber across route changes', async () => {
    await router.push('/sessions/sess-1');
    vi.spyOn(client.settings, 'loadRoute').mockResolvedValue('/sessions/sess-1');

    const wrapper = mountApp();
    await flushPromises();

    const initialSubscribers = rt.handlers.get('tool:confirm-pending')?.size ?? 0;
    expect(initialSubscribers).toBe(1);

    // App.vue sits above <router-view> (inside Shell, which is stubbed
    // here), so it must never re-mount — and therefore never
    // re-subscribe — on a route change. Two live subscriptions racing
    // to answer one park would be a new bug (the reconcile-as-a-set-diff
    // contract in ConfirmToolModal.vue assumes exactly one dialog).
    await router.push('/workflows');
    await flushPromises();
    await router.push('/sessions/sess-2');
    await flushPromises();

    expect(rt.handlers.get('tool:confirm-pending')?.size ?? 0).toBe(initialSubscribers);
    expect(wrapper.findAll('[data-testid="confirm-tool-modal"]').length).toBeLessThanOrEqual(1);
    wrapper.unmount();
  });

  it('labels a row foreign when no session is in front (activeSessionId falls back to "" off /sessions)', async () => {
    await router.push('/workflows');
    vi.spyOn(client.settings, 'loadRoute').mockResolvedValue('/workflows');

    const wrapper = mountApp();
    await flushPromises();

    rt.emit('tool:confirm-pending', row({ session_id: 'sess-background' }));
    await flushPromises();

    expect(wrapper.find('[data-testid="confirm-tool-foreign-call-1"]').exists()).toBe(true);
    wrapper.unmount();
  });

  it('still matches activeSessionId to the URL on the Sessions route — existing behaviour is unbroken', async () => {
    await router.push('/sessions/sess-1');
    vi.spyOn(client.settings, 'loadRoute').mockResolvedValue('/sessions/sess-1');

    const wrapper = mountApp();
    await flushPromises();

    rt.emit('tool:confirm-pending', row({ session_id: 'sess-1' }));
    await flushPromises();

    // The row is from the session the URL names — not foreign.
    expect(wrapper.find('[data-testid="confirm-tool-foreign-call-1"]').exists()).toBe(false);
    wrapper.unmount();
  });
});
