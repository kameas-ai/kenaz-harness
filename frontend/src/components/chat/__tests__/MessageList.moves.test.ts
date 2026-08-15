/**
 * The rendered move surface (model-moves-transcript-01PMCH01 WP04).
 *
 * The projection is unit-tested in lib/__tests__/transcript.test.ts;
 * these pin what actually reaches the DOM — that a turn's segments are
 * separate elements rather than one run-on paragraph, that chips carry
 * their status, and that neither raw arguments nor raw tool output leak
 * into the conversation flow.
 */

import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import MessageList from '@/components/chat/MessageList.vue';
import type { Message } from '@/lib/types';

function row(overrides: Partial<Message>): Message {
  return {
    id: 'm',
    sessionId: 's-1',
    role: 'assistant',
    content: '',
    createdAt: '2026-08-14T00:00:00Z',
    ...overrides,
  };
}

const SPAN = 'u-1';

/** Three model segments around two tool calls, then the answer. */
function movesTurn(): Message[] {
  return [
    row({ id: 'u-1', role: 'user', content: 'Where is the config read?' }),
    row({
      id: 'r-0',
      kind: 'assistant_move',
      moveIndex: 0,
      turnSpanId: SPAN,
      content: 'Let me explore it.',
    }),
    row({
      id: 'r-1',
      role: 'tool',
      kind: 'tool_call',
      moveIndex: 1,
      turnSpanId: SPAN,
      content: 'path:string',
      toolCalls: [{ id: 'call-a', name: 'read_file', argsSummary: 'path:string' }],
    }),
    row({
      id: 'r-2',
      role: 'tool',
      kind: 'tool_result',
      moveIndex: 2,
      turnSpanId: SPAN,
      content: 'alpha\nbeta\ngamma',
      toolCalls: [{ id: 'call-a', name: 'read_file', argsSummary: '' }],
    }),
    row({
      id: 'r-3',
      kind: 'assistant_move',
      moveIndex: 3,
      turnSpanId: SPAN,
      content: 'The native tools are blocked, but I can use bash.',
    }),
    row({
      id: 'r-4',
      role: 'tool',
      kind: 'tool_call',
      moveIndex: 4,
      turnSpanId: SPAN,
      content: 'cmd:string',
      toolCalls: [{ id: 'call-b', name: 'bash', argsSummary: 'cmd:string' }],
    }),
    row({
      id: 'r-5',
      role: 'tool',
      kind: 'tool_result',
      moveIndex: 5,
      turnSpanId: SPAN,
      content: 'permission denied',
      toolCalls: [{ id: 'call-b', name: 'bash', argsSummary: '', isError: true }],
    }),
    row({
      id: 'r-6',
      kind: 'final',
      moveIndex: 6,
      turnSpanId: SPAN,
      content: 'The config is read in core/config/load.go.',
    }),
  ];
}

describe('MessageList — move rendering', () => {
  it('renders each model segment as its own element', () => {
    const w = mount(MessageList, { props: { messages: movesTurn() } });
    const steps = w.findAll('[data-testid="move-step"]');
    expect(steps).toHaveLength(2);
    // Distinct elements, distinct text — the anti-run-on assertion. Before
    // WP04 these two segments arrived glued into one paragraph.
    expect(steps[0].text()).toContain('Let me explore it.');
    expect(steps[1].text()).toContain('The native tools are blocked');
    expect(steps[0].text()).not.toContain('The native tools are blocked');
  });

  it('renders the user turn and the answer as full bubbles, the moves not', () => {
    const w = mount(MessageList, { props: { messages: movesTurn() } });
    const articles = w.findAll('article');
    expect(articles).toHaveLength(2);
    expect(articles[0].text()).toContain('Where is the config read?');
    expect(articles[1].text()).toContain('core/config/load.go');
  });

  it('groups the intermediate steps under one trail', () => {
    const w = mount(MessageList, { props: { messages: movesTurn() } });
    const trails = w.findAll('[data-testid="move-trail"]');
    expect(trails).toHaveLength(1);
    // 2 moves + 2 chips.
    expect(trails[0].attributes('data-step-count')).toBe('4');
  });

  it('renders tool calls as chips carrying name + args summary', () => {
    const w = mount(MessageList, { props: { messages: movesTurn() } });
    const chips = w.findAll('[data-testid="tool-chip"]');
    expect(chips).toHaveLength(2);
    expect(chips[0].text()).toContain('read_file');
    expect(chips[0].find('[data-testid="tool-chip-args"]').text()).toBe('path:string');
  });
});

describe('MessageList — chip status in the DOM', () => {
  function chipStatuses(w: ReturnType<typeof mount>): string[] {
    return w
      .findAll('[data-testid="tool-chip"]')
      .map((c) => c.attributes('data-tool-status') ?? '');
  }

  it('running while the result has not landed', () => {
    const rows = movesTurn().filter((m) => m.kind !== 'tool_result' && m.kind !== 'final');
    const w = mount(MessageList, { props: { messages: rows } });
    expect(chipStatuses(w)).toEqual(['running', 'running']);
    expect(w.findAll('[data-testid="tool-chip-status"]')[0].text()).toBe('running');
  });

  it('running → ok and running → error once the results land', () => {
    const w = mount(MessageList, { props: { messages: movesTurn() } });
    expect(chipStatuses(w)).toEqual(['ok', 'error']);
    const labels = w
      .findAll('[data-testid="tool-chip-status"]')
      .map((s) => s.text());
    expect(labels).toEqual(['ok', 'error']);
  });
});

