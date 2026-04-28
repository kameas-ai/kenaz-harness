<script setup lang="ts">
/**
 * CanvasHead — Kenaz section header.
 *
 * Default (with number + section):
 *   01 / SESSIONS         <- muted small-caps, separator
 *   Recent runs           <- prominent title, Geist Semibold
 *   <subtitle>            <- one-paragraph subtitle, --ink-muted
 *
 * Minimal (number/section/subtitle omitted): just the title, used for
 * the active chat surface where the session name is the only useful
 * affordance and breadcrumb chrome wastes vertical space.
 */
defineProps<{
  number?: string;
  section?: string;
  title: string;
  subtitle?: string;
}>();
</script>

<template>
  <header class="px-6 pt-6 pb-4 border-b border-border-muted bg-surface-0">
    <div
      v-if="number && section"
      class="flex items-baseline gap-2 text-ink-subtle"
    >
      <span class="font-ui text-[11px] font-medium uppercase tracking-[0.18em]">
        {{ number }}
      </span>
      <span class="text-ink-dim">/</span>
      <span class="font-ui text-[11px] font-medium uppercase tracking-[0.18em]">
        {{ section }}
      </span>
    </div>
    <h1
      class="font-ui text-2xl font-semibold tracking-tight text-ink"
      :class="number && section ? 'mt-2' : ''"
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
