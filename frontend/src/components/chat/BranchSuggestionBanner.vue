<script setup lang="ts">
/**
 * BranchSuggestionBanner — inline non-blocking banner rendered above the
 * most-recent user message when the branch-advisor heuristic fires.
 *
 * Three buttons (FR-002):
 *   [Branch it off]       → emit 'branchOff'; parent opens the context-pick modal.
 *   [No thanks]           → emit 'dismiss' with scope "message".
 *   [Don't suggest again] → emit 'dismiss' with scope "session".
 *
 * Race guard (plan §arch §2): the banner is suppressed when the user
 * sends again within 800ms of the suggestion appearing. The parent
 * (ChatInput) controls the 800ms debounce; this component just renders
 * when the `suggestion` prop is non-null.
 *
 * DIRECTIVE_001 / C-004: suggestions ride alongside the user-message
 * echo on the existing chat broker channel — no new RPC layer.
 */

import { ref } from 'vue';
import type { BranchSuggestion, BranchAdvisorDismissScope } from '@/lib/types';

const props = defineProps<{
  suggestion: BranchSuggestion;
}>();

const emit = defineEmits<{
  /**
   * Fires when the user clicks [Branch it off]. ChatInput handles this
   * via handleBannerBranchOff, which opens CreateBranchModal with the
   * suggestion's task hint pre-filled.
   */
  branchOff: [suggestion: BranchSuggestion];
  /**
   * Fires when the user clicks [No thanks] (scope "message") or
   * [Don't suggest again] (scope "session").
   */
  dismiss: [scope: BranchAdvisorDismissScope, suggestionId: string];
}>();

const tooltipVisible = ref(false);

function handleBranchOff() {
  emit('branchOff', props.suggestion);
}

function handleNoThanks() {
  emit('dismiss', 'message', props.suggestion.id);
}

function handleDontSuggestAgain() {
  emit('dismiss', 'session', props.suggestion.id);
}

function toggleTooltip() {
  tooltipVisible.value = !tooltipVisible.value;
}
</script>

<template>
  <div
    class="flex items-start gap-2 rounded-md border border-accent-hairline bg-surface-1 px-3 py-2"
    role="note"
    aria-label="Branch suggestion"
    data-testid="branch-suggestion-banner"
  >
    <!-- Icon + label -->
    <div class="flex-shrink-0 text-accent-strong" aria-hidden="true">⤳</div>

    <div class="flex min-w-0 flex-1 flex-col gap-1">
      <div class="flex items-center gap-1">
        <span class="font-ui text-sm text-ink">
          This looks like a side task.
        </span>
        <!-- Tooltip trigger (FR-011: "What signals were detected?") -->
        <button
          v-if="suggestion.signals && suggestion.signals.length > 0"
          type="button"
          class="font-ui text-[11px] text-ink-muted underline-offset-2 hover:underline"
          :aria-label="'What signals were detected?'"
          data-testid="branch-banner-rationale-trigger"
          @click="toggleTooltip"
        >
          Why?
        </button>
      </div>

      <!-- Signals tooltip (FR-011) -->
      <div
        v-if="tooltipVisible && suggestion.signals && suggestion.signals.length > 0"
        class="rounded border border-accent-hairline bg-surface-0 px-2 py-1 font-ui text-[11px] text-ink-subtle"
        data-testid="branch-banner-rationale"
      >
        {{ suggestion.rationale }}
      </div>

      <!-- Actions -->
      <div class="flex flex-wrap gap-2">
        <button
          type="button"
          class="rounded-md bg-accent-strong px-2.5 py-1 font-ui text-xs text-on-accent"
          data-testid="branch-banner-branch-off"
          @click="handleBranchOff"
        >
          Branch it off
        </button>
        <button
          type="button"
          class="font-ui text-xs text-ink-muted hover:text-ink"
          data-testid="branch-banner-no-thanks"
          @click="handleNoThanks"
        >
          No thanks
        </button>
        <button
          type="button"
          class="font-ui text-xs text-ink-subtle hover:text-ink-muted"
          data-testid="branch-banner-dont-suggest"
          @click="handleDontSuggestAgain"
        >
          Don't suggest again
        </button>
      </div>
    </div>
  </div>
</template>
