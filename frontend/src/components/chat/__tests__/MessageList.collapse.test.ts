/**
 * Collapse UX (model-moves-transcript-01PMCH01 WP05, FR-004).
 *
 * "Many-move turns render collapsed by default to the final answer + a
 * 'N moves' expander; expanding shows the full trajectory; expansion is
 * lazy."
 *
 * The load-bearing word is LAZY. A collapsed trail that is merely hidden
 * with CSS still constructs a MarkdownBlock per move and a <pre> per
 * tool output, which is the entire cost of a 100-move session. These
 * tests assert on element EXISTENCE, not on visibility, so a `v-show`
 * regression fails them.
 *
 * MUTATION EVIDENCE — each was run against a deliberately broken tree:
 *   - `v-if="expanded"` → `v-show="expanded"` in MoveTrail:
 *     "collapses a many-move turn by default" fails (steps exist).
 *   - COLLAPSE_THRESHOLD → Infinity (never collapse):
 *     "collapses a many-move turn by default" and
 *     "a 100-move session builds almost no move DOM" both fail.
 *   - drop the EXPANDED_WINDOW slice (render every step on expand):
 *     "expanding a 30-move turn does not dump 30 moves" fails.
 *   - drop the `live` prop wiring in MessageList:
 *     "never collapses the turn that is still streaming" fails.
 *
 * ADVERSARIAL-REVIEW ADDITIONS (2026-08-14). Each of these was a
 * mutation that SURVIVED the tests above:
 *   - `errorCount` over `visibleSteps` instead of `props.steps`:
 *     nothing failed, because every fixture above fits inside
 *     EXPANDED_WINDOW. "surfaces a failure that the expand window hides"
 *     kills it.
 *   - revert `:aria-label="digestLabel"` to the bare "Show the model's
 *     trajectory": nothing failed, because no test read the accessible
 *     name. "speaks the digest to a screen reader" kills it.
 *   - drop the `focus-id` prop chain: nothing failed, because
 *     `MoveTrail` also reads `window.location.hash` on mount — which
 *     never fires for the hash-only navigation `router.push` performs.
 *     "opens a folded trail for a deep-link that arrives after mount"
 *     kills it.
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

/**
 * One human turn with `iterations` model fires, each calling one tool.
 * Trail steps = (iterations - 1) move steps + iterations chips; the last
 * fire is the answer and leaves the trail.
 */
function longTurn(span: string, iterations: number): Message[] {
  const out: Message[] = [
    row({ id: span, role: 'user', content: `question for ${span}` }),
  ];
  let idx = 0;
  for (let i = 0; i < iterations; i++) {
    out.push(
      row({
        id: `${span}-m${i}`,
        kind: 'assistant_move',
        moveIndex: idx++,
        turnSpanId: span,
        content: `Step ${i}: trying something.`,
      }),
    );
    out.push(
      row({
        id: `${span}-tc${i}`,
        role: 'tool',
        kind: 'tool_call',
        moveIndex: idx++,
        turnSpanId: span,
        content: 'path:string',
        toolCalls: [
          { id: `${span}-call-${i}`, name: i % 2 ? 'bash' : 'read_file', argsSummary: 'path:string' },
        ],
      }),
    );
    out.push(
      row({
        id: `${span}-tr${i}`,
        role: 'tool',
        kind: 'tool_result',
        moveIndex: idx++,
        turnSpanId: span,
        content: `output of step ${i}`,
        toolCalls: [
          {
            id: `${span}-call-${i}`,
            name: i % 2 ? 'bash' : 'read_file',
            argsSummary: '',
            isError: i === 1,
          },
        ],
      }),
    );
  }
  out.push(
    row({
      id: `${span}-final`,
      kind: 'final',
      moveIndex: idx,
      turnSpanId: span,
      content: `The answer for ${span}.`,
    }),
  );
  return out;
}

