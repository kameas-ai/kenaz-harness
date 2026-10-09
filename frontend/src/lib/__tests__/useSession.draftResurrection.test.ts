/**
 * Dogfood 2026-10-05: the composer resurrected the just-sent message.
 *
 * Drafts persist on a 400ms debounce; a session reload fires on stream
 * start/end. A reload landing after send read the STALE persisted draft
 * (the clear hadn't persisted yet — or, worse, the async save raced the
 * backend read) and wrote it back into the composer. The fix is the
 * once-per-session adoption rule: load() adopts the persisted draft only
 * on the first load after a session switch; mid-session reloads never
 * touch the composer. A clear also flushes the save immediately.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { defineComponent, h, ref, nextTick } from 'vue';
import { mount } from '@vue/test-utils';
import { useSession } from '@/lib/useSession';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';
import { setConnectionState } from '@/lib/useConnectionState';

interface FakeRuntime {
  EventsOn: (topic: string, cb: (payload: unknown) => void) => () => void;
}

function installFakeRuntime(): void {
  const rt: FakeRuntime = { EventsOn: () => () => {} };
  (window as unknown as { runtime: FakeRuntime }).runtime = rt;
}

describe('useSession — the persisted draft cannot resurrect sent text', () => {
  const savedDrafts: Array<{ id: string; text: string }> = [];
  // The backend's persisted value: deliberately stays stale ("whats our
  // status") for the whole test, simulating the save/read race.
  let persistedDraft = 'whats our status';

  function seed(): Partial<HarnessClient> {
    return {
      sessions: {
        list: async () => [],
        get: async (id: string) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        listMessages: async () => [],
        saveDraft: async (id: string, text: string) => {
          savedDrafts.push({ id, text });
        },
        loadDraft: async () => persistedDraft,
      } as never,
      llm: { listProviders: async () => [] } as never,
    };
  }

  beforeEach(() => {
    installFakeRuntime();
    setConnectionState('ready');
    savedDrafts.length = 0;
    persistedDraft = 'whats our status';
  });

  afterEach(() => {
    delete (window as unknown as { runtime?: unknown }).runtime;
    vi.useRealTimers();
  });

  function boot() {
    const idRef = ref<string>('s-1');
    let api!: ReturnType<typeof useSession>;
    const Host = defineComponent({
      setup() {
        api = useSession(idRef);
        return () => h('div');
      },
    });
    const w = mount(Host, {
      global: { plugins: [{ install: (app) => provideFakeClient(app, seed()) }] },
    });
    return { w, idRef, api: () => api };
  }

  async function settle() {
    // load() awaits several fake-client promises.
    for (let i = 0; i < 8; i++) await nextTick();
    await Promise.resolve();
    for (let i = 0; i < 8; i++) await nextTick();
  }

  it('adopts the persisted draft on first load, then never again mid-session', async () => {
    const { api } = boot();
    await settle();
    // First load restores the unsent text — the feature working as meant.
    expect(api().draft.value).toBe('whats our status');

    // The user sends: ChatInput clears the model value.
    api().draft.value = '';
    await settle();

    // A mid-session reload (stream end triggers one) with the backend
    // still returning the stale text.
    await api().refresh();
    await settle();
    expect(api().draft.value).toBe(''); // no resurrection

    // And typing fresh text also survives a reload un-clobbered.
    api().draft.value = 'next question';
    await api().refresh();
    await settle();
    expect(api().draft.value).toBe('next question');
  });

  it('a cleared draft is saved immediately, not after the debounce', async () => {
    vi.useFakeTimers();
    const { api } = boot();
    await settle();
    api().draft.value = '';
    await settle();
    const cleared = savedDrafts.filter((d) => d.text === '');
    expect(cleared.length).toBeGreaterThanOrEqual(1); // no 400ms window
    vi.useRealTimers();
  });

  // Dogfood 2026-10-08: type-then-Enter INSIDE the debounce window. No
  // save has fired yet, so lastSavedDraft is still "" — the send's clear
  // equals it, and the watcher used to early-return before cancelling
  // the queued save, which then persisted the just-sent text.
  it('a send within the debounce window never persists the sent text', async () => {
    vi.useFakeTimers();
    persistedDraft = '';
    const { api } = boot();
    await settle();
    expect(api().draft.value).toBe('');
    savedDrafts.length = 0;

    api().draft.value = 'Reply with exactly the word pong';
    await settle();
    vi.advanceTimersByTime(100); // well inside the 400ms debounce
    api().draft.value = ''; // the send clears the composer
    await settle();
    vi.advanceTimersByTime(1000);
    await settle();

    expect(savedDrafts.some((d) => d.text === 'Reply with exactly the word pong')).toBe(false);
    expect(savedDrafts.length).toBeGreaterThanOrEqual(1);
    expect(savedDrafts[savedDrafts.length - 1].text).toBe('');
    vi.useRealTimers();
  });

  it('a session switch adopts the new session persisted draft again', async () => {
    const { idRef, api } = boot();
    await settle();
    api().draft.value = '';
    await settle();
    persistedDraft = 'draft for session two';
    idRef.value = 's-2';
    await settle();
    expect(api().draft.value).toBe('draft for session two');
  });
});
