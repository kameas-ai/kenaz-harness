<script setup lang="ts">
/**
 * SessionTunePanel — per-session request-knob configuration panel.
 *
 * Allows the user to set session-level defaults for reasoning effort and
 * other RequestKnobs fields. The panel is opened from the chat header
 * (via a control that appears when the active model has a reasoning knob).
 *
 * Architecture:
 *   - The panel receives the current session knobs as props and emits
 *     a 'change' event when the user saves.
 *   - The parent (SessionsView) owns the state and calls Sessions_SetKnobsDefault
 *     (migration 0330) to persist to the DB.
 *   - Until the backend RPC method is wired, the change event updates
 *     local session state so the UX is complete end-to-end.
 *
 * ReasoningStyle routing:
 *   - 'effort_string'  → dropdown: minimal | low | medium | high
 *   - 'token_budget'   → number input (integer > 0)
 *   - 'both'           → both controls shown
 *   - 'none'           → reasoning section hidden
 *
 * (provider-implementation-uniformity-01KQ8V4F WP09)
 */

import { ref, watch, computed } from 'vue';
import type { ReasoningConfig, ReasoningStyle } from '@/lib/types';

const props = defineProps<{
  sessionId: string;
  /** How the active model expresses its reasoning knob. */
  reasoningStyle: ReasoningStyle;
  /** Current session-level knobs, if any. */
  knobs?: ReasoningConfig;
}>();

const emit = defineEmits<{
  change: [knobs: ReasoningConfig | null];
}>();

// ── Local editing state ───────────────────────────────────────────────

/** OpenAI effort level (used when reasoningStyle is effort_string | both). */
const effortLevel = ref<string>(props.knobs?.openAIEffort ?? '');
/** Anthropic token budget (used when reasoningStyle is token_budget | both). */
const tokenBudget = ref<string>(
  props.knobs?.anthropicThinkingBudget
    ? String(props.knobs.anthropicThinkingBudget)
    : '',
);

const saveError = ref<string | null>(null);
const saved = ref(false);

const showReasoning = computed(() => props.reasoningStyle !== 'none');
const showEffortDropdown = computed(
  () =>
    props.reasoningStyle === 'effort_string' ||
    props.reasoningStyle === 'both',
);
const showTokenBudget = computed(
  () =>
    props.reasoningStyle === 'token_budget' ||
    props.reasoningStyle === 'both',
);

// Sync inputs when parent knobs prop changes (e.g. after model switch).
watch(
  () => props.knobs,
  (k) => {
    effortLevel.value = k?.openAIEffort ?? '';
    tokenBudget.value = k?.anthropicThinkingBudget
      ? String(k.anthropicThinkingBudget)
      : '';
  },
);

const EFFORT_OPTIONS = ['', 'minimal', 'low', 'medium', 'high'] as const;

function validate(): string | null {
  if (showTokenBudget.value && tokenBudget.value) {
    const n = parseInt(tokenBudget.value, 10);
    if (isNaN(n) || n <= 0) {
      return 'Token budget must be a positive integer.';
    }
  }
  return null;
}

function onSave() {
  saveError.value = null;
  saved.value = false;

  const err = validate();
  if (err) {
    saveError.value = err;
    return;
  }

  const updated: ReasoningConfig = {};
  if (showEffortDropdown.value && effortLevel.value) {
    updated.openAIEffort = effortLevel.value;
  }
  if (showTokenBudget.value && tokenBudget.value) {
    const n = parseInt(tokenBudget.value, 10);
    if (n > 0) updated.anthropicThinkingBudget = n;
  }

  emit('change', Object.keys(updated).length > 0 ? updated : null);
  saved.value = true;
  setTimeout(() => {
    saved.value = false;
  }, 2000);
}

function onReset() {
  effortLevel.value = '';
  tokenBudget.value = '';
  saveError.value = null;
  emit('change', null);
}
</script>

<template>
  <div class="space-y-4" data-testid="session-tune-panel">
    <!-- Reasoning effort section -->
    <section v-if="showReasoning" class="space-y-2">
      <h4 class="font-ui text-[11px] uppercase tracking-[0.15em] text-ink-subtle">
        Reasoning effort
      </h4>

      <!-- Effort string dropdown (OpenAI o-series) -->
      <div v-if="showEffortDropdown" class="space-y-1">
        <label
          class="block font-ui text-[11px] text-ink-muted"
          for="effort-level-select"
        >
          Effort level
        </label>
        <select
          id="effort-level-select"
          v-model="effortLevel"
          class="w-full rounded-sm border border-border bg-surface-1 px-2 py-1 font-ui text-xs text-ink focus:border-accent focus:outline-none"
          data-testid="effort-level-select"
        >
          <option value="">— inherit default —</option>
          <option v-for="opt in EFFORT_OPTIONS.slice(1)" :key="opt" :value="opt">
            {{ opt }}
          </option>
        </select>
      </div>

      <!-- Token budget input (Anthropic) -->
      <div v-if="showTokenBudget" class="space-y-1">
        <label
          class="block font-ui text-[11px] text-ink-muted"
          for="token-budget-input"
        >
          Thinking token budget
        </label>
        <div class="flex items-center gap-1">
          <input
            id="token-budget-input"
            v-model="tokenBudget"
            type="number"
            min="1"
            placeholder="e.g. 16000"
            class="flex-1 rounded-sm border border-border bg-surface-1 px-2 py-1 font-mono text-xs text-ink focus:border-accent focus:outline-none"
            data-testid="token-budget-input"
          />
          <span class="font-ui text-[11px] text-ink-muted">tokens</span>
        </div>
      </div>

      <p class="font-ui text-[10px] text-ink-subtle">
        Session default — takes effect on the next message.
        Use <code class="font-mono">/effort</code> in chat for one-turn overrides.
      </p>
    </section>

    <!-- No-reasoning fallback -->
    <p
      v-else
      class="font-ui text-xs text-ink-subtle"
      data-testid="no-reasoning-hint"
    >
      The active model has no reasoning knob. Switch to a reasoning-capable
      model to configure extended thinking.
    </p>

    <!-- Save / reset -->
    <div class="flex items-center gap-2">
      <button
        type="button"
        class="rounded-sm bg-accent px-3 py-1 font-ui text-[11px] text-white transition-colors hover:bg-accent/90 disabled:opacity-40"
        data-testid="tune-panel-save"
        @click="onSave"
      >
        Apply to session
      </button>
      <button
        type="button"
        class="rounded-sm border border-border-muted px-3 py-1 font-ui text-[11px] text-ink-muted transition-colors hover:text-ink"
        data-testid="tune-panel-reset"
        @click="onReset"
      >
        Reset
      </button>
      <span
        v-if="saved"
        class="font-ui text-[11px] text-signal-success"
        data-testid="tune-panel-saved-badge"
      >
        Saved
      </span>
    </div>

    <p
      v-if="saveError"
      class="font-ui text-[11px] text-signal-danger"
      data-testid="tune-panel-error"
    >
      {{ saveError }}
    </p>
  </div>
</template>