describe('MessageList — collapse (FR-004)', () => {
  it('collapses a many-move turn by default, showing the answer and a "N moves" expander', () => {
    const w = mount(MessageList, { props: { messages: longTurn('u-1', 5) } });

    // The answer is a full bubble and is on screen.
    expect(w.text()).toContain('The answer for u-1.');

    // The trajectory is not.
    expect(w.find('[data-testid="move-trail"]').attributes('data-expanded')).toBe(
      'false',
    );
    expect(w.findAll('[data-testid="move-step"]')).toHaveLength(0);
    expect(w.findAll('[data-testid="tool-chip"]')).toHaveLength(0);
    expect(w.text()).not.toContain('Step 0: trying something.');

    // In its place, the expander.
    const toggle = w.find('[data-testid="move-trail-toggle"]');
    expect(toggle.exists()).toBe(true);
    // 5 intermediate model fires + 5 tool chips. The turn's answer is a
    // sixth fire and lives outside the trail, as a full bubble.
    expect(toggle.find('[data-testid="move-trail-count"]').text()).toBe('10 moves');
    expect(toggle.attributes('aria-expanded')).toBe('false');
  });

  it('names the tools it folded and how many failed', () => {
    const w = mount(MessageList, { props: { messages: longTurn('u-1', 5) } });
    const toggle = w.find('[data-testid="move-trail-toggle"]');
    expect(toggle.find('[data-testid="move-trail-tools"]').text()).toBe(
      'read_file, bash',
    );
    // The fixture fails exactly one tool. A fold that hides failures is a
    // prettier transcript and a worse one.
    expect(toggle.find('[data-testid="move-trail-errors"]').text()).toBe('1 failed');
  });

  it('expanding reveals the full trajectory', async () => {
    const w = mount(MessageList, { props: { messages: longTurn('u-1', 5) } });
    await w.find('[data-testid="move-trail-toggle"]').trigger('click');

    expect(w.findAll('[data-testid="move-step"]')).toHaveLength(5);
    expect(w.findAll('[data-testid="tool-chip"]')).toHaveLength(5);
    expect(w.text()).toContain('Step 0: trying something.');
    expect(w.find('[data-testid="move-trail"]').attributes('data-expanded')).toBe(
      'true',
    );
  });

  it('collapsing again destroys the trajectory DOM rather than hiding it', async () => {
    const w = mount(MessageList, { props: { messages: longTurn('u-1', 5) } });
    const toggle = () => w.find('[data-testid="move-trail-toggle"]').trigger('click');
    await toggle();
    expect(w.findAll('[data-testid="move-step"]').length).toBeGreaterThan(0);
    await toggle();
    expect(w.findAll('[data-testid="move-step"]')).toHaveLength(0);
  });

  it('leaves a short trail expanded — collapsing it would cost a click and save nothing', () => {
    // 2 iterations → 2 move steps + 2 chips = 4 steps, below the threshold.
    const w = mount(MessageList, { props: { messages: longTurn('u-1', 2) } });
    expect(w.find('[data-testid="move-trail-toggle"]').exists()).toBe(false);
    expect(w.findAll('[data-testid="tool-chip"]')).toHaveLength(2);
  });

  it('never collapses the turn that is still streaming', () => {
    const rows = longTurn('u-1', 5);
    const persisted = rows.filter((m) => !m.kind);
    const inFlight = rows.filter((m) => m.kind && m.kind !== 'final');
    const w = mount(MessageList, {
      props: { messages: persisted, streamingMessages: inFlight },
    });
    // Same span, still open: the user is watching the model work and the
    // trail must not fold out from under them.
    expect(w.find('[data-testid="move-trail"]').attributes('data-expanded')).toBe(
      'true',
    );
    expect(w.findAll('[data-testid="move-step"]').length).toBeGreaterThan(0);
  });
});

