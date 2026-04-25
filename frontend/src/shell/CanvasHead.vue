<script setup lang="ts">
/**
 * CanvasHead — Kenaz numbered-section header pattern (FR-001b).
 *
 *   01 / SESSIONS         <- muted small-caps, separator
 *   Recent runs           <- prominent title, Geist Semibold
 *   The harness preserves your conversation thread per session.
 *                         <- one-paragraph subtitle, --ink-muted
 */
defineProps<{
  number: string;
  section: string;
  title: string;
  subtitle?: string;
}>();
</script>

<template>
  <header class="px-6 pt-6 pb-4 border-b border-border-muted bg-surface-0">
    <div class="flex items-baseline gap-2 text-ink-subtle">
      <span class="font-ui text-[11px] font-medium uppercase tracking-[0.18em]">
        {{ number }}
      </span>
      <span class="text-ink-dim">/</span>
      <span class="font-ui text-[11px] font-medium uppercase tracking-[0.18em]">
        {{ section }}
      </span>
    </div>
    <h1
      class="mt-2 font-ui text-2xl font-semibold tracking-tight text-ink"
    >
      {{ title }}
    </h1>
    <p
      v-if="subtitle"
      class="mt-1 max-w-prose font-ui text-sm leading-relaxed text-ink-muted"
    >
      {{ subtitle }}
    </p>
    <div class="mt-3">
      <slot name="trailing" />
    </div>
  </header>
</template>
