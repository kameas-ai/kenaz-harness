<script setup lang="ts">
import { Settings as SettingsIcon } from './icons';

/**
 * Toolbar — surface-level action row hosting Customize and other
 * per-surface affordances (plan §2.1). Surfaces inject their own
 * actions via a slot.
 */
</script>

<template>
  <div class="flex items-center justify-between px-4 py-2">
    <slot name="left" />
    <div class="flex items-center gap-2 ml-auto text-ink-muted text-xs font-ui">
      <slot name="right">
        <button
          type="button"
          class="flex items-center gap-1 px-2 py-1 rounded-sm hover:text-ink hover:bg-surface-2 transition-fast ease-kenaz"
          aria-label="Customize this surface"
        >
          <SettingsIcon :size="12" />
          <span>Customize</span>
        </button>
      </slot>
    </div>
  </div>
</template>
