/**
 * Classic-session render golden (model-moves-transcript-01PMCH01 WP04).
 *
 * WP04 replaced MessageList's one-MessageBubble-per-row loop with a
 * projection that folds a turn's moves into a trail. The rule the
 * mission binds itself to is that a session written BEFORE the mission —
 * every row of which has no `kind` — must render exactly as it did
 * before, down to the markup.
 *
 * The snapshot in `__snapshots__/` was captured by running THIS FILE
 * against the pre-WP04 MessageList (base commit) and is committed
 * unchanged. It is therefore a real before/after golden, not a
 * self-portrait: if the projection ever perturbs the classic path, the
 * bytes move and this fails.
 *
 * The fixture deliberately covers every classic row shape the surface
 * knows — user, assistant, system, a `role: 'tool'` row with no move
 * metadata, a summary row, an archived row and a partial-output row —
 * because "renders the same" is only meaningful if the fixture exercises
 * the branches.
 */

import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import MessageList from '@/components/chat/MessageList.vue';
import type { Message } from '@/lib/types';

function msg(overrides: Partial<Message>): Message {
  return {
    id: 'm-1',
    sessionId: 's-1',
    role: 'user',
    content: 'hello',
    createdAt: '2026-04-25T00:00:00Z',
    ...overrides,
  };
}

/**
 * A pre-moves session. Not one row carries `kind` / `moveIndex` /
 * `turnSpanId` — which is exactly what `omitempty` produces for every
 * message written before this mission.
 */
const CLASSIC_SESSION: Message[] = [
  msg({ id: 'c-1', role: 'user', content: 'What does the config do?' }),
  msg({
    id: 'c-2',
    role: 'assistant',
    content: 'It selects the provider.',
    promptTokens: 120,
    completionTokens: 40,
    costUsd: 0.002,
    messageCostSource: 'provider',
  }),
  msg({
    id: 'c-3',
    role: 'tool',
    content: 'read_file → 24 lines',
    toolCalls: [{ id: 'tc-legacy', name: 'read_file', argsSummary: 'path:string' }],
  }),
  msg({ id: 'c-4', role: 'system', content: 'Model switched to sonnet.' }),
  msg({ id: 'c-5', role: 'user', content: 'And the fallback?' }),
  msg({
    id: 'c-6',
    role: 'assistant',
    content: 'Falls back to the secondary profile.',
    compactedAt: '2026-04-25T01:00:00Z',
  }),
  msg({
    id: 'c-7',
    role: 'assistant',
    content: 'An older turn.',
    archivedAt: '2026-04-25T01:00:00Z',
    compactedIntoId: 'c-6',
  }),
  msg({
    id: 'c-8',
    role: 'assistant',
    content: 'Interrupted repl',
    streamingFailedAt: '2026-04-25T02:00:00Z',
    streamingFailureKind: 'transient',
    streamingRecoverable: true,
  }),
];

describe('MessageList — classic (pre-moves) session', () => {
  it('renders byte-identically to the pre-WP04 surface', () => {
    const w = mount(MessageList, {
      props: {
        messages: CLASSIC_SESSION,
        rememberable: true,
        saveable: true,
        projectId: 'p-1',
        showTokenMeter: true,
        archiveDays: 90,
      },
    });
    expect(w.html()).toMatchSnapshot();
  });

  it('folds nothing: one bubble per row, no trail, no chips', () => {
    const w = mount(MessageList, { props: { messages: CLASSIC_SESSION } });
    expect(w.findAll('article')).toHaveLength(CLASSIC_SESSION.length);
    expect(w.find('[data-testid="move-trail"]').exists()).toBe(false);
    expect(w.find('[data-testid="tool-chip"]').exists()).toBe(false);
    // The legacy tool row keeps its own bubble — a row without move
    // metadata is not a chip and is not indexed.
    expect(w.text()).toContain('read_file → 24 lines');
  });
});
