<script setup lang="ts">
/**
 * MemoryCaptureRatePill — §2.7 LegendBar capture-rate widget.
 *
 * Shows "🧠 N/min" with a colored dot reflecting embedder health:
 *   green  — ok
 *   amber  — slow (last embedder call > 5s)
 *   red    — error (> 3 embedder errors in the last 5 min)
 *
 * Hidden when chunksPerMinute === 0 and no errors (avoids cluttering
 * the LegendBar during inactivity).
 *
 * Polls Memory_CaptureRate every 5 s while mounted.
 * Click → navigates to /memory.
 */
import { ref, computed, onMounted, onBeforeUnmount } from 'vue';
import { useRouter } from 'vue-router';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { MemoryCaptureRateSnapshot } from '@/lib/types';

const POLL_MS = 5000;

const router = useRouter();
const client = useHarnessClient();

const snapshot = ref<MemoryCaptureRateSnapshot>({
  chunksPerMinute: 0,
  embedderHealth: 'ok',
  lastErrorAt: null,
  recentErrorCount: 0,
});

const visible = computed(
  () =>
    snapshot.value.chunksPerMinute > 0 ||
    snapshot.value.recentErrorCount > 0,
);

const rateLabel = computed(() => {
  const n = Math.round(snapshot.value.chunksPerMinute);
  return `${n}/min`;
});

const dotClass = computed(() => {
  switch (snapshot.value.embedderHealth) {
    case 'error':
      return 'bg-signal-danger';
    case 'slow':
      return 'bg-signal-warn';
    default:
      return 'bg-signal-ok';
  }
});

const tooltipText = computed(() => {
  const h = snapshot.value.embedderHealth;
  if (h === 'error')
    return `Embedder errors: ${snapshot.value.recentErrorCount} in last 5 min`;
  if (h === 'slow') return 'Embedder is slow (last call > 5s)';
  return 'Memory capturing OK';
});

let timer: ReturnType<typeof setInterval> | null = null;

async function poll() {
  try {
    snapshot.value = await client.memory.captureRate();
  } catch {
    // non-fatal: keep showing last snapshot
  }
}

onMounted(() => {
  void poll();
  timer = setInterval(() => {
    void poll();
  }, POLL_MS);
});

onBeforeUnmount(() => {
  if (timer !== null) {
    clearInterval(timer);
    timer = null;
  }
});

function onClick() {
  void router.push('/memory');
}
</script>

<template>
  <button
    v-if="visible"
    type="button"
    :title="tooltipText"
    aria-label="Memory capture rate"
    class="flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] font-ui text-ink-muted hover:bg-surface-hover focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand transition-colors"
    @click="onClick"
  >
    <span aria-hidden="true">🧠</span>
    <span class="font-mono tabular-nums">{{ rateLabel }}</span>
    <span
      :class="['h-1.5 w-1.5 rounded-full', dotClass]"
      aria-hidden="true"
    />
  </button>
</template>
