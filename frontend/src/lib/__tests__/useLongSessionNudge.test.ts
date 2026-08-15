/**
 * useLongSessionNudge counts TURNS, not rows
 * (model-moves-transcript-01PMCH01 WP04).
 *
 * The composable used to derive turns as `messageCount / 2`, which was a
 * stand-in that only held while every turn was exactly one user row plus
 * one assistant row. Moves broke it: a five-iteration tool-using turn
 * persists ~13 rows, so the halving counted one turn as six and the
 * "this session is getting long" banner fired roughly 3x early.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { defineComponent, h, ref, nextTick } from 'vue';
import { mount } from '@vue/test-utils';
import { useLongSessionNudge } from '@/lib/useLongSessionNudge';
import { provideFakeClient } from '@/lib/harnessClientContext';

function mountNudge(turnCount: ReturnType<typeof ref<number>>, promptTokens = ref(0)) {
  let nudge: ReturnType<typeof useLongSessionNudge> | null = null;
  const Comp = defineComponent({
    setup() {
      nudge = useLongSessionNudge({
        turnCount: turnCount as ReturnType<typeof ref<number>> & { value: number },
        promptTokens,
      });
      return () => h('div');
    },
  });
  const w = mount(Comp, {
    global: {
      plugins: [
        {
          install: (app) =>
            provideFakeClient(app, {
              // No thresholds configured → the 30-turn / 50k-token defaults.
              settings: { get: async () => ({}) } as never,
            }),
        },
      ],
    },
  });
  return {
    w,
    get nudge() {
      if (!nudge) throw new Error('no nudge');
      return nudge;
    },
  };
}

describe('useLongSessionNudge', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('does not fire below the turn threshold', async () => {
    const turns = ref(29);
    const { w, nudge } = mountNudge(turns);
    await nextTick();
    expect(nudge.nudgeVisible.value).toBe(false);
    w.unmount();
  });

  it('fires at exactly the configured number of turns', async () => {
    const turns = ref(30);
    const { w, nudge } = mountNudge(turns);
    await nextTick();
    expect(nudge.nudgeTurns.value).toBe(30);
    expect(nudge.nudgeVisible.value).toBe(true);
    w.unmount();
  });

  it('does not fire early on a move-heavy session', async () => {
    // Ten human turns, each answered with ~13 rows: 130 rows in the
    // transcript. The old `rows / 2` arithmetic saw 65 "turns" and
    // nagged; the turn count is ten and does not.
    const turns = ref(10);
    const { w, nudge } = mountNudge(turns);
    await nextTick();
    expect(nudge.nudgeVisible.value).toBe(false);
    w.unmount();
  });

  it('still fires on a token-heavy short session', async () => {
    const { w, nudge } = mountNudge(ref(3), ref(50_000));
    await nextTick();
    expect(nudge.nudgeVisible.value).toBe(true);
    w.unmount();
  });

  it('stays hidden once dismissed', async () => {
    const { w, nudge } = mountNudge(ref(40));
    await nextTick();
    expect(nudge.nudgeVisible.value).toBe(true);
    nudge.dismiss();
    await nextTick();
    expect(nudge.nudgeVisible.value).toBe(false);
    w.unmount();
  });
});
