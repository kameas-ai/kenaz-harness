/**
 * Dogfood 2026-10-05: the composer cost readout went up AND down.
 *
 * It read the per-turn lastUsage snapshot (whose cost falls when caching
 * shrinks a turn), not the conversation total. The footer now reads the
 * cumulative Sessions_GetUsage aggregate, loaded with the session and
 * refetched after every `session.usage.updated` event — so it is
 * monotonic across a conversation by construction (the backend sums).
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
  emit: (topic: string, payload: unknown) => void;
}

function installFakeRuntime(): FakeRuntime {
  const handlers = new Map<string, Set<(payload: unknown) => void>>();
  const rt: FakeRuntime = {
    EventsOn: (topic, cb) => {
      let set = handlers.get(topic);
      if (!set) {
        set = new Set();
        handlers.set(topic, set);
      }
      set.add(cb);
      return () => set!.delete(cb);
    },
    emit: (topic, payload) => {
      for (const cb of handlers.get(topic) ?? []) cb(payload);
    },
  };
  (window as unknown as { runtime: FakeRuntime }).runtime = rt;
  return rt;
}

describe('useSession — cumulative usage feeds the footer', () => {
  // The backend aggregate grows turn over turn even as per-turn cost falls.
  let aggregate = { promptTokens: 100, completionTokens: 50, totalTokens: 150, costUsd: 3.3, costSource: 'provider', messageCount: 4, pricingDataDate: '2026-10-01' };

  function seed(): Partial<HarnessClient> {
    return {
      sessions: {
        list: async () => [],
        get: async (id: string) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        listMessages: async () => [],
        saveDraft: async () => undefined,
        loadDraft: async () => '',
        getUsage: async () => ({ ...aggregate }),
      } as never,
      llm: { listProviders: async () => [] } as never,
    };
  }

  let rt: FakeRuntime;

  beforeEach(() => {
    rt = installFakeRuntime();
    setConnectionState('ready');
    aggregate = { promptTokens: 100, completionTokens: 50, totalTokens: 150, costUsd: 3.3, costSource: 'provider', messageCount: 4, pricingDataDate: '2026-10-01' };
  });

  afterEach(() => {
    delete (window as unknown as { runtime?: unknown }).runtime;
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
    mount(Host, {
      global: { plugins: [{ install: (app) => provideFakeClient(app, seed()) }] },
    });
    return { idRef, api: () => api };
  }

  async function settle() {
    for (let i = 0; i < 8; i++) await nextTick();
    await Promise.resolve();
    for (let i = 0; i < 8; i++) await nextTick();
  }

  it('loads the aggregate with the session and refetches after a turn — monotonic while per-turn cost falls', async () => {
    const { api } = boot();
    await settle();
    expect(api().cumulativeUsage.value?.costUsd).toBe(3.3);

    // Next turn: CHEAP (cached) — per-turn cost far below the total.
    aggregate = { ...aggregate, totalTokens: 200, costUsd: 3.4, messageCount: 5 };
    rt.emit('session.usage.updated', {
      sessionId: 's-1',
      promptTokens: 40,
      completionTokens: 10,
      totalTokens: 50,
      costUsd: 0.1, // the per-turn dip that used to show in the footer
    });
    await settle();
    expect(api().cumulativeUsage.value?.costUsd).toBe(3.4); // grew, never dipped
    expect(api().lastUsage.value?.costUsd).toBe(0.1); // per-turn snapshot intact
  });

  it("another session's usage event does not touch the aggregate", async () => {
    const { api } = boot();
    await settle();
    aggregate = { ...aggregate, costUsd: 99 };
    rt.emit('session.usage.updated', { sessionId: 'other', totalTokens: 1, costUsd: 1 });
    await settle();
    expect(api().cumulativeUsage.value?.costUsd).toBe(3.3);
  });
});
