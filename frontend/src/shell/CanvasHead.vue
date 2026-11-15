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
  <!--
    Mirrors the DS v0.13.0 `.canvas-head` rules (styles/components.css):
    mono breadcrumb at --type-small-size in --ink-subtle, a Geist 600
    title at the DS page-title size, mono subtitle. Padding is the DS
    canvas inset (--space-24 / --space-32).
  -->
  <header class="px-8 pt-6 pb-4 border-b border-border-muted bg-surface-0">
    <div
      v-if="number && section"
      class="mb-2 flex items-baseline gap-1.5 font-mono text-small tracking-meta text-ink-subtle"
    >
      <span>{{ number }}</span>
      <span class="text-ink-dim">/</span>
      <span class="uppercase">{{ section }}</span>
    </div>
    <!--
      Page title. Every view renders its title through this component, so
      this is the app-wide page-title rule. DS 0.12 retired the serif;
      the face is --font-heading (Geist). The DS sets the page title to a
      literal 28px in `.canvas-head__title` and publishes no token for it
      (design gap, see fleet design-gaps/harness.md).
    -->
    <!-- css-tokens-allow-next-line: DS .canvas-head__title is a literal 28px with no --type-* token (design gap) -->
    <h1 class="font-heading text-[28px] font-semibold tracking-hero text-ink">
      {{ title }}
    </h1>
    <p
      v-if="subtitle"
      class="mt-1 max-w-prose font-mono text-small tracking-meta text-ink-subtle"
    >
      {{ subtitle }}
    </p>
    <div class="mt-3">
      <slot name="trailing" />
    </div>
  </header>
</template>