describe('MessageList — the 30-move wall (WP04 review follow-up)', () => {
  it('expanding a 30-move turn does not dump 30 moves at once', async () => {
    // 15 iterations → 15 move steps + 15 chips = 30 steps.
    const w = mount(MessageList, { props: { messages: longTurn('u-1', 15) } });
    expect(w.find('[data-testid="move-trail-toggle"]').text()).toContain('30 moves');

    await w.find('[data-testid="move-trail-toggle"]').trigger('click');

    // The window is the tail: the steps nearest the answer explain it.
    const drawn =
      w.findAll('[data-testid="move-step"]').length +
      w.findAll('[data-testid="tool-chip"]').length;
    expect(drawn).toBe(12);
    expect(w.text()).toContain('Show 18 earlier steps');
    // The most recent step is in the window; the first is not.
    expect(w.text()).toContain('Step 14: trying something.');
    expect(w.text()).not.toContain('Step 0: trying something.');

    await w.find('[data-testid="move-trail-show-earlier"]').trigger('click');
    const all =
      w.findAll('[data-testid="move-step"]').length +
      w.findAll('[data-testid="tool-chip"]').length;
    expect(all).toBe(30);
    expect(w.text()).toContain('Step 0: trying something.');
  });

  it('surfaces a failure that the expand window hides', async () => {
    // 15 iterations → 30 steps; the fixture fails iteration 1, whose
    // chip is step index 3. The window is the last 12 (indices 18–29),
    // so the failing chip is NOT drawn even after expanding. The digest
    // is the only place that failure can appear, in either state.
    const w = mount(MessageList, { props: { messages: longTurn('u-1', 15) } });
    expect(w.find('[data-testid="move-trail-errors"]').text()).toBe('1 failed');

    await w.find('[data-testid="move-trail-toggle"]').trigger('click');
    expect(w.text()).toContain('Show 18 earlier steps');
    // The erroring chip really is out of frame…
    const chipStates = w
      .findAll('[data-testid="tool-chip"]')
      .map((c) => c.attributes('data-tool-status'));
    expect(chipStates).not.toContain('error');
    // …and the digest still says so.
    expect(w.find('[data-testid="move-trail-errors"]').text()).toBe('1 failed');
  });

  it('speaks the digest to a screen reader, not just to the eye', () => {
    // `aria-label` REPLACES the accessible name. Labelling the toggle
    // "Show the model's trajectory" made the move count, the tool names
    // and — the one that matters — the failure count unreachable for
    // assistive tech, which is the fold hiding failures again, for a
    // narrower audience.
    const w = mount(MessageList, { props: { messages: longTurn('u-1', 5) } });
    const toggle = w.find('[data-testid="move-trail-toggle"]');
    expect(toggle.element.tagName).toBe('BUTTON');
    const label = toggle.attributes('aria-label') ?? '';
    expect(label).toContain('10 moves');
    expect(label).toContain('read_file');
    expect(label).toContain('1 failed');
    expect(toggle.attributes('aria-expanded')).toBe('false');
  });

  it('opens a folded trail for a deep-link that arrives after mount', async () => {
    // `SearchModal` navigates with `router.push('/sessions/<id>#<msg>')`.
    // In history mode that is a `pushState`, which fires neither
    // `hashchange` nor `popstate` — so a hit in the session already on
    // screen reaches a trail that is ALREADY MOUNTED. Without the prop
    // the trail stays folded and the deep-link scrolls to a zero-height
    // anchor inside the fold.
    const rows = longTurn('u-1', 5);
    const w = mount(MessageList, { props: { messages: rows } });
    expect(w.find('[data-testid="move-trail"]').attributes('data-expanded')).toBe(
      'false',
    );

    await w.setProps({ focusMessageId: 'u-1-m2' });
    expect(w.find('[data-testid="move-trail"]').attributes('data-expanded')).toBe(
      'true',
    );
    expect(w.text()).toContain('Step 2: trying something.');
  });

  it('keeps a scroll-to anchor for every folded row', () => {
    // A folded row is still a searchable, still-indexed row. Without the
    // anchor the search deep-link poller spins for 5s and gives up.
    const rows = longTurn('u-1', 5);
    const w = mount(MessageList, { props: { messages: rows } });
    expect(w.find('[data-testid="move-trail"]').attributes('data-expanded')).toBe(
      'false',
    );
    for (const m of rows) {
      if (!m.kind || m.kind === 'final') continue;
      expect(w.find(`[data-message-id="${m.id}"]`).exists()).toBe(true);
    }
  });
});

describe('MessageList — 100-move session (plan.md risk)', () => {
  /**
   * The plan's "move volume" risk: hours-long turns × per-move
   * persistence. Collapse-by-default is the pressure valve, and the
   * thing that makes it work is that the folded moves are never built.
   *
   * The DOM-count assertion is the deterministic half — it fails the
   * moment collapse stops being lazy. The timing assertion is the
   * recorded budget; it is deliberately loose (a CI box under load is
   * not a benchmark rig) and the measurement is in the WP report.
   */
  it('a 100-move session builds almost no move DOM and mounts inside budget', () => {
    const messages: Message[] = [];
    // 10 turns x 10 iterations = 100 intermediate model fires (plus 10
    // answers), 320 persisted rows.
    for (let t = 1; t <= 10; t++) messages.push(...longTurn(`u-${t}`, 10));
    expect(messages.filter((m) => m.kind === 'assistant_move')).toHaveLength(100);
    expect(messages.filter((m) => m.kind === 'final')).toHaveLength(10);
    expect(messages).toHaveLength(320);

    const started = performance.now();
    const w = mount(MessageList, { props: { messages } });
    const elapsed = performance.now() - started;

    // 10 answers + 10 user questions as full bubbles; all 300
    // trajectory rows folded.
    expect(w.findAll('article')).toHaveLength(20);
    expect(w.findAll('[data-testid="move-trail"]')).toHaveLength(10);
    expect(w.findAll('[data-testid="move-step"]')).toHaveLength(0);
    expect(w.findAll('[data-testid="tool-chip"]')).toHaveLength(0);
    expect(elapsed).toBeLessThan(2000);
  });
});
