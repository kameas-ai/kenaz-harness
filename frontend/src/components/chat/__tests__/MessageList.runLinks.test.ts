/**
 * Turn -> run linkage on the session transcript
 * (agentgraph-settings-linkage-01DOGF0D WP04, pins P-3 frontend half, P-5,
 * P-6).
 *
 * P-5 is the anti-"demotion rot" invariant: the place the user already is
 * (the transcript) must carry a router link to BOTH run routes for a
 * completed turn — the materialized graph and RunView. If a later IA pass
 * drops the strip, these fail before the run surfaces become unreachable
 * again (agentgraph-total-convergence-01PMGX01 WP16's history).
 */

import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import MessageList from '@/components/chat/MessageList.vue';
import type { Message } from '@/lib/types';

function router() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/agentgraph/run/:runId', component: { template: '<div />' } },
      { path: '/agentgraph/run/:runId/graph', component: { template: '<div />' } },
    ],
  });
}

function row(overrides: Partial<Message>): Message {
  return {
    id: 'm',
    sessionId: 's-1',
    role: 'assistant',
    content: '',
    createdAt: '2026-10-04T00:00:00Z',
    ...overrides,
  };
}

const RUN = 'chat-01J9ZZZZZZZZZZZZZZZZZZZZZZ';

/** A recorded move-bearing turn followed by a pre-linkage classic turn. */
function transcript(): Message[] {
  return [
    row({ id: 'u-1', role: 'user', content: 'What changed?' }),
    row({ id: 'a-0', kind: 'assistant_move', moveIndex: 0, turnSpanId: 'u-1', content: 'Looking.' }),
    row({ id: 'a-1', kind: 'final', moveIndex: 1, turnSpanId: 'u-1', content: 'Two files changed.' }),
    row({ id: 'u-old', role: 'user', content: 'Older question' }),
    row({ id: 'a-old', content: 'Older classic answer' }),
  ];
}

function mountList(props: Record<string, unknown>) {
  return mount(MessageList, {
    props: { messages: transcript(), ...props },
    global: { plugins: [router()] },
  });
}

describe('MessageList — turn -> run links', () => {
  it('P-5: a completed recorded turn links to both run routes', () => {
    const w = mountList({ turnRuns: new Map([['u-1', RUN]]) });
    const graph = w.get('[data-testid="turn-run-graph-link"]');
    const details = w.get('[data-testid="turn-run-details-link"]');
    expect(graph.attributes('href')).toBe(`/agentgraph/run/${RUN}/graph`);
    expect(details.attributes('href')).toBe(`/agentgraph/run/${RUN}`);
    // Exactly one strip of links — on the answer, not on the moves or the
    // user bubble.
    expect(w.findAll('[data-testid="turn-run-links"]')).toHaveLength(1);
    const answer = w.get('[data-message-id="a-1"]');
    expect(answer.find('[data-testid="turn-run-graph-link"]').exists()).toBe(true);
  });

  it('a turn with no recorded run gets the reason, never a link', () => {
    const w = mountList({ turnRuns: new Map([['u-1', RUN]]) });
    const old = w.get('[data-message-id="a-old"]');
    expect(old.find('[data-testid="turn-run-graph-link"]').exists()).toBe(false);
    expect(old.get('[data-testid="turn-run-unrecorded"]').text()).toContain(
      'Run graph not recorded for this turn',
    );
  });

  it('P-6: no run map (served build) renders no strip and no dead link', () => {
    const w = mountList({});
    expect(w.find('[data-testid="turn-run-links"]').exists()).toBe(false);
    expect(w.find('[data-testid="turn-run-unrecorded"]').exists()).toBe(false);
    expect(w.html()).not.toContain('/agentgraph/run/');
  });

  // The live / just-committed turn is pinned through the REAL useSession
  // stream path in lib/__tests__/useSession.turnRunLinks.test.ts (review
  // H2): production live rows carry `live:<sub id>`, a shape a hand
  // fixture here would only guess at.
});
