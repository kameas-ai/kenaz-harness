import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import SessionsView from '@/views/sessions/SessionsView.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';
import type { ListMessagesResult, Message, Provider } from '@/lib/types';
import { setConnectionState } from '@/lib/useConnectionState';

/**
 * CompactedHistory.spec — WP07 acceptance tests for the
 * compaction-strategy-ui mission.
 *
 *   - Default state hits listMessagesActive (archived rows hidden).
 *   - Toggle ON refetches via listMessagesAll, archived rows appear.
 *   - Summary row renders the "Summary of N turns" indicator.
 *   - Archived row chip carries the right summary id (for jump).
 *   - SweptCount > 0 renders the placeholder; sweptCount === 0 does not.
 */

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

function makeMessage(overrides: Partial<Message>): Message {
  return {
    id: 'm-1',
    sessionId: 's-1',
    role: 'user',
    content: 'hello',
    createdAt: '2026-04-25T00:00:00Z',
    ...overrides,
  };
}

const providers: Provider[] = [
  {
    id: 'anthropic-p-1',
    name: 'Anthropic',
    tier: 'cloud',
    kind: 'anthropic',
    model: 'claude',
  },
];

// Build a session whose history has been compacted: two archived
// originals fold into a single synthetic summary row, then a fresh
// user/assistant exchange tails the transcript. The active view (no
// archived rows) skips m-arch-1 and m-arch-2; the full view returns
// every row including the archived pair.
const SUMMARY_ID = 'm-summary';
const archivedOne = makeMessage({
  id: 'm-arch-1',
  role: 'user',
  content: 'archived turn 1',
  archivedAt: '2026-04-22T00:00:00Z',
  compactedIntoId: SUMMARY_ID,
});
const archivedTwo = makeMessage({
  id: 'm-arch-2',
  role: 'assistant',
  content: 'archived turn 2',
  archivedAt: '2026-04-22T00:00:00Z',
  compactedIntoId: SUMMARY_ID,
});
const summary = makeMessage({
  id: SUMMARY_ID,
  role: 'system',
  content: '[Earlier conversation summary: ...]',
  compactedAt: '2026-04-22T00:00:00Z',
});
const liveOne = makeMessage({
  id: 'm-live-1',
  role: 'user',
  content: 'How are you today?',
});
const liveTwo = makeMessage({
  id: 'm-live-2',
  role: 'assistant',
  content: 'Doing well, thanks.',
});

const ACTIVE_RESULT: ListMessagesResult = {
  messages: [summary, liveOne, liveTwo],
  sweptCount: 0,
};
const ALL_RESULT: ListMessagesResult = {
  messages: [archivedOne, archivedTwo, summary, liveOne, liveTwo],
  sweptCount: 0,
};

interface Calls {
  active: number;
  all: number;
}

function makeSessionsClient(
  calls: Calls,
  overrides?: { activeResult?: ListMessagesResult; allResult?: ListMessagesResult },
): Partial<HarnessClient['sessions']> {
  return {
    list: async () => [],
    get: async (id) => ({ id, name: 'Compacted', createdAt: '', updatedAt: '' }),
    create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
    rename: async () => undefined,
    delete: async () => undefined,
    reorder: async () => undefined,
    startStream: async () => 'sub',
    stopStream: async () => undefined,
    listMessages: async () => ACTIVE_RESULT.messages,
    listMessagesActive: async () => {
      calls.active += 1;
      return overrides?.activeResult ?? ACTIVE_RESULT;
    },
    listMessagesAll: async () => {
      calls.all += 1;
      return overrides?.allResult ?? ALL_RESULT;
    },
    appendMessage: async (id, role, content) =>
      makeMessage({ id: 'new', sessionId: id, role, content }),
    saveDraft: async () => undefined,
    loadDraft: async () => '',
  };
}

async function mountWithRoute(seed: Partial<HarnessClient>) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/sessions/:id?', component: SessionsView },
      {
        path: '/providers',
        component: defineComponent({ render: () => h('div', 'providers') }),
      },
    ],
  });
  await router.push('/sessions/s-1');
  await router.isReady();

  const w = mount(SessionsView, {
    global: {
      plugins: [
        router,
        {
          install(app) {
            provideFakeClient(app, seed);
          },
        },
      ],
    },
  });
  await flushPromises();
  return { w, router };
}

