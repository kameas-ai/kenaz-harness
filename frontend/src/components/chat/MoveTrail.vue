<script setup lang="ts">
/**
 * MoveTrail — the intermediate moves of one human turn, drawn as ONE
 * trajectory (model-moves-transcript-01PMCH01 WP04, FR-003).
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
 */

import MarkdownBlock from './MarkdownBlock.vue';
import ToolChip from './ToolChip.vue';
import type { TrailStep } from '@/lib/transcript';

defineProps<{
  steps: readonly TrailStep[];
}>();
</script>

<template>
  <div
    class="move-trail relative pl-4 space-y-1.5"
    data-testid="move-trail"
    :data-step-count="steps.length"
  >
    <!-- The rail. Hairline, accent-tinted: the same "this belongs
         together" device the app uses for archived rows. -->
    <span
      class="absolute left-0 top-1 bottom-1 w-px bg-accent-hairline"
      aria-hidden="true"
    ></span>

    <template v-for="step in steps" :key="step.key">
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

      <div v-else class="max-w-[74ch] pr-2">
        <ToolChip :chip="step.chip" />
      </div>
    </template>
  </div>
</template>
