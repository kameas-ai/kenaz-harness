import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { defineComponent, h, nextTick } from 'vue';
import { mount } from '@vue/test-utils';
import { useEventStream } from '@/lib/useEventStream';
import { setConnectionState } from '@/lib/useConnectionState';

interface FakeRuntime {
  EventsOn: (topic: string, cb: (payload: unknown) => void) => () => void;
  EventsOff?: (topic: string) => void;
  /** test helper */
  emit: (topic: string, payload: unknown) => void;
  /** test helper */
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
      return () => {
        s!.delete(cb);
      };
    },
    EventsOff: (topic) => {
      handlers.delete(topic);
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

describe('useEventStream (chat-ui)', () => {
  let rt: FakeRuntime;

  beforeEach(() => {
    rt = installFakeRuntime();
    setConnectionState('ready');
  });

  afterEach(() => {
    uninstallRuntime();
  });

  it('forwards payloads to the handler and tracks count', async () => {
    const seen: Array<{ delta: string }> = [];
    const Comp = defineComponent({
      setup() {
        const { latest, count } = useEventStream<{ delta: string }>(
          'llm:stream-chunk',
          (p) => seen.push(p),
        );
        return () =>
          h('div', `${count.value}|${latest.value?.delta ?? ''}`);
      },
    });
    const w = mount(Comp);
    rt.emit('llm:stream-chunk', { delta: 'hi' });
    rt.emit('llm:stream-chunk', { delta: ' there' });
    await nextTick();
    expect(seen.map((s) => s.delta)).toEqual(['hi', ' there']);
    expect(w.text()).toBe('2| there');
    w.unmount();
  });

  it('cleans up on unmount', () => {
    const Comp = defineComponent({
      setup() {
        useEventStream('llm:stream-chunk');
        return () => h('div');
      },
    });
    const w = mount(Comp);
    expect(rt.handlers.get('llm:stream-chunk')?.size).toBe(1);
    w.unmount();
    expect(rt.handlers.get('llm:stream-chunk')?.size ?? 0).toBe(0);
  });

  it('resubscribes when connection flips back to ready', async () => {
    const Comp = defineComponent({
      setup() {
        useEventStream('llm:stream-chunk');
        return () => h('div');
      },
    });
    const w = mount(Comp);
    expect(rt.handlers.get('llm:stream-chunk')?.size).toBe(1);
    setConnectionState('lost');
    await nextTick();
    expect(rt.handlers.get('llm:stream-chunk')?.size ?? 0).toBe(0);
    setConnectionState('ready');
    await nextTick();
    expect(rt.handlers.get('llm:stream-chunk')?.size).toBe(1);
    w.unmount();
  });

  it('exposes a manual unsubscribe function', () => {
    let unsubscribe: () => void = () => undefined;
    const Comp = defineComponent({
      setup() {
        const { unsubscribe: u } = useEventStream('llm:stream-chunk');
        unsubscribe = u;
        return () => h('div');
      },
    });
    const w = mount(Comp);
    expect(rt.handlers.get('llm:stream-chunk')?.size).toBe(1);
    unsubscribe();
    expect(rt.handlers.get('llm:stream-chunk')?.size ?? 0).toBe(0);
    w.unmount();
  });

  it('falls back to served-event bus when window.runtime is missing', async () => {
    // Uninstall the Wails runtime to simulate served mode.
    uninstallRuntime();

    const seen: string[] = [];
    const Comp = defineComponent({
      setup() {
        const r = useEventStream<{ delta: string }>(
          'llm:stream-chunk',
          (p) => seen.push(p.delta),
        );
        return () =>
          h('div', `${r.active.value}|${r.count.value}`);
      },
    });
    const w = mount(Comp);

    // In served mode the composable subscribes to the served-event bus
    // and reports active=true even without window.runtime.
    expect(w.text()).toBe('true|0');

    // Dispatch an event through the served bus; the handler should fire.
    const { dispatchServedEvent } = await import('@/lib/useServedEvents');
    dispatchServedEvent('llm:stream-chunk', { delta: 'hello' });
    await nextTick();

    expect(seen).toEqual(['hello']);
    expect(w.text()).toBe('true|1');
    w.unmount();
  });
});

// silence noisy timer warnings if any
vi.useRealTimers();
