<script setup lang="ts">
/**
 * TraceLink — renders an OpenTelemetry trace_id link when OTel is active.
 *
 * Visibility: only shown when otelActive is true (defaults to false when
 * OTel is not configured so the component is invisible on a fresh install).
 *
 * The link target is configurable via traceBaseUrl; defaults to the
 * Jaeger UI convention (<base>/trace/<trace_id>).
 */
import { computed } from 'vue';

const props = withDefaults(
  defineProps<{
    /** 128-bit OTel trace ID in hex (32 chars). */
    traceId: string;
    /** Base URL of the tracing UI (e.g. http://localhost:16686). */
    traceBaseUrl?: string;
    /** Only render the link when OTel is active. */
    otelActive?: boolean;
  }>(),
  {
    traceBaseUrl: 'http://localhost:16686',
    otelActive: false,
  },
);

const href = computed(() =>
  `${props.traceBaseUrl}/trace/${props.traceId}`,
);

const shortId = computed(() =>
  props.traceId.length > 8 ? props.traceId.slice(0, 8) + '…' : props.traceId,
);
</script>

<template>
  <a
    v-if="otelActive && traceId"
    :href="href"
    target="_blank"
    rel="noopener noreferrer"
    class="inline-flex items-center gap-1 px-1.5 py-0.5 text-[10px] font-mono rounded border border-border-muted text-ink-muted hover:text-accent hover:border-accent transition-colors"
    :title="`OTel trace: ${traceId}`"
    @click.stop
  >
    trace:{{ shortId }}
  </a>
</template>
