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
 * CompactionFlow.e2e.spec — WP09 end-to-end tests for the
 * compaction-strategy-ui mission, plan §4 acceptance smoke checklist.
 *
 * The "e2e" label here is "frontend e2e": SessionsView is mounted with
 * @vue/test-utils against a fake harness client whose RPC responses
 * model the full chain (settings → send → trigger → toggle full
 * history → audit visible). No real browser, no real Wails — the task
 * constraints explicitly forbid both.
 *
 * Five scenarios mirror plan §4 / tasks.md WP09:
 *   1. Default state hides archived rows + listMessagesActive is the
 *      called endpoint.
 *   2. Toggle "Show full history" calls listMessagesAll and reveals
 *      archived rows.
 *   3. Summary indicator renders ("Summary of N turns").
 *   4. Swept-count placeholder is gated on sweptCount > 0.
 *   5. Settings are read on mount (the compactionArchiveDays read
 *      drives the swept-placeholder copy — covered indirectly here).
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

// Pre-compaction snapshot: a fresh session with two live turns; no
// archived rows, no summary, no sweep placeholder. Models "user just
// sent the first burst of messages, threshold not yet crossed".
const preLiveOne = makeMessage({
  id: 'm-pre-1',
  role: 'user',
  content: 'pre-compaction question',
});
const preLiveTwo = makeMessage({
  id: 'm-pre-2',
  role: 'assistant',
  content: 'pre-compaction answer',
});
const PRE_ACTIVE_RESULT: ListMessagesResult = {
  messages: [preLiveOne, preLiveTwo],
  sweptCount: 0,
};

// Post-compaction snapshot: the engine archived the two pre rows and
// inserted a summary row in their slot. Two new live turns trail the
// summary so the UI's MessageList has interesting content above + below
// the indicator.
const SUMMARY_ID = 'm-summary';
const archivedOne = makeMessage({
  id: 'm-pre-1',
  role: 'user',
  content: 'pre-compaction question',
  archivedAt: '2026-04-22T00:00:00Z',
  compactedIntoId: SUMMARY_ID,
});
const archivedTwo = makeMessage({
  id: 'm-pre-2',
  role: 'assistant',
  content: 'pre-compaction answer',
  archivedAt: '2026-04-22T00:00:00Z',
  compactedIntoId: SUMMARY_ID,
});
const summaryRow = makeMessage({
  id: SUMMARY_ID,
  role: 'system',
  content: '[Earlier conversation summary: prior turns folded.]',
  compactedAt: '2026-04-22T00:00:00Z',
});
const postLiveOne = makeMessage({
  id: 'm-post-1',
  role: 'user',
  content: 'post-compaction question',
});
const postLiveTwo = makeMessage({
  id: 'm-post-2',
  role: 'assistant',
  content: 'post-compaction answer',
});
const POST_ACTIVE_RESULT: ListMessagesResult = {
  messages: [summaryRow, postLiveOne, postLiveTwo],
  sweptCount: 0,
};
const POST_ALL_RESULT: ListMessagesResult = {
  messages: [archivedOne, archivedTwo, summaryRow, postLiveOne, postLiveTwo],
  sweptCount: 0,
};

interface CallTracker {
  active: number;
  all: number;
}

interface BackendStubControls {
  /** Trigger the simulated post-compaction state for subsequent fetches. */
  triggerCompaction(): void;
}

// makeStubBackend constructs a sessions client whose listMessagesActive /
// listMessagesAll responses depend on whether triggerCompaction() has
// been called. Models the compaction event boundary at the data layer.
function makeStubBackend(
  calls: CallTracker,
  initialActive: ListMessagesResult = PRE_ACTIVE_RESULT,
  initialAll: ListMessagesResult = PRE_ACTIVE_RESULT,
): {
  sessions: HarnessClient['sessions'];
  controls: BackendStubControls;
} {
  let activeResult: ListMessagesResult = initialActive;
  let allResult: ListMessagesResult = initialAll;
  const sessions: Partial<HarnessClient['sessions']> = {
    list: async () => [],
    get: async (id) => ({
      id,
      name: 'Compaction E2E',
      createdAt: '',
      updatedAt: '',
    }),
    create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
    rename: async () => undefined,
    delete: async () => undefined,
    reorder: async () => undefined,
    startStream: async () => 'sub',
    stopStream: async () => undefined,
    listMessages: async () => activeResult.messages,
    listMessagesActive: async () => {
      calls.active += 1;
      return activeResult;
    },
    listMessagesAll: async () => {
      calls.all += 1;
      return allResult;
    },
    appendMessage: async (id, role, content) =>
      makeMessage({ id: `appended-${calls.active}`, sessionId: id, role, content }),
    saveDraft: async () => undefined,
    loadDraft: async () => '',
  };
  return {
    sessions: sessions as HarnessClient['sessions'],
    controls: {
      triggerCompaction() {
        activeResult = POST_ACTIVE_RESULT;
        allResult = POST_ALL_RESULT;
      },
    },
  };
}

