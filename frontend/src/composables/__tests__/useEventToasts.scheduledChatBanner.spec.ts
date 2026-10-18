/**
 * useEventToasts.scheduledChatBanner.spec.ts — model-scheduled-jobs-
 * 01PMSJ01 WP07, FR-007.
 *
 * `scheduled-chat:banner` used to be a string that appeared exactly once
 * in the whole repository, in a Go comment (core/scheduler/job.go) — no
 * declared Topic* const, no emitter, no subscriber. This test proves the
 * frontend half: a real broker event on this topic renders a real toast
 * through useToastQueue, not just that the composable subscribes without
 * crashing.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { defineComponent, h } from 'vue';
import { mount, type VueWrapper } from '@vue/test-utils';
import { useEventToasts } from '@/composables/useEventToasts';
import { useToastQueue, _resetToastQueue } from '@/composables/useToastQueue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { setConnectionState } from '@/lib/useConnectionState';

interface FakeRuntime {
  EventsOn: (topic: string, cb: (payload: unknown) => void) => () => void;
  EventsOff: (topic: string) => void;
  emit: (topic: string, payload?: unknown) => void;
}

function installFakeRuntime(): FakeRuntime {
  const handlers = new Map<string, Set<(payload: unknown) => void>>();
  const rt: FakeRuntime = {
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

function uninstallRuntime() {
  delete (window as unknown as { runtime?: unknown }).runtime;
}

const Host = defineComponent({
  setup() {
    useEventToasts();
    return () => h('div');
  },
});

describe('useEventToasts — scheduled-chat:banner (FR-007)', () => {
  let rt: FakeRuntime;
  let client: ReturnType<typeof createFakeHarnessClient>;
  let wrapper: VueWrapper | undefined;

  beforeEach(() => {
    rt = installFakeRuntime();
    setConnectionState('ready');
    _resetToastQueue();
    client = createFakeHarnessClient();
    client.settings.getPermissionsMigrationToastShown = async () => true;
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = undefined;
    uninstallRuntime();
    _resetToastQueue();
  });

  function mountHost() {
    wrapper = mount(Host, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    return wrapper;
  }

  it('renders an info toast naming the run for a completed banner', () => {
    mountHost();
    rt.emit('scheduled-chat:banner', {
      chatRunId: 'run-1',
      name: 'Daily briefing',
      sessionId: 'sess-1',
      status: 'completed',
      outputSnippet: 'Here is your summary.',
    });
    const { toasts } = useToastQueue();
    const match = toasts.find((t) => t.message.includes('Daily briefing'));
    expect(match).toBeTruthy();
    expect(match?.level).toBe('info');
    expect(match?.message).toContain('Here is your summary.');
  });

  it('renders a warn toast with the error for a failed banner', () => {
    mountHost();
    rt.emit('scheduled-chat:banner', {
      chatRunId: 'run-2',
      name: 'Nightly report',
      sessionId: 'sess-2',
      status: 'failed',
      error: 'no default LLM profile configured',
    });
    const { toasts } = useToastQueue();
    const match = toasts.find((t) => t.message.includes('Nightly report'));
    expect(match).toBeTruthy();
    expect(match?.level).toBe('warn');
    expect(match?.message).toContain('no default LLM profile configured');
  });

  it('ignores a malformed payload with no chatRunId', () => {
    mountHost();
    rt.emit('scheduled-chat:banner', { status: 'completed' });
    const { toasts } = useToastQueue();
    expect(toasts.length).toBe(0);
  });
});
