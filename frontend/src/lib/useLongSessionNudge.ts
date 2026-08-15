/**
 * useLongSessionNudge — composable that decides when to show the inline
 * long-session nudge banner (v0.5.6 memory-trust-signals).
 *
 * Threshold logic (OR conditions):
 *   - turn count >= nudgeTurns
 *   - promptTokens (cumulative) >= nudgeTokens
 *
 * A TURN is one human message, and the caller counts them directly.
 * This used to be `messageCount / 2`, which was a stand-in for the same
 * thing back when a turn was always exactly one user row plus one
 * assistant row. Moves (model-moves-transcript-01PMCH01) broke that
 * assumption: a five-iteration tool-using turn persists ~13 rows, so the
 * halving counted it as six or seven turns and the banner fired roughly
 * 3x early — nagging the user to start a new session a third of the way
 * into a healthy one. Counting the human turns cannot drift with how
 * many moves the model needs, and on a classic session it yields exactly
 * the number the old arithmetic did.
 *
 * Per-session dismiss: once the user clicks "Dismiss for this session"
 * the banner stays hidden for the lifetime of the component that mounts
 * this composable. No persistence — only component-local state.
 *
 * The threshold values are loaded from Settings_Get on first call.
 * Defaults: 30 turns / 50,000 tokens (matching Go-side constants).
 */

import { computed, ref, type Ref } from 'vue';
import { useHarnessClient } from './useHarnessAPI';

// Spec-locked defaults (mirrors Go-side constants).
const DEFAULT_NUDGE_TURNS = 30;
const DEFAULT_NUDGE_TOKENS = 50_000;

export interface UseLongSessionNudgeOptions {
  /**
   * Reactive ref of the number of HUMAN TURNS in the session — not the
   * row count. One turn is one user message, however many model moves
   * and tool entries it produced.
   */
  turnCount: Ref<number>;
  /** Reactive ref of cumulative prompt tokens for the session. */
  promptTokens: Ref<number>;
}

export interface UseLongSessionNudgeReturn {
  /** True when the nudge banner should be visible. */
  nudgeVisible: Ref<boolean>;
  /** Permanently hides the banner for the current component lifecycle. */
  dismiss: () => void;
  /** The effective turn threshold (loaded from settings or default). */
  nudgeTurns: Ref<number>;
  /** The effective token threshold (loaded from settings or default). */
  nudgeTokens: Ref<number>;
}

export function useLongSessionNudge(
  opts: UseLongSessionNudgeOptions,
): UseLongSessionNudgeReturn {
  const client = useHarnessClient();

  const nudgeTurns = ref(DEFAULT_NUDGE_TURNS);
  const nudgeTokens = ref(DEFAULT_NUDGE_TOKENS);

  // Per-session dismissed flag — no persistence, just component-local state.
  const dismissed = ref(false);
  // Whether the nudge has already been shown once (shown at most once per session).
  const shownOnce = ref(false);

  // Load threshold settings asynchronously. Failures are silently ignored
  // (defaults remain in effect) so transient RPC errors don't break chat.
  void (async () => {
    try {
      const s = await client.settings.get();
      if (s.longSessionNudgeTurns && s.longSessionNudgeTurns > 0) {
        nudgeTurns.value = s.longSessionNudgeTurns;
      }
      if (s.longSessionNudgeTokens && s.longSessionNudgeTokens > 0) {
        nudgeTokens.value = s.longSessionNudgeTokens;
      }
    } catch {
      // Keep defaults.
    }
  })();

  // Derived: whether the session has crossed either threshold.
  const thresholdCrossed = computed<boolean>(() => {
    if (opts.turnCount.value >= nudgeTurns.value) return true;
    if (opts.promptTokens.value >= nudgeTokens.value) return true;
    return false;
  });

  // nudgeVisible: threshold must be crossed AND not dismissed AND not already shown.
  // "shown once" is set to true when the banner first becomes visible.
  const nudgeVisible = computed<boolean>(() => {
    if (dismissed.value) return false;
    if (!thresholdCrossed.value) return false;
    // Allow showing if threshold just crossed (shownOnce is false).
    // Once shown, keep visible until dismissed (so the user sees it).
    if (!shownOnce.value) {
      // Side-effect inside computed is intentional: set the flag once.
      // This is safe because it only transitions false → true and is
      // idempotent.
      shownOnce.value = true;
    }
    return true;
  });

  function dismiss() {
    dismissed.value = true;
  }

  return { nudgeVisible, dismiss, nudgeTurns, nudgeTokens };
}
