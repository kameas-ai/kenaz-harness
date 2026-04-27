<script setup lang="ts">
/**
 * CapHitToast — pop-up surfaced when a per-run cap fires (Bundle E
 * WP17). Lets the user "bump + resume" without leaving the chat
 * surface. Closing the toast leaves the run paused.
 *
 * Props:
 *   - visible: whether to render the toast
 *   - reason: e.g. "max_tokens_per_run"
 *   - limit + used: context numbers from the EventLog payload
 *   - runID: the paused run; passed back through bumpAndResume
 *
 * Emits:
 *   - close: user dismissed the toast (run stays paused)
 *   - resumed: bump succeeded and resume was triggered
 */
import { computed, onMounted, ref, watch } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { DialDelta } from '@/lib/types';

const props = defineProps<{
  visible: boolean;
  runID: string;
  reason: string;
  limit: number;
  used: number;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'resumed'): void;
}>();

const client = useHarnessClient();
const bumpAmount = ref<number>(0);
const submitting = ref(false);
const error = ref<string | null>(null);

const knob = computed(() => {
  switch (props.reason) {
    case 'max_tokens_per_run':
      return { label: 'tokens', step: 1000, default: 5000, key: 'addTokensPerRun' as const };
    case 'max_llm_calls_per_run':
      return { label: 'LLM calls', step: 1, default: 10, key: 'addLLMCalls' as const };
    case 'max_tool_calls_per_run':
      return { label: 'tool calls', step: 1, default: 10, key: 'addToolCalls' as const };
    case 'max_cost_usd_per_run':
      return { label: 'USD', step: 0.5, default: 1, key: 'addCostUSD' as const };
    case 'max_wallclock_per_run_seconds':
      return { label: 'seconds', step: 30, default: 60, key: 'addWallclockSeconds' as const };
    default:
      return { label: 'units', step: 1, default: 1, key: 'addTokensPerRun' as const };
  }
});

watch(
  () => props.visible,
  (v) => {
    if (v) {
      bumpAmount.value = knob.value.default;
      error.value = null;
    }
  },
  { immediate: true },
);

onMounted(() => {
  // Defensive: when the parent mounts the toast already-visible, the
  // watcher should fire via { immediate: true } above; double-set the
  // default here so older Vue versions still seed correctly.
  if (props.visible) {
    bumpAmount.value = knob.value.default;
  }
});

async function bumpAndResume() {
  submitting.value = true;
  error.value = null;
  try {
    const delta: DialDelta = { [knob.value.key]: bumpAmount.value } as DialDelta;
    await client.dials.bumpAndResume(props.runID, delta);
    emit('resumed');
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    submitting.value = false;
  }
}

function dismiss() {
  emit('close');
}
</script>

<template>
  <Transition name="caphit-fade">
    <div
      v-if="visible"
      class="fixed bottom-6 right-6 z-50 w-[26rem] max-w-[92vw] rounded-md border border-signal-warning bg-surface-1 p-4 shadow-lg"
      role="alertdialog"
      aria-labelledby="caphit-title"
      data-testid="cap-hit-toast"
    >
      <h4
        id="caphit-title"
        class="font-ui text-[11px] uppercase tracking-[0.18em] text-signal-warning"
      >
        Cap hit · {{ reason }}
      </h4>
      <p class="mt-2 font-ui text-sm text-ink">
        Used <span class="font-mono">{{ used }}</span> of
        <span class="font-mono">{{ limit }}</span> {{ knob.label }}.
        Bump the cap and resume, or close to leave the run paused.
      </p>

      <div class="mt-3 flex items-center gap-2 font-ui text-[12px]">
        <label for="caphit-amount" class="text-ink-dim uppercase tracking-[0.18em]">
          Bump
        </label>
        <input
          id="caphit-amount"
          v-model.number="bumpAmount"
          type="number"
          :step="knob.step"
          min="0"
          class="w-24 px-2 py-0.5 rounded-sm border border-border-muted bg-surface-1 font-mono text-[12px] text-ink"
          data-testid="cap-hit-amount"
        />
        <span class="text-ink-dim">{{ knob.label }}</span>
      </div>

      <p
        v-if="error"
        class="mt-2 font-ui text-[12px] text-signal-danger"
        role="alert"
      >
        {{ error }}
      </p>

      <div class="mt-4 flex justify-end gap-2">
        <button
          type="button"
          class="px-3 py-1 rounded-sm border border-border-muted font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2"
          data-testid="cap-hit-close"
          @click="dismiss"
        >
          Close
        </button>
        <button
          type="button"
          class="px-3 py-1 rounded-sm border border-accent font-ui text-[11px] uppercase tracking-[0.18em] text-accent hover:bg-surface-2 disabled:opacity-50"
          :disabled="submitting || bumpAmount <= 0"
          data-testid="cap-hit-resume"
          @click="bumpAndResume"
        >
          {{ submitting ? 'Resuming…' : 'Bump + resume' }}
        </button>
      </div>
    </div>
  </Transition>
</template>

<style scoped>
.caphit-fade-enter-active,
.caphit-fade-leave-active {
  transition: opacity 0.15s ease;
}
.caphit-fade-enter-from,
.caphit-fade-leave-to {
  opacity: 0;
}
</style>
