<script setup lang="ts">
/**
 * ModelCallsDisclosure — the read-only "Model calls" view under the
 * session's cost readout (llm-call-telemetry-01LLMTEL01 WP02).
 *
 * A "calls" toggle next to the cost pill opens a panel listing the
 * session's most recent provider calls from the local model-call store
 * (Sessions_ModelCalls): time, model, provider / upstream, tokens in /
 * out / cached, latency and time to first token, cost, finish reason or
 * error class, and — once the OpenRouter generation-stats pull has
 * landed — the provider's own latency, generation time and cost. Every
 * value is an allowlisted, bounded field; the store holds no content.
 *
 * The list is fetched when the panel opens and again on each reopen.
 * Served mode shows the boundary panel and fetches nothing.
 */
import { computed, ref } from 'vue';
import NotAvailableInServedMode from '@/components/ui/NotAvailableInServedMode.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { formatCost } from '@/lib/formatCost';
import type { ModelCall } from '@/lib/types';

const props = withDefaults(
  defineProps<{
    sessionId: string;
    servedMode?: boolean;
    limit?: number;
  }>(),
  { servedMode: false, limit: 50 },
);

const client = useHarnessClient();
const open = ref(false);
const loading = ref(false);
const error = ref('');
const calls = ref<ModelCall[]>([]);
const panelId = `model-calls-${Math.random().toString(36).slice(2, 10)}`;

async function load() {
  if (props.servedMode || !props.sessionId) return;
  loading.value = true;
  error.value = '';
  try {
    calls.value = await client.sessions.modelCalls(props.sessionId, props.limit);
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}

function toggle() {
  open.value = !open.value;
  if (open.value) void load();
}

function close() {
  open.value = false;
}

function fmtTokens(n: number | null | undefined): string {
  if (n === null || n === undefined) return '—';
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M';
  if (n >= 10_000) return Math.round(n / 1_000) + 'k';
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'k';
  return String(n);
}

function fmtMs(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || ms <= 0) return '—';
  if (ms < 1_000) return `${ms} ms`;
  return `${(ms / 1_000).toFixed(1)} s`;
}

function fmtTime(ms: number): string {
  return new Date(ms).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

function model(c: ModelCall): string {
  return c.responseModel || c.requestModel || '—';
}

function route(c: ModelCall): string {
  return c.upstream ? `${c.provider} → ${c.upstream}` : c.provider || '—';
}

function outcome(c: ModelCall): string {
  if (c.status === 'error') return c.errorType || 'error';
  return c.finishReasons.length ? c.finishReasons.join(', ') : '—';
}

function cost(c: ModelCall): string {
  const usd = c.providerCostUsd ?? c.costUsd;
  return usd === null || usd === undefined ? '—' : formatCost(usd);
}

const summary = computed(() => {
  const n = calls.value.length;
  const errors = calls.value.filter((c) => c.status === 'error').length;
  if (n === 0) return '';
  return `${n} call${n === 1 ? '' : 's'}${errors ? ` · ${errors} failed` : ''}`;
});
</script>

<template>
  <div class="relative" @keydown.esc="close">
    <button
      type="button"
      class="uppercase tracking-[0.14em] text-ink-subtle hover:text-ink-muted"
      data-testid="model-calls-toggle"
      :aria-expanded="open"
      :aria-controls="open ? panelId : undefined"
      title="Model calls for this session"
      @click="toggle"
    >
      calls
    </button>
    <div
      v-if="open"
      :id="panelId"
      role="region"
      aria-label="Model calls"
      class="absolute bottom-full right-0 z-20 mb-2 max-h-96 w-[34rem] max-w-[90vw] overflow-auto rounded-md border border-hairline bg-surface-1 p-3 text-xs shadow-lg normal-case tracking-normal"
      data-testid="model-calls-panel"
    >
      <NotAvailableInServedMode
        v-if="servedMode"
        feature="Model calls"
        reason="The per-call record is kept in the local store of Kenaz on the host; the in-workbench build does not read it."
      />
      <template v-else>
        <div class="mb-2 flex items-baseline justify-between gap-3">
          <p class="font-semibold text-ink">Model calls</p>
          <p class="text-ink-subtle" data-testid="model-calls-summary">{{ summary }}</p>
        </div>
        <p v-if="loading && calls.length === 0" class="text-ink-muted" data-testid="model-calls-loading">Loading…</p>
        <p v-else-if="error" class="text-signal-danger" data-testid="model-calls-error">{{ error }}</p>
        <p v-else-if="calls.length === 0" class="text-ink-muted" data-testid="model-calls-empty">
          No model calls recorded for this session yet.
        </p>
        <ul v-else class="flex flex-col divide-y divide-hairline">
          <li
            v-for="c in calls"
            :key="c.spanId"
            class="py-1.5"
            :data-testid="`model-call-${c.spanId}`"
          >
            <div class="flex items-baseline justify-between gap-3">
              <span class="font-mono text-ink" data-testid="model-call-model">{{ model(c) }}</span>
              <span class="font-mono tabular-nums text-ink-subtle">{{ fmtTime(c.startedAtMs) }}</span>
            </div>
            <div class="mt-0.5 flex flex-wrap gap-x-3 gap-y-0.5 text-ink-muted">
              <span data-testid="model-call-route">{{ route(c) }}</span>
              <span data-testid="model-call-tokens">
                in {{ fmtTokens(c.inputTokens) }} · out {{ fmtTokens(c.outputTokens) }} · cached {{ fmtTokens(c.cacheReadTokens) }}
              </span>
              <span data-testid="model-call-latency">{{ fmtMs(c.durationMs) }} · ttft {{ fmtMs(c.ttftMs) }}</span>
              <span class="font-mono tabular-nums" data-testid="model-call-cost">{{ cost(c) }}</span>
              <span
                :class="c.status === 'error' ? 'text-signal-danger' : ''"
                data-testid="model-call-outcome"
              >{{ outcome(c) }}</span>
              <span v-if="c.attempt > 0 || c.fallback" class="text-ink-subtle" data-testid="model-call-attempt">
                attempt {{ c.attempt + 1 }}<template v-if="c.fallback"> · fallback</template>
              </span>
            </div>
            <div
              v-if="c.statsState === 'fetched'"
              class="mt-0.5 text-ink-subtle"
              data-testid="model-call-provider-stats"
            >
              Provider: latency {{ fmtMs(c.providerLatencyMs) }} · generation {{ fmtMs(c.providerGenerationTimeMs) }}
              · native in {{ fmtTokens(c.nativeInputTokens) }} / out {{ fmtTokens(c.nativeOutputTokens) }}
            </div>
          </li>
        </ul>
        <p class="mt-2 text-ink-subtle">
          Stored on this device only. No prompt or response text is recorded.
        </p>
      </template>
    </div>
  </div>
</template>
