/**
 * useEventToasts.spec.ts — engineer-truth-pass-01PMTP01 WP08 (B16, B17).
 *
 * Mounts a host component that calls `useEventToasts()` ALONGSIDE the
 * real `ToastRoot.vue` (the component that actually renders toasts to
 * the DOM), fires broker events through a fake Wails runtime, and
 * asserts on the rendered DOM — not just on the composable's internal
 * state. CLAUDE.md's release-ritual doctrine calls this out explicitly:
 * "a mounted surface can still be dead — proving the field is non-nil
 * is not proving the toast appears." This file is the "toast actually
 * fires end-to-end" proof for both findings:
 *
 *   B16 — branches:merge-suggested → a toast with a working "Merge now"
 *         button that calls client.branches.merge(branchId).
 *   B17 — provider:retry-after-rotation-failed → a toast telling the
 *         user their post-rotation retry failed, deduped per profile,
 *         and cleared by provider:auth-resumed (the clearer that exists
 *         solely to dismiss this toast).
 *
 * Before this WP, useEventToasts.ts had zero test coverage at all.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { defineComponent, h } from 'vue';
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils';
import { useEventToasts } from '@/composables/useEventToasts';
import ToastRoot from '@/components/ui/ToastRoot.vue';
import { _resetToastQueue } from '@/composables/useToastQueue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { setConnectionState } from '@/lib/useConnectionState';

// ---------------------------------------------------------------------------
// Fake Wails runtime (mirrors AppMenuBroker.spec.ts / useEventStream.test.ts)
// ---------------------------------------------------------------------------
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

function uninstallRuntime() {
  delete (window as unknown as { runtime?: unknown }).runtime;
}

// Host mounts useEventToasts() (the subscriber under test) alongside
// ToastRoot (the real renderer) — so a click on the DOM button exercises
// the actual invokeAction() -> action.perform() path, not a mock of it.
const Host = defineComponent({
  setup() {
    useEventToasts();
    return () => h(ToastRoot);
  },
});

describe('useEventToasts — B16/B17 end-to-end (broker event -> rendered toast -> action)', () => {
  let rt: FakeRuntime;
  let client: ReturnType<typeof createFakeHarnessClient>;
  let wrapper: VueWrapper | undefined;

  beforeEach(() => {
    rt = installFakeRuntime();
    setConnectionState('ready');
    _resetToastQueue();
    client = createFakeHarnessClient();
    // Avoid the unrelated one-time migration toast polluting assertions.
    client.settings.getPermissionsMigrationToastShown = async () => true;
    client.branches.merge = vi.fn(async () => undefined);
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

  it('B16: branches:merge-suggested renders a toast whose "Merge now" button calls client.branches.merge', async () => {
    const w = mountHost();
    await flushPromises();

    rt.emit('branches:merge-suggested', {
      branchId: 'br-1',
      reason: 'terminal_state_token',
      detail: 'contains a terminal-state marker',
    });
    await flushPromises();

    const text = w.text();
    expect(text).toContain('Branch reply looks terminal');
    expect(text).toContain('contains a terminal-state marker');

    const mergeButton = w.find('button.toast-action');
    expect(mergeButton.exists()).toBe(true);
    expect(mergeButton.text()).toBe('Merge now');

    await mergeButton.trigger('click');
    await flushPromises();

    expect(client.branches.merge).toHaveBeenCalledOnce();
    expect(client.branches.merge).toHaveBeenCalledWith('br-1');
    // dismissAfter defaults true — the toast is gone after the click.
    expect(w.find('.toast').exists()).toBe(false);
  });

  it('B16: a second suggestion for the same branch is deduped (no second toast)', async () => {
    const w = mountHost();
    await flushPromises();

    rt.emit('branches:merge-suggested', { branchId: 'br-2', reason: 'idle_timeout' });
    await flushPromises();
    rt.emit('branches:merge-suggested', { branchId: 'br-2', reason: 'idle_timeout' });
    await flushPromises();

    expect(w.findAll('.toast').length).toBe(1);
  });

  it('B17: provider:retry-after-rotation-failed renders a toast telling the user the retry failed', async () => {
    const w = mountHost();
    await flushPromises();

    rt.emit('provider:retry-after-rotation-failed', {
      sub_id: 'sub-1',
      session_id: 'sess-1',
      profile_id: 'prof-9',
      error_message: 'still invalid',
    });
    await flushPromises();

    expect(w.text()).toContain('Your key works, but the request still failed');
    expect(w.find('button.toast-action').text()).toBe('View error');
  });

  it('B17: a second failure for the same profile is deduped (no second toast)', async () => {
    const w = mountHost();
    await flushPromises();

    rt.emit('provider:retry-after-rotation-failed', {
      sub_id: 'sub-1',
      session_id: 'sess-1',
      profile_id: 'prof-10',
      error_message: 'still invalid',
    });
    await flushPromises();
    rt.emit('provider:retry-after-rotation-failed', {
      sub_id: 'sub-2',
      session_id: 'sess-1',
      profile_id: 'prof-10',
      error_message: 'still invalid again',
    });
    await flushPromises();

    expect(w.findAll('.toast').length).toBe(1);
  });

  it('B17: provider:auth-resumed dismisses the pending retry-after-rotation toast (the clearer)', async () => {
    const w = mountHost();
    await flushPromises();

    rt.emit('provider:retry-after-rotation-failed', {
      sub_id: 'sub-3',
      session_id: 'sess-2',
      profile_id: 'prof-11',
      error_message: 'nope',
    });
    await flushPromises();
    expect(w.findAll('.toast').length).toBe(1);

    rt.emit('provider:auth-resumed', { profile_id: 'prof-11', new_sub_id: 'sub-4' });
    await flushPromises();

    expect(w.findAll('.toast').length).toBe(0);
  });

  it('B17: provider:auth-resumed for a DIFFERENT profile does not dismiss an unrelated toast', async () => {
    const w = mountHost();
    await flushPromises();

    rt.emit('provider:retry-after-rotation-failed', {
      sub_id: 'sub-5',
      session_id: 'sess-3',
      profile_id: 'prof-12',
      error_message: 'nope',
    });
    await flushPromises();
    expect(w.findAll('.toast').length).toBe(1);

    rt.emit('provider:auth-resumed', { profile_id: 'some-other-profile', new_sub_id: 'sub-6' });
    await flushPromises();

    expect(w.findAll('.toast').length).toBe(1);
  });
});
