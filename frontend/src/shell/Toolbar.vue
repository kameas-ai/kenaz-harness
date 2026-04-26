<script setup lang="ts">
/**
 * Toolbar — surface-level action row (plan §2.1). Surfaces inject their
 * actions through left/right slots; the chassis provides no default
 * affordances of its own.
 */
</script>

<template>
  <div class="flex items-center justify-between px-4 py-2">
    <slot name="left" />
    <div class="flex items-center gap-2 ml-auto text-ink-muted text-xs font-ui">
      <slot name="right" />
    </div>
  </div>
</template>
