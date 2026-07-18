<script setup lang="ts">
import { computed } from 'vue';
import { CATEGORY_REGISTRY, type Category } from '@/lib/categories';

/**
 * EventStreamRow — Kenaz event-stream row primitive (plan §5.2):
 *   [timestamp] · [DOT + LABEL] · [subject] · [trailing-metadata]
 *
 * FR-002: `trailing` values of the form "count=N" (emitted by the backend
 * for chain-length / total-count metadata) are reformatted to "×N" so the
 * intent is clear to readers scanning the audit log.  Other trailing formats
 * (hex hash prefix, "payload_bytes=N", "payload_type=T") are passed through
 * verbatim.
 */
const props = defineProps<{
  timestamp: string;
  category: Category;
  subject: string;
  trailing?: string;
  size?: number;
}>();

const colorVar = computed(
  () => `var(${CATEGORY_REGISTRY[props.category].token})`,
);

const truncatedSubject = computed(() => {
  const s = props.subject ?? '';
  if (s.length <= 256) return s;
  const head = s.slice(0, 120);
  const tail = s.slice(-120);
  return `${head}…${tail}`;
});

/**
 * Reformat "count=N" trailing metadata as "×N" for readability (FR-002).
 * All other trailing strings pass through unchanged.
 */
const formattedTrailing = computed(() => {
  const t = props.trailing;
  if (!t) return t;
  const m = t.match(/^count=(\d+)$/);
  if (m) return `×${m[1]}`;
  return t;
});
</script>

<template>
  <div
    class="grid items-baseline gap-3 px-3 py-1 font-mono text-[12px] leading-5 hover:bg-surface-1"
    style="grid-template-columns: 18ch 14ch 1fr auto"
    role="row"
  >
    <span class="text-ink-muted truncate">{{ timestamp }}</span>
    <span class="flex items-center gap-1.5 truncate">
      <span
        class="inline-block w-1.5 h-1.5 rounded-full shrink-0"
        :style="{ background: colorVar }"
        aria-hidden="true"
      ></span>
      <span
        class="uppercase tracking-[0.12em] text-[11px]"
        :style="{ color: colorVar }"
      >
        {{ CATEGORY_REGISTRY[category].label }}
      </span>
    </span>
    <span class="text-ink truncate">{{ truncatedSubject }}</span>
    <span v-if="formattedTrailing" class="text-ink-subtle text-right truncate">
      {{ formattedTrailing }}
    </span>
  </div>
</template>
