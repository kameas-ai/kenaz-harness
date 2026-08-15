<script setup lang="ts">
/**
 * MoveTrail — the intermediate moves of one human turn, drawn as ONE
 * trajectory (model-moves-transcript-01PMCH01 WP04 FR-003, WP05 FR-004).
 *
 * The problem this solves is not correctness, it is legibility. WP02
 * turned a 5-iteration turn from 2 persisted rows into ~13, and a chat
 * pane that renders 13 equal-weight assistant bubbles per question is
 * unreadable — the answer stops being findable. So the turn reads as:
 *
 *   ┌ ①  Let me look at the two config files first.
 *   │ ● read_file  path:string                            ok
 *   │ ● bash       cmd:string                          error
 *   │ ②  The native tools are blocked; falling back to bash.
 *   └
 *   ┌──────────────────────────────────────────────────┐
 *   │ ASSISTANT                                        │  ← the answer,
 *   │ …full-weight bubble, unchanged from today…       │    full weight
 *   └──────────────────────────────────────────────────┘
 *
 * The rail + the muted, tighter type is the whole trick: intermediate
 * moves are visibly subordinate to the answer that follows them, so the
 * eye lands on the answer first and the trajectory is there to read when
 * you want it. The ordinals are what make a long trail scannable.
 *
 * Deliberately NOT a MessageBubble: an intermediate step has no
 * "branch from this turn", no pin, no token meter. Those affordances
 * belong to a turn's answer, and hanging them off every model iteration
 * is exactly the unlabelled-bubble noise the ledger recorded.
 *
 * ── WP05: THE RAIL ORGANISES, IT DOES NOT SHORTEN ────────────────────
 *
 * WP04's review made the point that the rail is not enough on its own:
 * 30 intermediate moves under a hairline is still 30 moves of scroll
 * between one answer and the next, and the turn ceiling is 500. FR-004
 * asks for collapse-by-default, and this component answers it in two
 * stages, because one stage does not survive the 30-move case:
 *
 *   1. COLLAPSE. A trail of COLLAPSE_THRESHOLD or more steps renders as
 *      one header line — a digest naming how many moves ran, which tools
 *      they called, and how many failed — and nothing else. The whole
 *      trajectory is `v-if`'d out, not hidden with CSS: on a 100-move
 *      session the MarkdownBlock per move and the <pre> per tool output
 *      are never constructed, which is where the load time goes.
 *      Expansion is therefore lazy in the only sense that matters.
 *
 *   2. WINDOW. Expanding a 30-step trail must not simply move the wall
 *      one click away, so an expanded trail renders its most recent
 *      EXPANDED_WINDOW steps and offers "Show N earlier steps" above
 *      them. The tail is the right window: a trajectory is chronological
 *      and abuts the answer, so the steps nearest the answer are the
 *      ones that explain it, and the earlier ones are context you ask
 *      for. One more click gets the whole thing.
 *
 * A LIVE TURN IS NEVER COLLAPSED. Watching the model work is the point
 * of the mission; collapsing the trail out from under a running turn
 * would hide exactly what FR-003 just made visible. `live` is true for
 * the span currently streaming, so the trail stays open for the whole
 * turn and tidies itself away when the turn lands. It is a span-level
 * flag rather than a "does any step look busy" derivation on purpose:
 * between a tool result landing and the next segment opening, no step is
 * busy, and a derived flag would collapse and re-expand mid-turn.
 *
 * An explicit user toggle wins over both rules for as long as the trail
 * is mounted — `manual` is the override, `null` means "follow the rule".
 *
 * Collapse is a DISCLOSURE state, not a content state: the live view and
 * a reload still project the same steps from the same rows (see
 * `lib/transcript.ts`). What differs is how many of them are on screen
 * before you ask.
 */

import { computed, onMounted, onUnmounted, ref } from 'vue';
import { ChevronDown, ChevronRight } from '@/shell/icons';
import MarkdownBlock from './MarkdownBlock.vue';
import ToolChip from './ToolChip.vue';
import type { TrailStep } from '@/lib/transcript';

const props = defineProps<{
  steps: readonly TrailStep[];
  /**
   * True while the human turn these steps belong to is still streaming.
   * The parent knows it because it owns the in-flight buffer.
   */
  live?: boolean;
  /**
   * The message id a search deep-link is pointing at, passed down from
   * the view that owns the router.
   *
   * REQUIRED, not belt-and-braces. `SearchModal` navigates with
   * `router.push('/sessions/<id>#<messageId>')`, which in history mode
   * is a `history.pushState` — and pushState fires NEITHER `hashchange`
   * NOR `popstate`. So the window-hash listener below only catches the
   * case where this component MOUNTS after the navigation (a hit in a
   * different session). A hit in the session you are already reading
   * changes the hash without remounting anything, and the trail holding
   * the target would stay folded: the poller finds the bare anchor,
   * scrolls to a zero-height span inside a collapsed fold, and the link
   * dead-ends. That is the WP04-review regression, one navigation shape
   * over.
   */
  focusId?: string | null;
}>();