describe('SessionsView (WP07 compacted history)', () => {
  beforeEach(() => {
    installFakeRuntime();
    setConnectionState('ready');
  });
  afterEach(() => {
    uninstallRuntime();
  });

  it('default state calls listMessagesActive and hides archived rows', async () => {
    const calls: Calls = { active: 0, all: 0 };
    const { w } = await mountWithRoute({
      sessions: makeSessionsClient(calls) as HarnessClient['sessions'],
      llm: {
        listProviders: async () => providers,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
      } as any,
    });
    await flushPromises();

    expect(calls.active).toBeGreaterThanOrEqual(1);
    expect(calls.all).toBe(0);

    // Archived rows MUST NOT appear in the default scrollback.
    expect(w.text()).not.toContain('archived turn 1');
    expect(w.text()).not.toContain('archived turn 2');

    // Toggle button reads "Show full history" before any flip.
    const toggle = w.find('[data-testid="show-full-history-toggle"]');
    expect(toggle.exists()).toBe(true);
    expect(toggle.text()).toContain('Show full history');

    w.unmount();
  });

  it('renders the "Summary of N turns" indicator on the summary row', async () => {
    const calls: Calls = { active: 0, all: 0 };
    const { w } = await mountWithRoute({
      sessions: makeSessionsClient(calls) as HarnessClient['sessions'],
      llm: {
        listProviders: async () => providers,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
      } as any,
    });
    await flushPromises();

    // The summary indicator only renders once active list has been
    // fetched; flip to full history first so the archived rows
    // contribute to the summary fold count.
    await w.find('[data-testid="show-full-history-toggle"]').trigger('click');
    await flushPromises();

    const indicator = w.find('[data-testid="message-summary-indicator"]');
    expect(indicator.exists()).toBe(true);
    expect(indicator.text()).toContain('Summary of 2 turns');

    w.unmount();
  });

  it('toggle ON fetches via listMessagesAll and reveals archived rows', async () => {
    const calls: Calls = { active: 0, all: 0 };
    const { w } = await mountWithRoute({
      sessions: makeSessionsClient(calls) as HarnessClient['sessions'],
      llm: {
        listProviders: async () => providers,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
      } as any,
    });
    await flushPromises();

    expect(calls.all).toBe(0);

    await w.find('[data-testid="show-full-history-toggle"]').trigger('click');
    await flushPromises();

    expect(calls.all).toBeGreaterThanOrEqual(1);
    expect(w.text()).toContain('archived turn 1');
    expect(w.text()).toContain('archived turn 2');

    // Each archived row carries the archived tag + a chip pointing
    // to the summary it folded into.
    const archivedTags = w.findAll('[data-testid="message-archived-tag"]');
    expect(archivedTags.length).toBeGreaterThanOrEqual(2);
    const jumpChips = w.findAll('[data-testid="message-archived-jump"]');
    expect(jumpChips.length).toBeGreaterThanOrEqual(2);
    for (const chip of jumpChips) {
      expect(chip.attributes('data-summary-id')).toBe(SUMMARY_ID);
    }

    // Flipping back hides them again and restores the toggle copy.
    await w.find('[data-testid="show-full-history-toggle"]').trigger('click');
    await flushPromises();
    expect(w.text()).not.toContain('archived turn 1');
    expect(w.text()).not.toContain('archived turn 2');

    w.unmount();
  });

  it('renders the swept-count placeholder when sweptCount > 0', async () => {
    const calls: Calls = { active: 0, all: 0 };
    const { w } = await mountWithRoute({
      sessions: makeSessionsClient(calls, {
        activeResult: { messages: [summary, liveOne, liveTwo], sweptCount: 7 },
      }) as HarnessClient['sessions'],
      llm: {
        listProviders: async () => providers,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
      } as any,
    });
    await flushPromises();

    const placeholder = w.find('[data-testid="swept-history-placeholder"]');
    expect(placeholder.exists()).toBe(true);
    expect(placeholder.text()).toContain('7 earlier turns no longer available');

    w.unmount();
  });

  it('does NOT render the swept-count placeholder when sweptCount === 0', async () => {
    const calls: Calls = { active: 0, all: 0 };
    const { w } = await mountWithRoute({
      sessions: makeSessionsClient(calls) as HarnessClient['sessions'],
      llm: {
        listProviders: async () => providers,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
      } as any,
    });
    await flushPromises();

    const placeholder = w.find('[data-testid="swept-history-placeholder"]');
    expect(placeholder.exists()).toBe(false);

    w.unmount();
  });
});
