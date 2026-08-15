/**
 * AppElicitationMount.spec.ts — the anti-regression pin for the
 * unmounted elicitation dialog.
 *
 * kenaz__ask_user_question is registered default-on with a live
 * Delegate, so a model that calls it PARKS the turn inside
 * `elicitview.API.OpenDialog` — a goroutine blocked on a channel with a
 * ten-minute deadline. `client.elicit.submitAnswer` is the only thing
 * that releases it, and AskUserQuestion.vue is the only caller of that.
 *
 * That component sat in the tree unimported: `git log -S` over
 * frontend/src never found an import of it, and none existed. The
 * dialog's own unit tests passed the whole time, because a component
 * test mounts the component itself. Nothing tested that the APP mounts
 * it — so "the dialog works" and "the user can answer the model" were
 * two different facts, and only the first was true.
 *
 * These tests therefore mount App.vue, not the dialog: the subject under
 * test is the mounting, not the rendering.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createRouter, createMemoryHistory } from 'vue-router';
import App from '@/App.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { setConnectionState } from '@/lib/useConnectionState';
import type { ElicitRequest } from '@/lib/types';

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
      return () => { s!.delete(cb); };
    },
    EventsOff(topic) { handlers.delete(topic); },
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

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: stubComponent }],
  });
}

function radioAsk(overrides: Partial<ElicitRequest> = {}): ElicitRequest {
  return {
    request_id: 'ask-1',
    question: 'Which database should I use?',
    kind: 'radio',
    options: [
      { value: 'sqlite', label: 'SQLite' },
      { value: 'postgres', label: 'Postgres' },
    ],
    ...overrides,
  } as ElicitRequest;
}

describe('App.vue — the elicitation dialog is mounted', () => {
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

  it('subscribes to elicit:pending and renders the dialog on an ask', async () => {
    const wrapper = mountApp();
    await flushPromises();

    // Nothing parked yet: no overlay in front of the user.
    expect(wrapper.find('[data-testid="ask-user-question-dialog"]').exists()).toBe(false);
    // The subscription must exist BEFORE the event — an unmounted dialog
    // has no subscriber, which is exactly the bug.
    expect(rt.handlers.get('elicit:pending')?.size ?? 0).toBeGreaterThan(0);

    rt.emit('elicit:pending', radioAsk());
    await flushPromises();

    expect(wrapper.find('[data-testid="ask-user-question-dialog"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="ask-dialog-question"]').text()).toContain(
      'Which database should I use?',
    );
    wrapper.unmount();
  });

  it('answering reaches client.elicit.submitAnswer — the call that unparks the turn', async () => {
    const submitSpy = vi.spyOn(client.elicit, 'submitAnswer').mockResolvedValue(undefined);
    const wrapper = mountApp();
    await flushPromises();

    rt.emit('elicit:pending', radioAsk());
    await flushPromises();

    await wrapper.find('[data-testid="ask-dialog-submit"]').trigger('click');
    await flushPromises();

    expect(submitSpy).toHaveBeenCalledTimes(1);
    // (requestID, answerValue, cancelled=false). The radio pre-selects
    // its first option, so the answer is the option VALUE, not its label
    // and not a JSON string of it.
    expect(submitSpy.mock.calls[0][0]).toBe('ask-1');
    expect(submitSpy.mock.calls[0][1]).toBe('sqlite');
    expect(submitSpy.mock.calls[0][2]).toBe(false);

    // The dialog clears once the pause is released.
    await flushPromises();
    expect(wrapper.find('[data-testid="ask-user-question-dialog"]').exists()).toBe(false);
    wrapper.unmount();
  });

  it('cancelling submits a declination rather than silently unmounting', async () => {
    const submitSpy = vi.spyOn(client.elicit, 'submitAnswer').mockResolvedValue(undefined);
    const wrapper = mountApp();
    await flushPromises();

    rt.emit('elicit:pending', radioAsk());
    await flushPromises();

    await wrapper.find('[data-testid="ask-dialog-cancel"]').trigger('click');
    await flushPromises();

    // Dismissing a dialog that owns a blocked goroutine and telling
    // nobody would leave the turn hung for the full ten minutes.
    expect(submitSpy).toHaveBeenCalledWith('ask-1', null, true);
    wrapper.unmount();
  });

  it('rebuilds the queue from Elicit_ListPending on mount (reload does not un-park)', async () => {
    vi.spyOn(client.elicit, 'listPending').mockResolvedValue([
      radioAsk({ request_id: 'ask-parked', question: 'Still waiting?' }),
    ]);

    const wrapper = mountApp();
    await flushPromises();
    await flushPromises();

    expect(wrapper.find('[data-testid="ask-user-question-dialog"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="ask-dialog-question"]').text()).toContain('Still waiting?');
    wrapper.unmount();
  });
});
