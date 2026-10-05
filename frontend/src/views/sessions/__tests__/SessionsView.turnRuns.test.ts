/**
 * SessionsView — turn -> run links (agentgraph-settings-linkage-01DOGF0D
 * WP04, pins P-5 / P-6 at the view boundary).
 *
 * MessageList.runLinks.test.ts pins the strip's rendering; this pins the
 * wiring that feeds it: the session surface fetches Sessions_TurnRuns and
 * a completed turn carries router links to both run routes (the
 * "reachable from where the user is" invariant), while a served build
 * neither calls the binding (Graph_* has no serve dispatch, D-701) nor
 * renders a link that would dead-end.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import SessionsView from '@/views/sessions/SessionsView.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { setConnectionState } from '@/lib/useConnectionState';
import type { Message, TurnRun } from '@/lib/types';

const served = vi.hoisted(() => ({ on: false }));
vi.mock('@/lib/useServedMode', async (orig) => ({
  ...(await orig<typeof import('@/lib/useServedMode')>()),
  isServedMode: () => served.on,
}));

const SID = 'sess-runs';
const RUN = 'chat-01J9ZZZZZZZZZZZZZZZZZZZZZZ';

const MESSAGES: Message[] = [
  { id: 'u-1', sessionId: SID, role: 'user', content: 'What changed?', createdAt: '2026-10-04T00:00:00Z' },
  {
    id: 'a-1',
    sessionId: SID,
    role: 'assistant',
    content: 'Two files changed.',
    createdAt: '2026-10-04T00:00:01Z',
    kind: 'final',
    moveIndex: 0,
    turnSpanId: 'u-1',
  },
];

async function mountView(opts: { subagent?: boolean } = {}) {
  const turnRuns = vi.fn(async (): Promise<TurnRun[]> => [
    { runId: RUN, turnSpanId: 'u-1', graphId: 'chat_default', specDigest: 'sha256:ab', createdAt: '2026-10-04T00:00:00Z' },
  ]);
  const base = createFakeHarnessClient();
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/sessions/:id?', component: SessionsView },
      { path: '/agentgraph/run/:runId', component: defineComponent({ render: () => h('div') }) },
      { path: '/agentgraph/run/:runId/graph', component: defineComponent({ render: () => h('div') }) },
      { path: '/providers', component: defineComponent({ render: () => h('div') }) },
    ],
  });
  await router.push(`/sessions/${SID}`);
  await router.isReady();
  const w = mount(SessionsView, {
    global: {
      plugins: [
        router,
        {
          install(app) {
            provideFakeClient(app, {
              sessions: {
                ...base.sessions,
                get: async () => ({
                  id: SID,
                  name: 'runs',
                  createdAt: '2026-10-04T00:00:00Z',
                  updatedAt: '2026-10-04T00:00:00Z',
                  systemPrompt: '',
                  contextKind: 'system' as const,
                }),
                listMessagesActive: async () => ({ messages: MESSAGES, sweptCount: 0 }),
                turnRuns,
              },
              // review L8: the subagent transcript is a second MessageList
              // mount (inside SubagentTab's #transcript slot). It renders
              // when BranchSidebar lists a running subagent branch whose
              // child session is the one on screen.
              branches: {
                ...base.branches,
                list: async () =>
                  opts.subagent
                    ? [
                        {
                          id: 'br-1',
                          parentSessionId: 'sess-parent',
                          childSessionId: SID,
                          kind: 'fork' as const,
                          status: 'active' as const,
                          createdAt: '2026-10-04T00:00:00Z',
                          updatedAt: '2026-10-04T00:00:00Z',
                          subagentBranch: true,
                          subagentStatus: 'running' as const,
                          profileId: 'p',
                        },
                      ]
                    : [],
              },
            });
          },
        },
      ],
    },
    attachTo: document.body,
  });
  await flushPromises();
  return { w, turnRuns };
}

describe('SessionsView — turn -> run links', () => {
  afterEach(() => {
    served.on = false;
  });

  it('P-5: a completed turn in the transcript links to the run graph and RunView', async () => {
    setConnectionState('ready');
    const { w, turnRuns } = await mountView();
    expect(turnRuns).toHaveBeenCalledWith(SID);
    expect(w.get('[data-testid="turn-run-graph-link"]').attributes('href')).toBe(
      `/agentgraph/run/${RUN}/graph`,
    );
    expect(w.get('[data-testid="turn-run-details-link"]').attributes('href')).toBe(
      `/agentgraph/run/${RUN}`,
    );
    w.unmount();
  });

  it('P-6: a served build neither fetches turn runs nor renders a run link', async () => {
    served.on = true;
    setConnectionState('ready');
    const { w, turnRuns } = await mountView();
    expect(turnRuns).not.toHaveBeenCalled();
    expect(w.find('[data-testid="turn-run-links"]').exists()).toBe(false);
    expect(w.find('[data-testid="turn-run-unrecorded"]').exists()).toBe(false);
    w.unmount();
  });

  it('L8: the subagent transcript mount links the turn too', async () => {
    setConnectionState('ready');
    const { w } = await mountView({ subagent: true });
    const tab = w.get('[data-testid="session-subagent-tab"]');
    expect(tab.get('[data-testid="turn-run-graph-link"]').attributes('href')).toBe(
      `/agentgraph/run/${RUN}/graph`,
    );
    w.unmount();
  });
});