describe('MessageList — tool output stays out of the conversation flow', () => {
  it('does not inline tool output; reveals it only on demand', async () => {
    const w = mount(MessageList, { props: { messages: movesTurn() } });
    // The pre-WP04 bug: the raw result flattened into the transcript.
    expect(w.text()).not.toContain('alpha');
    expect(w.find('[data-testid="tool-chip-output"]').exists()).toBe(false);

    await w.findAll('[data-testid="tool-chip"]')[0].find('button').trigger('click');
    const out = w.find('[data-testid="tool-chip-output"]');
    expect(out.exists()).toBe(true);
    expect(out.text()).toContain('alpha');
    // Still boxed, not in the flow.
    expect(out.classes()).toContain('overflow-auto');
  });

  it('caps very large tool output', async () => {
    const huge = 'x'.repeat(9000);
    const rows = movesTurn().map((m) =>
      m.id === 'r-2' ? ({ ...m, content: huge } as Message) : m,
    );
    const w = mount(MessageList, { props: { messages: rows } });
    await w.findAll('[data-testid="tool-chip"]')[0].find('button').trigger('click');
    const text = w.find('[data-testid="tool-chip-output"]').text();
    expect(text).toContain('output truncated');
    expect(text.length).toBeLessThan(huge.length);
  });

  it('never renders raw tool arguments', () => {
    // A tool_call row's content is the DISPLAY summary. If any code path
    // ever swapped the raw argument JSON in, this catches it.
    const rows = movesTurn();
    const w = mount(MessageList, { props: { messages: rows } });
    const html = w.html();
    expect(html).toContain('path:string');
    expect(html).not.toContain('/etc/passwd');
    expect(html).not.toMatch(/"path"\s*:/);
    expect(html).not.toMatch(/\{"cmd"/);
  });
});

describe('MessageList — live and reloaded render the same trajectory', () => {
  /**
   * The live half of the same turn: last segment announced as
   * `assistant_move` (persistence calls it `final`), per-stream span id,
   * and no tool output on the stream. Reconciliation is on moveIndex.
   */
  function liveRows(): Message[] {
    return movesTurn().map((m) => {
      if (!m.kind) return m;
      return {
        ...m,
        id: `streaming-sub-x-${m.moveIndex}`,
        kind: m.kind === 'final' ? 'assistant_move' : m.kind,
        turnSpanId: 'live:sub-x',
        content: m.kind === 'tool_result' ? '' : m.content,
      } as Message;
    });
  }

  /** The visible shape of the thread, ignoring ids and chrome. */
  function shape(w: ReturnType<typeof mount>): unknown {
    return {
      bubbles: w.findAll('article').map((a) => a.text().replace(/\s+/g, ' ').trim()),
      steps: w.findAll('[data-testid="move-step"]').map((s) => s.text().trim()),
      chips: w.findAll('[data-testid="tool-chip"]').map((c) => [
        c.attributes('data-tool-status'),
        c.find('[data-testid="tool-chip-args"]').exists()
          ? c.find('[data-testid="tool-chip-args"]').text()
          : '',
      ]),
    };
  }

  it('a reload shows what the live view showed', () => {
    const live = mount(MessageList, {
      props: { messages: [movesTurn()[0]], streamingMessages: liveRows().slice(1) },
    });
    const reloaded = mount(MessageList, { props: { messages: movesTurn() } });
    expect(shape(live)).toEqual(shape(reloaded));
  });
});

describe('MessageList — adversarial review follow-ups (WP04)', () => {
  /**
   * The 4000-char cap slices UTF-16 code units. A cap that lands between
   * the halves of an astral character leaves a lone surrogate and the
   * browser paints U+FFFD. This repo has shipped the same mid-rune cut
   * twice in the Go layer; verified present here before the fix by
   * rendering exactly this fixture and matching the lone-surrogate
   * pattern below.
   */
  it('does not split a multi-byte character at the output cap', async () => {
    const raw = 'a'.repeat(3999) + '\u{1F600}' + 'tail';
    const rows = movesTurn().map((m) =>
      m.id === 'r-2' ? ({ ...m, content: raw } as Message) : m,
    );
    const w = mount(MessageList, { props: { messages: rows } });
    await w.findAll('[data-testid="tool-chip"]')[0].find('button').trigger('click');
    const text = w.find('[data-testid="tool-chip-output"]').text();
    const loneSurrogate =
      /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/;
    expect(text).not.toMatch(loneSurrogate);
    expect(text).toContain('output truncated');
  });

  /**
   * Both halves of a tool pair are searchable rows, and the app-wide
   * scroll-to target is `[data-message-id]`. The chip renders both, so a
   * search hit on either one resolves instead of spinning out.
   */
  it('exposes a scroll-to anchor for both halves of a folded tool pair', () => {
    const w = mount(MessageList, { props: { messages: movesTurn() } });
    for (const id of ['r-1', 'r-2', 'r-4', 'r-5']) {
      expect(w.find(`[data-message-id="${id}"]`).exists()).toBe(true);
    }
  });
});
