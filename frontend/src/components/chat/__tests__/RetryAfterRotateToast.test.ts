import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import RetryAfterRotateToast from '@/components/chat/RetryAfterRotateToast.vue';
import { setConnectionState } from '@/lib/useConnectionState';
import type { RetryAfterRotationFailedPayload } from '@/lib/types';

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

const samplePayload: RetryAfterRotationFailedPayload = {
  sub_id: 'sub-2',
  session_id: 'sess-1',
  profile_id: 'prof-openai',
  error_message: 'context length exceeded',
};

function mountToast(initial: RetryAfterRotationFailedPayload | null = null) {
  return mount(RetryAfterRotateToast, { props: { initial } });
}

describe('RetryAfterRotateToast', () => {
  beforeEach(() => {
    installFakeRuntime();
    setConnectionState('ready');
  });
  afterEach(() => {
    uninstallRuntime();
  });

  it('renders nothing when no failure is queued', () => {
    const w = mountToast();
    expect(w.find('[data-testid="retry-after-rotate-toast"]').exists()).toBe(false);
  });

  it('renders the toast for an initial seed', () => {
    const w = mountToast(samplePayload);
    expect(w.find('[data-testid="retry-after-rotate-toast"]').exists()).toBe(true);
    expect(w.find('[data-testid="retry-after-rotate-body"]').text()).toContain('key works');
  });

  it('appears when provider:retry-after-rotation-failed fires', async () => {
    const rt = installFakeRuntime();
    const w = mountToast();
    rt.emit('provider:retry-after-rotation-failed', samplePayload);
    await flushPromises();
    expect(w.find('[data-testid="retry-after-rotate-toast"]').exists()).toBe(true);
  });

  it('shows/hides error detail on toggle', async () => {
    const w = mountToast(samplePayload);
    expect(w.text()).not.toContain('context length exceeded');
    await w.find('[data-testid="retry-after-rotate-detail"]').trigger('click');
    expect(w.text()).toContain('context length exceeded');
    await w.find('[data-testid="retry-after-rotate-detail"]').trigger('click');
    expect(w.text()).not.toContain('context length exceeded');
  });

  it('dedupes: two events for same profileID → one toast', async () => {
    const rt = installFakeRuntime();
    const w = mountToast();
    rt.emit('provider:retry-after-rotation-failed', samplePayload);
    rt.emit('provider:retry-after-rotation-failed', {
      ...samplePayload,
      error_message: 'again',
    });
    await flushPromises();
    expect((w.vm as unknown as { queue: unknown[] }).queue).toHaveLength(1);
  });

  it('dismiss emits dismissed and clears the toast', async () => {
    const w = mountToast(samplePayload);
    await w.find('[data-testid="retry-after-rotate-dismiss"]').trigger('click');
    expect(w.emitted('dismissed')?.[0]).toEqual(['prof-openai']);
    expect(w.find('[data-testid="retry-after-rotate-toast"]').exists()).toBe(false);
  });
});
