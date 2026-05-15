<script setup lang="ts">
/**
 * ReasoningControl — chat-header chip showing the active reasoning-effort
 * setting for the current session.
 *
 * The chip is only rendered when the active model has a reasoning knob
 * (style !== 'none'). It switches its display mode based on the provider
 * kind: effort strings (low/medium/high/minimal) for OpenAI o-series;
 * token-budget integers for Anthropic thinking-capable models.
 *
 * The chip is display-only. The actual setting is applied via the
 * /effort slash command (provider-implementation-uniformity-01KQ8V4F WP07).
 * When the user switches to a different model mid-session the parent
 * passes a new reasoningStyle and the chip rebinds. Both stored values
 * (openAIEffort + anthropicThinkingBudget) survive the model switch and
 * are restored when the user switches back.
 *
 * (provider-implementation-uniformity-01KQ8V4F WP07)
 */

import { computed, ref } from 'vue';
import type { ReasoningConfig, ReasoningStyle } from '@/lib/types';

const props = defineProps<{
  /**
   * How the active model expresses its reasoning knob. When 'none' the
   * component renders nothing. Parent derives this from the provider kind
   * + model ID without a round-trip (Anthropic sonnet/opus → token_budget;
   * OpenAI o-series → effort_string).
   */
  reasoningStyle: ReasoningStyle;
  /**
   * The current reasoning configuration set via /effort. May be undefined
   * when the user has not issued /effort yet for this session.
   */
  config?: ReasoningConfig;
}>();

const open = ref(false);

/** True when the chip should be shown. */
const visible = computed(() => props.reasoningStyle !== 'none');

/** True when an effort value is actively set. */
const isSet = computed<boolean>(() => {
  const c = props.config;
  if (!c) return false;
  if (props.reasoningStyle === 'effort_string' || props.reasoningStyle === 'both') {
    if (c.openAIEffort) return true;
  }
  if (props.reasoningStyle === 'token_budget' || props.reasoningStyle === 'both') {
    if (c.anthropicThinkingBudget && c.anthropicThinkingBudget > 0) return true;
  }
  return false;
});

/** Label shown in the chip. */
const label = computed<string>(() => {
  const c = props.config;
  if (!c) return 'effort: —';
  if (props.reasoningStyle === 'effort_string' || props.reasoningStyle === 'both') {
    if (c.openAIEffort) return `effort: ${c.openAIEffort}`;
  }
  if (props.reasoningStyle === 'token_budget' || props.reasoningStyle === 'both') {
    if (c.anthropicThinkingBudget && c.anthropicThinkingBudget > 0) {
      return `effort: ${c.anthropicThinkingBudget.toLocaleString()} tok`;
    }
  }
  return 'effort: —';
});

/** Tooltip text explaining how to change the value. */
const tooltip = computed<string>(() => {
  if (props.reasoningStyle === 'effort_string') {
    return 'Reasoning effort (OpenAI). Use /effort low|medium|high|minimal to change.';
  }
  if (props.reasoningStyle === 'token_budget') {
    return 'Reasoning token budget (Anthropic). Use /effort <tokens> to change, e.g. /effort 16000.';
  }
  if (props.reasoningStyle === 'both') {
    return 'Reasoning effort. Use /effort low|medium|high|minimal or /effort <tokens>.';
  }
  return '';
});

function toggle() {
  open.value = !open.value;
}
</script>

<template>
  <div
    v-if="visible"
    class="relative inline-flex"
    data-testid="reasoning-control"
  >
    <button
      type="button"
      class="inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 font-ui text-[11px] uppercase tracking-[0.12em] transition-colors"
      :class="
        isSet
          ? 'border-accent text-accent bg-accent/10 hover:bg-accent/15'
          : 'border-border-muted text-ink-muted bg-surface-1 hover:text-ink'
      "
      :title="tooltip"
      :aria-expanded="open"
      data-testid="reasoning-control-button"
      @click="toggle"
    >
      <span aria-hidden="true">◆</span>
      {{ label }}
    </button>

    <!-- Popover: inform user how to set effort via /effort command -->
    <div
      v-if="open"
      class="absolute right-0 top-full z-40 mt-2 w-72 max-w-[90vw] rounded-md border border-border bg-surface-1 p-4 shadow-lg"
      role="tooltip"
      data-testid="reasoning-control-popover"
    >
      <div class="mb-2 flex items-center justify-between">
        <h3 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Reasoning effort
        </h3>
        <button
          type="button"
          class="font-ui text-[11px] text-ink-muted hover:text-ink"
          data-testid="reasoning-control-close"
          @click="open = false"
        >
          Close
        </button>
      </div>

      <p class="text-xs text-ink-muted mb-2">
        <template v-if="reasoningStyle === 'effort_string'">
          Set reasoning effort with
          <code class="font-mono bg-surface-2 rounded px-1">/effort low|medium|high|minimal</code>.
        </template>
        <template v-else-if="reasoningStyle === 'token_budget'">
          Set reasoning token budget with
          <code class="font-mono bg-surface-2 rounded px-1">/effort &lt;tokens&gt;</code>,
          e.g. <code class="font-mono bg-surface-2 rounded px-1">/effort 16000</code>.
        </template>
        <template v-else>
          Set reasoning effort with <code class="font-mono bg-surface-2 rounded px-1">/effort</code>.
          Use a level (low|medium|high|minimal) or a token budget (integer).
        </template>
      </p>

      <div v-if="isSet" class="mt-1 text-xs text-ink-subtle">
        Current:
        <span class="font-medium text-accent">{{ label }}</span>
      </div>
      <div v-else class="mt-1 text-xs text-ink-subtle">
        No reasoning effort set for this session yet.
      </div>
    </div>
  </div>
</template>
