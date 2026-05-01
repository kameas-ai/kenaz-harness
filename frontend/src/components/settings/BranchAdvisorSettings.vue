<script setup lang="ts">
/**
 * BranchAdvisorSettings — Settings sub-section for the branch advisor
 * (branch-as-subagent-recommendation-01KQ8TDJ WP08, FR-009/FR-010).
 *
 * Three controls:
 *   1. Toggle  — "Suggest spawning subagents" (BranchAdvisorEnabled)
 *   2. Slider  — "Suggestion confidence threshold" (BranchAdvisorMinConfidence)
 *   3. Number  — "Reintegration summary max tokens" (BranchReintegrationMaxTokens)
 *
 * All persists via debouncedSave. Number-input clamping is client-side;
 * the server also clamps via EffectiveBranchReintegrationMaxTokens.
 */
import { onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { debouncedSave } from '@/lib/settings';
import type { Settings } from '@/lib/types';

// Constants mirroring Go-side defaults / bounds.
const DEFAULT_MIN_CONFIDENCE = 0.85;
const DEFAULT_MAX_TOKENS = 2000;
const MIN_TOKENS = 500;
const MAX_TOKENS = 16000;

const client = useHarnessClient();

const settings = ref<Settings>({
  schemaVersion: 1,
  lastRoute: '/sessions',
  theme: 'system',
  accent: 'default',
  windowSize: { width: 1280, height: 800 },
});

// Working copies — hydrated from settings on mount.
const advisorEnabled = ref(false);
const minConfidence = ref(DEFAULT_MIN_CONFIDENCE);
const maxTokens = ref(DEFAULT_MAX_TOKENS);
const maxTokensError = ref<string | null>(null);

onMounted(async () => {
  try {
    settings.value = await client.settings.get();
  } catch {
    // Keep defaults on error.
  }
  advisorEnabled.value = settings.value.branchAdvisorEnabled ?? false;
  minConfidence.value =
    settings.value.branchAdvisorMinConfidence ?? DEFAULT_MIN_CONFIDENCE;
  maxTokens.value =
    settings.value.branchReintegrationMaxTokens ?? DEFAULT_MAX_TOKENS;
  maxTokensError.value = null;
});

function persist(patch: Partial<Settings>): void {
  debouncedSave(client, { ...settings.value, ...patch });
}

function onToggleEnabled(): void {
  advisorEnabled.value = !advisorEnabled.value;
  persist({ branchAdvisorEnabled: advisorEnabled.value });
}

function onConfidenceInput(evt: Event): void {
  const raw = parseFloat((evt.target as HTMLInputElement).value);
  if (!Number.isFinite(raw)) return;
  // Slider min/max attrs enforce [0.5, 0.99] in the DOM; clamp defensively.
  const clamped = Math.min(0.99, Math.max(0.5, raw));
  minConfidence.value = clamped;
  persist({ branchAdvisorMinConfidence: clamped });
}

function onMaxTokensInput(evt: Event): void {
  const raw = parseInt((evt.target as HTMLInputElement).value, 10);
  if (Number.isNaN(raw)) {
    maxTokensError.value = `Max tokens must be between ${MIN_TOKENS} and ${MAX_TOKENS}.`;
    return;
  }
  if (raw < MIN_TOKENS || raw > MAX_TOKENS) {
    maxTokensError.value = `Max tokens must be between ${MIN_TOKENS} and ${MAX_TOKENS}.`;
    return;
  }
  maxTokensError.value = null;
  maxTokens.value = raw;
  persist({ branchReintegrationMaxTokens: raw });
}
</script>

<template>
  <div class="grid gap-6" data-testid="branch-advisor-settings">
    <!-- Toggle: enable/disable advisor -->
    <section data-testid="branch-advisor-enabled-section">
      <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Suggest spawning subagents
      </h2>
      <label class="mt-2 flex items-center gap-3 font-ui text-[12px] text-ink">
        <input
          type="checkbox"
          class="accent-accent"
          :checked="advisorEnabled"
          data-testid="branch-advisor-enabled-toggle"
          @change="onToggleEnabled"
        />
        Show inline suggestions when a message looks like a good branch candidate
      </label>
    </section>

    <!-- Slider: confidence threshold -->
    <section data-testid="branch-advisor-confidence-section">
      <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Suggestion confidence threshold
      </h2>
      <p class="mt-1 font-ui text-[11px] text-ink-muted">
        Minimum confidence the detector must reach before a suggestion banner appears.
        Default {{ DEFAULT_MIN_CONFIDENCE }}.
      </p>
      <div class="mt-2 flex items-center gap-3">
        <input
          type="range"
          min="0.5"
          max="0.99"
          step="0.01"
          :value="minConfidence"
          class="w-48"
          data-testid="branch-advisor-confidence-slider"
          @input="onConfidenceInput"
        />
        <span class="font-mono text-[12px] text-ink" data-testid="branch-advisor-confidence-value">
          {{ minConfidence.toFixed(2) }}
        </span>
      </div>
    </section>

    <!-- Number input: max reintegration tokens -->
    <section data-testid="branch-advisor-max-tokens-section">
      <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Reintegration summary max tokens
      </h2>
      <p class="mt-1 font-ui text-[11px] text-ink-muted">
        Token budget for the branch reintegration summarisation call. Range {{ MIN_TOKENS }}–{{ MAX_TOKENS }}.
        Default {{ DEFAULT_MAX_TOKENS }}.
      </p>
      <div class="mt-2">
        <input
          type="number"
          :min="MIN_TOKENS"
          :max="MAX_TOKENS"
          :value="maxTokens"
          class="w-28 rounded-sm border border-border bg-surface-1 px-2 py-1 font-ui text-[12px] text-ink"
          data-testid="branch-advisor-max-tokens-input"
          @input="onMaxTokensInput"
        />
        <p
          v-if="maxTokensError"
          class="mt-1 font-ui text-[11px] text-signal-danger"
          role="alert"
          data-testid="branch-advisor-max-tokens-error"
        >
          {{ maxTokensError }}
        </p>
        <p v-else class="mt-1 font-ui text-[11px] text-ink-muted">
          Short branches produce short summaries regardless of this cap.
        </p>
      </div>
    </section>
  </div>
</template>