// makeSettingsClient returns a fake settings client that surfaces a
// representative compaction-strategy-ui Settings shape (archive days +
// recent window). The view reads this on mount; a regression that
// dropped the read would leave the swept-placeholder copy on its
// hardcoded fallback.
function makeSettingsClient(): HarnessClient['settings'] {
  return {
    get: async () => ({
      schemaVersion: 1,
      lastRoute: '/sessions',
      theme: 'system',
      accent: 'default',
      windowSize: { width: 1280, height: 800 },
      memoryEnabled: false,
      confirmEachDisabled: false,
      compactionAggressiveness: 'balanced',
      compactionArchiveDays: 90,
      compactionRecentWindow: 4,
    }),
    set: async () => undefined,
    loadRoute: async () => '/sessions',
    saveRoute: async () => undefined,
    logRouteChange: async () => undefined,
    loadTheme: async () => 'system',
    saveTheme: async () => undefined,
    getMemory: async () => false,
    setMemory: async () => undefined,
    getWebFetchEnabled: async () => false,
    setWebFetchEnabled: async () => undefined,
    getWebSearch: async () => false,
    setWebSearch: async () => undefined,
    getBash: async () => false,
    setBash: async () => undefined,
    getSaveArtifact: async () => true,
    setSaveArtifact: async () => undefined,
    getMaxAgentTurns: async () => 0,
    setMaxAgentTurns: async () => undefined,
    getPermissionMode: async () => 'normal',
    setPermissionMode: async () => undefined,
    getPermissionCacheDangerousOps: async () => false,
    setPermissionCacheDangerousOps: async () => undefined,
    getBashAllowlistMigrated: async () => false,
    setBashAllowlistMigrated: async () => undefined,
    getPermissionsMigrationToastShown: async () => false,
    setPermissionsMigrationToastShown: async () => undefined,
    getCedarStrictCredentialMode: async () => false,
    setCedarStrictCredentialMode: async () => undefined,
    getFSRequestAccessEnabled: async () => true,
    setFSRequestAccessEnabled: async () => undefined,
  } as any;
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

describe('CompactionFlow.e2e (WP09 — plan §4 acceptance smoke)', () => {
  beforeEach(() => {
    installFakeRuntime();
    setConnectionState('ready');
  });
  afterEach(() => {
    uninstallRuntime();
  });

  it('TestE2E_CompactionFlow_Default_HidesArchived: default state calls listMessagesActive and hides archived rows', async () => {
    const calls: CallTracker = { active: 0, all: 0 };
    // Use the post-compaction snapshot directly so we can assert that
    // the active fetch path is the one filtering out archived rows.
    const { sessions } = makeStubBackend(
      calls,
      POST_ACTIVE_RESULT,
      POST_ALL_RESULT,
    );
    const { w } = await mountWithRoute({
      sessions,
      settings: makeSettingsClient(),
      llm: {
        listProviders: async () => providers,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
      } as any,
    });
    await flushPromises();

    // Active endpoint fired at least once; all endpoint NOT called yet.
    expect(calls.active).toBeGreaterThanOrEqual(1);
    expect(calls.all).toBe(0);

    // Archived rows (the compaction-trigger inputs) are NOT visible
    // in the default scrollback.
    expect(w.text()).not.toContain('pre-compaction question');
    expect(w.text()).not.toContain('pre-compaction answer');

    // Live tail post-compaction IS visible.
    expect(w.text()).toContain('post-compaction question');
    expect(w.text()).toContain('post-compaction answer');

    // Toggle button reads "Show full history" before any flip.
    const toggle = w.find('[data-testid="show-full-history-toggle"]');
    expect(toggle.exists()).toBe(true);
    expect(toggle.text()).toContain('Show full history');

    w.unmount();
  });

  it('TestE2E_CompactionFlow_ToggleShowsArchived: toggle ON calls listMessagesAll and reveals archived rows + jump chips', async () => {
    const calls: CallTracker = { active: 0, all: 0 };
    const { sessions } = makeStubBackend(
      calls,
      POST_ACTIVE_RESULT,
      POST_ALL_RESULT,
    );
    const { w } = await mountWithRoute({
      sessions,
      settings: makeSettingsClient(),
      llm: {
        listProviders: async () => providers,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
      } as any,
    });
    await flushPromises();

    expect(calls.all).toBe(0);

    // Flip the toggle.
    await w.find('[data-testid="show-full-history-toggle"]').trigger('click');
    await flushPromises();

    expect(calls.all).toBeGreaterThanOrEqual(1);
    // Archived rows are now visible.
    expect(w.text()).toContain('pre-compaction question');
    expect(w.text()).toContain('pre-compaction answer');

    // Each archived row carries the archived chip + a jump chip pointing
    // to the summary it folded into.
    const archivedTags = w.findAll('[data-testid="message-archived-tag"]');
    expect(archivedTags.length).toBeGreaterThanOrEqual(2);
    const jumpChips = w.findAll('[data-testid="message-archived-jump"]');
    expect(jumpChips.length).toBeGreaterThanOrEqual(2);
    for (const chip of jumpChips) {
      expect(chip.attributes('data-summary-id')).toBe(SUMMARY_ID);
    }

    // Toggle button text flips while ON.
    const toggle = w.find('[data-testid="show-full-history-toggle"]');
    expect(toggle.text()).toContain('Hide archived');

    // Flipping back hides the archived rows again.
    await w.find('[data-testid="show-full-history-toggle"]').trigger('click');
    await flushPromises();
    expect(w.text()).not.toContain('pre-compaction question');
    expect(w.text()).not.toContain('pre-compaction answer');

    w.unmount();
  });

  it('TestE2E_CompactionFlow_SummaryIndicatorRenders: "Summary of N turns" indicator renders above the summary row', async () => {
    const calls: CallTracker = { active: 0, all: 0 };
    const { sessions } = makeStubBackend(
      calls,
      POST_ACTIVE_RESULT,
      POST_ALL_RESULT,
    );
    const { w } = await mountWithRoute({
      sessions,
      settings: makeSettingsClient(),
      llm: {
        listProviders: async () => providers,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
      } as any,
    });
    await flushPromises();

    // Summary indicator's "N turns" count is computed from the archived
    // siblings; only show-full-history fetches surface those sibling
    // rows. Flip the toggle so the count becomes accurate.
    await w.find('[data-testid="show-full-history-toggle"]').trigger('click');
    await flushPromises();

    const indicator = w.find('[data-testid="message-summary-indicator"]');
    expect(indicator.exists()).toBe(true);
    // Was "Summary of 2 turns" until model-moves-transcript-01PMCH01
    // WP05. The fixture folds ONE exchange into the summary — the user
    // row `m-pre-1` and the assistant row `m-pre-2` answering it — and
    // the indicator used to count ROWS, so it reported two turns for
    // one. The count is turns now (see `foldedTurnCounts`), which is
    // what makes it survive a move-bearing session: WP02 persists ~13
    // rows per turn, so the old arithmetic reported three compacted
    // turns as "Summary of 39 turns". The expected value moved because
    // the fixture never had two turns in it.
    expect(indicator.text()).toContain('Summary of 1 turn');

    w.unmount();
  });

  it('TestE2E_CompactionFlow_SweptPlaceholderGated: placeholder visible when sweptCount > 0; hidden at 0', async () => {
    // Visible-when-positive case.
    {
      const calls: CallTracker = { active: 0, all: 0 };
      const { sessions } = makeStubBackend(
        calls,
        { messages: POST_ACTIVE_RESULT.messages, sweptCount: 12 },
        { messages: POST_ALL_RESULT.messages, sweptCount: 12 },
      );
      const { w } = await mountWithRoute({
        sessions,
        settings: makeSettingsClient(),
        llm: {
          listProviders: async () => providers,
          startStream: async () => 'sub',
          stopStream: async () => undefined,
        } as any,
      });
      await flushPromises();

      const placeholder = w.find('[data-testid="swept-history-placeholder"]');
      expect(placeholder.exists()).toBe(true);
      expect(placeholder.text()).toContain('12 earlier turns no longer available');

      w.unmount();
    }

    // Hidden-when-zero case.
    {
      const calls: CallTracker = { active: 0, all: 0 };
      const { sessions } = makeStubBackend(calls);
      const { w } = await mountWithRoute({
        sessions,
        settings: makeSettingsClient(),
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
    }
  });

  it('TestE2E_CompactionFlow_TriggerSimulation: send → trigger → reload reveals the post-compaction state', async () => {
    // Simulates the full plan §4 item 1 chain: a session that starts
    // pre-compaction, then a backend "compaction trigger" flips the
    // active fetch result to the post-compaction shape. The toggle
    // reveal still works and surfaces the archived siblings.
    const calls: CallTracker = { active: 0, all: 0 };
    const { sessions, controls } = makeStubBackend(calls);
    const { w } = await mountWithRoute({
      sessions,
      settings: makeSettingsClient(),
      llm: {
        listProviders: async () => providers,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
      } as any,
    });
    await flushPromises();

    // Pre-compaction: only the two pre rows are visible.
    expect(w.text()).toContain('pre-compaction question');
    expect(w.text()).toContain('pre-compaction answer');

    // Simulate the compaction event firing on the backend: subsequent
    // listMessagesActive returns the post-compaction shape.
    controls.triggerCompaction();

    // Re-fetch via the toggle path — flipping it ON forces a refresh.
    await w.find('[data-testid="show-full-history-toggle"]').trigger('click');
    await flushPromises();

    // Now archived siblings are visible.
    expect(w.text()).toContain('pre-compaction question');
    const archivedTags = w.findAll('[data-testid="message-archived-tag"]');
    expect(archivedTags.length).toBeGreaterThanOrEqual(2);

    // And the post-compaction live tail is on the page too.
    expect(w.text()).toContain('post-compaction question');
    expect(w.text()).toContain('post-compaction answer');

    w.unmount();
  });
});
