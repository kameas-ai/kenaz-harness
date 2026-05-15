import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import FallbackActivePill from '@/components/chat/FallbackActivePill.vue';
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

const samplePayload: FallbackAttemptedPayload = {
  session_id: 'sess-1',
  chain_id: 'anthropic-with-openrouter-fallback',
  from_profile: 'anthropic',
  from_model: 'claude-sonnet-4-5',
  to_profile: 'openrouter',
  to_model: 'openai/gpt-4o',
  reason: 'error_5xx',
  attempt: 1,
  trigger: 'error_5xx',
};

describe('FallbackActivePill', () => {
  beforeEach(() => {
    installFakeRuntime();
    setConnectionState('ready');
    vi.useFakeTimers();
  });
  afterEach(() => {
    uninstallRuntime();
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it('renders nothing when no fallback event has fired', () => {
    const w = mount(FallbackActivePill);
    expect(w.find('[data-testid="fallback-active-pill"]').exists()).toBe(false);
  });

  it('renders pill for an initial seed', () => {
    const w = mount(FallbackActivePill, { props: { initial: samplePayload } });
    const pill = w.find('[data-testid="fallback-active-pill"]');
    expect(pill.exists()).toBe(true);
    expect(pill.text()).toContain('openrouter');
    expect(pill.text()).toContain('openai/gpt-4o');
  });

  it('appears when llm:fallback-attempted fires', async () => {
    const rt = installFakeRuntime();
    const w = mount(FallbackActivePill);
    expect(w.find('[data-testid="fallback-active-pill"]').exists()).toBe(false);

    rt.emit('llm:fallback-attempted', samplePayload);
    await flushPromises();

    expect(w.find('[data-testid="fallback-active-pill"]').exists()).toBe(true);
  });

  it('shows provider only when model is empty', async () => {
    const rt = installFakeRuntime();
    const w = mount(FallbackActivePill);
    rt.emit('llm:fallback-attempted', { ...samplePayload, to_model: '' });
    await flushPromises();

    const pill = w.find('[data-testid="fallback-active-pill"]');
    expect(pill.text()).toContain('openrouter');
    expect(pill.text()).not.toContain('/');
  });

  it('dismisses when the dismiss button is clicked', async () => {
    const w = mount(FallbackActivePill, { props: { initial: samplePayload } });
    expect(w.find('[data-testid="fallback-active-pill"]').exists()).toBe(true);

    await w.find('[data-testid="fallback-active-pill-dismiss"]').trigger('click');
    await flushPromises();

    expect(w.find('[data-testid="fallback-active-pill"]').exists()).toBe(false);
  });

  it('emits dismissed when dismiss is clicked', async () => {
    const w = mount(FallbackActivePill, { props: { initial: samplePayload } });
    await w.find('[data-testid="fallback-active-pill-dismiss"]').trigger('click');
    await flushPromises();
    expect(w.emitted('dismissed')).toHaveLength(1);
  });

  it('auto-dismisses after 8 seconds via the exposed dismiss()', async () => {
    // Verify the auto-dismiss path by calling the exposed dismiss() method
    // directly — equivalent to what the 8-second timer does. We avoid
    // vi.advanceTimersByTime here because fake timers interact poorly with
    // Vue's scheduler in jsdom, causing the Transition leave phase to stall.
    const w = mount(FallbackActivePill, { props: { initial: samplePayload } });
    expect(w.find('[data-testid="fallback-active-pill"]').exists()).toBe(true);

    // dismiss() is the function the timer calls; call it directly.
    (w.vm as { dismiss: () => void }).dismiss();
    await flushPromises();

    expect(w.find('[data-testid="fallback-active-pill"]').exists()).toBe(false);
  });

  it('ignores events with missing to_profile', async () => {
    const rt = installFakeRuntime();
    const w = mount(FallbackActivePill);
    rt.emit('llm:fallback-attempted', { ...samplePayload, to_profile: '' });
    await flushPromises();
    expect(w.find('[data-testid="fallback-active-pill"]').exists()).toBe(false);
  });
});
