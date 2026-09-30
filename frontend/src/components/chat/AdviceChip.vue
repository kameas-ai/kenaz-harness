<script setup lang="ts">
/**
 * AdviceChip — laya-advisors-01LAYA001 WP07's passive inline chip for
 * the advisor seam (core/advice). Renders a kind-specific one-sentence
 * recommendation with accept/dismiss actions (spec §3: "passive inline
 * chip — one sentence, accept/dismiss, never modal, never repeated for
 * materially identical features after a dismiss").
 *
 * Purely presentational — state management (which chip is active,
 * calling Advice_Respond, session scoping) lives in useAdviceChips.ts,
 * mirroring BranchSuggestionBanner.vue / LongSessionNudge.vue's own
 * split between a dumb component and its composable.
 */

import type { AdviceChipPayload } from '@/lib/types';

const props = defineProps<{
  chip: AdviceChipPayload;
}>();

const emit = defineEmits<{
  /** Fires when the user clicks the accept action. */
  accept: [chip: AdviceChipPayload];
  /** Fires when the user clicks the dismiss ("x") action. */
  dismiss: [chip: AdviceChipPayload];
}>();

/** Per-kind accept-button label (spec §2's consumer-action table). */
const acceptLabels: Record<string, string> = {
  branch_now: 'Branch it off',
  compact_now: 'Compact now',
  escalate_model: 'Switch model',
};

function acceptLabel(): string {
  return acceptLabels[props.chip.kind_id] ?? 'Accept';
}

function handleAccept() {
  emit('accept', props.chip);
}

function handleDismiss() {
  emit('dismiss', props.chip);
}
</script>

<template>
  <div
    class="flex items-start gap-2 rounded-md border border-accent-hairline bg-surface-1 px-3 py-2"
    role="note"
    :aria-label="`Advice: ${chip.title}`"
    data-testid="advice-chip"
    :data-kind-id="chip.kind_id"
  >
    <div class="flex-shrink-0 text-accent-strong" aria-hidden="true">💡</div>

    <div class="flex min-w-0 flex-1 flex-col gap-1">
      <span class="font-ui text-sm text-ink" data-testid="advice-chip-title">
        {{ chip.title }}
      </span>
      <span class="font-ui text-xs text-ink-muted" data-testid="advice-chip-body">
        {{ chip.body }}
      </span>

      <div class="flex flex-wrap items-center gap-2">
        <button
          type="button"
          class="rounded-md bg-accent-strong px-2.5 py-1 font-ui text-xs text-on-accent"
          data-testid="advice-chip-accept"
          @click="handleAccept"
        >
          {{ acceptLabel() }}
        </button>
        <button
          type="button"
          class="font-ui text-xs text-ink-muted hover:text-ink"
          data-testid="advice-chip-dismiss"
          aria-label="Dismiss"
          @click="handleDismiss"
        >
          Dismiss
        </button>
      </div>
    </div>
  </div>
</template>