/**
 * Trails shorter than this stay open: below it the trajectory is
 * physically smaller than the answer bubble it precedes, so collapsing
 * costs a click and saves nothing.
 */
const COLLAPSE_THRESHOLD = 6;

/**
 * How many of the most recent steps an expanded long trail shows.
 *
 * Set well above COLLAPSE_THRESHOLD on purpose: a window that bites at
 * the same size as the fold turns "expand" into two clicks for a trail
 * that was never a wall. Twelve compact lines is a scroll, not a wall;
 * thirty is a wall.
 */
const EXPANDED_WINDOW = 12;

/** null = follow the collapse rule; true/false = the user decided. */
const manual = ref<boolean | null>(null);
const showEarlier = ref(false);

/**
 * The row a search deep-link is pointing at, if any.
 *
 * `SessionsView` routes a search hit to `/sessions/<id>#<messageId>` and
 * then polls the document for `[data-message-id]`. A collapsed trail
 * still renders an anchor per step (see the template) so the poll always
 * resolves — but scrolling the user to a fold and leaving them to guess
 * which of twelve moves matched is a dead end dressed as a link. So a
 * trail that contains the target opens itself.
 *
 * The `focusId` prop is the primary source — the view that owns the
 * router is the only thing that observes a pushState hash change. The
 * `window.location.hash` read below is the fallback for the
 * mount-after-navigation case and for the leaf-component tests that
 * mount this without a router.
 */
const hashTarget = ref('');
function readHash() {
  const h = typeof window === 'undefined' ? '' : window.location.hash;
  hashTarget.value = h.startsWith('#') ? decodeURIComponent(h.slice(1)) : '';
}
onMounted(() => {
  readHash();
  window.addEventListener('hashchange', readHash);
});
onUnmounted(() => {
  if (typeof window !== 'undefined') {
    window.removeEventListener('hashchange', readHash);
  }
});

const holdsHashTarget = computed(() => {
  const target = props.focusId || hashTarget.value;
  if (!target) return false;
  return props.steps.some(
    (s) =>
      s.key === target ||
      (s.type === 'tool' && s.resultKey === target) ||
      (s.type === 'move' && s.message.id === target),
  );
});

const autoExpanded = computed(
  () =>
    props.live === true ||
    holdsHashTarget.value ||
    props.steps.length < COLLAPSE_THRESHOLD,
);
const expanded = computed(() => manual.value ?? autoExpanded.value);

const visibleSteps = computed<readonly TrailStep[]>(() => {
  if (
    showEarlier.value ||
    holdsHashTarget.value ||
    props.steps.length <= EXPANDED_WINDOW
  ) {
    return props.steps;
  }
  return props.steps.slice(props.steps.length - EXPANDED_WINDOW);
});

const earlierCount = computed(
  () => props.steps.length - visibleSteps.value.length,
);

/**
 * Steps that are currently not drawn — folded away or windowed out. They
 * still get a bare `[data-message-id]` anchor so the app-wide scroll-to
 * target exists for every row the store holds.
 */
const anchorOnlySteps = computed<readonly TrailStep[]>(() =>
  expanded.value ? props.steps.slice(0, earlierCount.value) : props.steps,
);

/**
 * Distinct tool names in call order, so the digest says what the turn
 * actually did rather than only how much of it there was.
 */
const toolNames = computed<string[]>(() => {
  const seen: string[] = [];
  for (const s of props.steps) {
    if (s.type !== 'tool' || !s.chip.name) continue;
    if (!seen.includes(s.chip.name)) seen.push(s.chip.name);
  }
  return seen;
});

const toolDigest = computed(() => {
  const names = toolNames.value;
  if (names.length === 0) return '';
  if (names.length <= 3) return names.join(', ');
  return `${names.slice(0, 3).join(', ')} +${names.length - 3}`;
});

/**
 * Failures must survive the fold. A collapsed trail that hides the fact
 * that two tools errored is a prettier transcript and a worse one — the
 * error is usually why the answer reads the way it does.
 */
const errorCount = computed(
  () => props.steps.filter((s) => s.type === 'tool' && s.chip.status === 'error')
    .length,
);

/**
 * The digest, spoken.
 *
 * `aria-label` REPLACES a button's accessible name — it does not add to
 * it. The first version of this component labelled the toggle "Show the
 * model's trajectory", which meant a screen reader heard exactly that
 * and never heard the digest: not the move count, not which tools ran,
 * and — the one that matters — not that two of them failed. The whole
 * argument for the digest is that "failures must survive the fold"; a
 * label that hides them from assistive tech makes the digest survive
 * the fold for sighted users only.
 *
 * So the accessible name IS the digest, with the action in front of it.
 * Commas because a screen reader pauses on them; the visible text can
 * stay terse.
 */
