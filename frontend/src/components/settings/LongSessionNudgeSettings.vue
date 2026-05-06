<script setup lang="ts">
/**
 * LongSessionNudgeSettings — Settings sub-section for the long-session
 * nudge banner thresholds (v0.5.6 memory-trust-signals).
 *
 * Two controls:
 *   1. Number — "Turn-count threshold" (longSessionNudgeTurns)
 *   2. Number — "Token threshold" (longSessionNudgeTokens)
 *
 * Both persist via debouncedSave. The Go-side EffectiveLong* accessors
 * clamp zero → defaults so the user can reset to defaults by clearing.
 */
import { onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { debouncedSave } from '@/lib/settings';
import type { Settings } from '@/lib/types';

// Mirrors Go-side constants (api.go DefaultLongSession*).
const DEFAULT_NUDGE_TURNS = 30;
const DEFAULT_NUDGE_TOKENS = 50_000;
const MIN_NUDGE_TURNS = 1;
const MIN_NUDGE_TOKENS = 1_000;

const client = useHarnessClient();

const settings = ref<Settings>({
  schemaVersion: 1,
  lastRoute: '/sessions',
  theme: 'system',
  accent: 'default',
  windowSize: { width: 1280, height: 800 },
});

// Working copies — hydrated from settings on mount.
const nudgeTurns = ref(DEFAULT_NUDGE_TURNS);
const nudgeTokens = ref(DEFAULT_NUDGE_TOKENS);
const turnsError = ref<string | null>(null);
const tokensError = ref<string | null>(null);

onMounted(async () => {
  try {
    settings.value = await client.settings.get();
  } catch {
    // Keep defaults on error.
  }
  nudgeTurns.value = settings.value.longSessionNudgeTurns || DEFAULT_NUDGE_TURNS;
  nudgeTokens.value = settings.value.longSessionNudgeTokens || DEFAULT_NUDGE_TOKENS;
  turnsError.value = null;
  tokensError.value = null;
});

function persist(patch: Partial<Settings>): void {
  debouncedSave(client, { ...settings.value, ...patch });
}

function onTurnsInput(evt: Event): void {
  const raw = parseInt((evt.target as HTMLInputElement).value, 10);
  if (Number.isNaN(raw)) {
    turnsError.value = `Must be a whole number (e.g. ${DEFAULT_NUDGE_TURNS}).`;
    return;
  }
  if (raw < MIN_NUDGE_TURNS) {
    turnsError.value = `Must be at least ${MIN_NUDGE_TURNS}.`;
    return;
  }
  turnsError.value = null;
  nudgeTurns.value = raw;
  persist({ longSessionNudgeTurns: raw });
}

function onTokensInput(evt: Event): void {
  const raw = parseInt((evt.target as HTMLInputElement).value, 10);
  if (Number.isNaN(raw)) {
    tokensError.value = `Must be a whole number (e.g. ${DEFAULT_NUDGE_TOKENS.toLocaleString()}).`;
    return;
  }
  if (raw < MIN_NUDGE_TOKENS) {
    tokensError.value = `Must be at least ${MIN_NUDGE_TOKENS.toLocaleString()}.`;
    return;
  }
  tokensError.value = null;
  nudgeTokens.value = raw;
  persist({ longSessionNudgeTokens: raw });
}
</script>

<template>
  <div class="grid gap-6" data-testid="long-session-nudge-settings">
    <!-- Turn-count threshold -->
    <section data-testid="nudge-turns-section">
      <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Long-session nudge — turn threshold
      </h2>
      <p class="mt-1 font-ui text-[11px] text-ink-muted">
        Number of user-assistant turn pairs (half the message count) after which the nudge
        banner appears. Default {{ DEFAULT_NUDGE_TURNS }}. Set to 0 to use the default.
      </p>
      <div class="mt-2">
        <input
          type="number"
          :min="MIN_NUDGE_TURNS"
          :value="nudgeTurns"
          class="w-28 rounded-sm border border-border bg-surface-1 px-2 py-1 font-ui text-[12px] text-ink"
          data-testid="nudge-turns-input"
          @input="onTurnsInput"
        />
        <p
          v-if="turnsError"
          class="mt-1 font-ui text-[11px] text-signal-danger"
          role="alert"
          data-testid="nudge-turns-error"
        >
          {{ turnsError }}
        </p>
        <p v-else class="mt-1 font-ui text-[11px] text-ink-muted">
          Pairs, not individual messages. 30 pairs = 60 messages.
        </p>
      </div>
    </section>

    <!-- Token threshold -->
    <section data-testid="nudge-tokens-section">
      <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Long-session nudge — token threshold
      </h2>
      <p class="mt-1 font-ui text-[11px] text-ink-muted">
        Cumulative prompt-token count after which the nudge banner appears, regardless of
        turn count. Default {{ DEFAULT_NUDGE_TOKENS.toLocaleString() }}. Set to 0 to use the default.
      </p>
      <div class="mt-2">
        <input
          type="number"
          :min="MIN_NUDGE_TOKENS"
          :value="nudgeTokens"
          class="w-36 rounded-sm border border-border bg-surface-1 px-2 py-1 font-ui text-[12px] text-ink"
          data-testid="nudge-tokens-input"
          @input="onTokensInput"
        />
        <p
          v-if="tokensError"
          class="mt-1 font-ui text-[11px] text-signal-danger"
          role="alert"
          data-testid="nudge-tokens-error"
        >
          {{ tokensError }}
        </p>
        <p v-else class="mt-1 font-ui text-[11px] text-ink-muted">
          Tokens in the prompt portion only (not completion).
        </p>
      </div>
    </section>
  </div>
</template>