const digestLabel = computed(() => {
  const parts = [`${props.steps.length} moves`];
  if (toolDigest.value) parts.push(toolDigest.value);
  if (errorCount.value > 0) {
    parts.push(errorCount.value === 1 ? '1 failed' : `${errorCount.value} failed`);
  }
  const action = expanded.value
    ? "Hide the model's trajectory"
    : "Show the model's trajectory";
  return `${action}: ${parts.join(', ')}`;
});

function toggle() {
  manual.value = !expanded.value;
}
</script>

<template>
  <div
    class="move-trail relative pl-4 space-y-1.5"
    data-testid="move-trail"
    :data-step-count="steps.length"
    :data-expanded="expanded ? 'true' : 'false'"
  >
    <!-- The rail. Hairline, accent-tinted: the same "this belongs
         together" device the app uses for archived rows. -->
    <span
      class="absolute left-0 top-1 bottom-1 w-px bg-accent-hairline"
      aria-hidden="true"
    ></span>

    <button
      v-if="steps.length >= COLLAPSE_THRESHOLD"
      type="button"
      class="flex w-full max-w-[74ch] items-center gap-1.5 rounded-sm px-1 py-0.5 font-ui text-[11px] text-ink-subtle text-left hover:bg-surface-2 hover:text-ink-muted transition-fast"
      :aria-expanded="expanded"
      :aria-label="digestLabel"
      data-testid="move-trail-toggle"
      @click="toggle"
    >
      <component
        :is="expanded ? ChevronDown : ChevronRight"
        :size="12"
        class="shrink-0"
        aria-hidden="true"
      />
      <span class="shrink-0 tabular-nums" data-testid="move-trail-count">
        {{ steps.length }} moves
      </span>
      <span
        v-if="toolDigest"
        class="min-w-0 truncate font-mono text-ink-subtle"
        data-testid="move-trail-tools"
      >
        {{ toolDigest }}
      </span>
      <span
        v-if="errorCount > 0"
        class="ml-auto shrink-0 uppercase tracking-[0.14em] text-[10px] text-signal-danger"
        data-testid="move-trail-errors"
      >
        {{ errorCount }} failed
      </span>
    </button>

    <!-- Scroll-to anchors survive the fold. `[data-message-id]` is the
         app-wide deep-link target and a folded row is still a searchable,
         still-indexed row; without these the search poller spins for 5s
         and gives up. An empty span costs nothing — the expensive part
         is the MarkdownBlock and the output <pre>, and those stay
         unbuilt. -->
    <template v-if="anchorOnlySteps.length > 0">
      <template v-for="step in anchorOnlySteps" :key="`anchor-${step.key}`">
        <span
          class="block h-0 w-0"
          :data-message-id="step.key"
          aria-hidden="true"
        ></span>
        <span
          v-if="step.type === 'tool' && step.resultKey"
          class="block h-0 w-0"
          :data-message-id="step.resultKey"
          aria-hidden="true"
        ></span>
      </template>
    </template>

    <!-- `v-if`, never `v-show`: a collapsed trail must not construct a
         MarkdownBlock per move. That is what makes a 100-move session
         mount in the same order of time as a classic one. -->
    <template v-if="expanded">
      <button
        v-if="earlierCount > 0"
        type="button"
        class="block w-full max-w-[74ch] rounded-sm border border-dashed border-border-muted px-2 py-0.5 font-ui text-[11px] text-ink-subtle text-left hover:bg-surface-2 hover:text-ink-muted transition-fast"
        data-testid="move-trail-show-earlier"
        @click="showEarlier = true"
      >
        Show {{ earlierCount }} earlier steps
      </button>

      <template v-for="step in visibleSteps" :key="step.key">
        <div
          v-if="step.type === 'move'"
          class="flex gap-2 pr-2"
          :data-message-id="step.message.id"
          data-testid="move-step"
        >
          <span
            class="mt-[2px] shrink-0 font-mono text-[10px] tabular-nums text-ink-subtle"
            data-testid="move-step-ordinal"
            aria-hidden="true"
          >
            {{ step.ordinal }}
          </span>
          <div class="min-w-0 max-w-[74ch] font-ui text-[13px] leading-relaxed text-ink-muted">
            <MarkdownBlock
              :source="step.message.content"
              :streaming="step.message.streaming === true"
              :message-id="step.message.id"
            />
          </div>
        </div>

        <!-- `data-message-id` is the app-wide scroll-to target: the
             search deep-link poller in SessionsView and MessageList's
             jump-to-summary both querySelector on it. A tool row is a
             searchable row like any other, so its chip has to answer to
             its row id or the link spins for 5s and gives up. -->
        <div v-else class="max-w-[74ch] pr-2" :data-message-id="step.key">
          <span
            v-if="step.resultKey"
            class="block h-0 w-0"
            :data-message-id="step.resultKey"
            aria-hidden="true"
          ></span>
          <ToolChip :chip="step.chip" />
        </div>
      </template>
    </template>
  </div>
</template>
