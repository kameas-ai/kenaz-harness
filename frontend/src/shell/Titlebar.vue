<script setup lang="ts">
import { Command, Sun } from './icons';
import { useCommandPalette } from '@/lib/useCommandPalette';
import { useTheme } from '@/lib/useTheme';

defineProps<{
  /** Per-surface override; defaults to true so AI-output disclaimer is
   *  visible on every surface unless explicitly suppressed (FR-001h). */
  showDisclaimer?: boolean;
}>();

const palette = useCommandPalette();
const theme = useTheme();
</script>

<template>
  <div class="flex items-center justify-between px-4 py-2 select-none">
    <div class="flex items-center gap-3">
      <span class="font-ui text-sm font-medium tracking-tight text-ink"
        >kaneaz-harness</span
      >
      <span
        v-if="showDisclaimer !== false"
        class="font-ui text-xs text-ink-subtle"
        aria-label="Disclaimer"
      >
        Content is user-generated and unverified
      </span>
    </div>

    <div class="flex items-center gap-2">
      <button
        type="button"
        class="rounded-sm px-2 py-1 text-ink-muted hover:text-ink hover:bg-surface-2 transition-fast ease-kenaz"
        :aria-label="`Switch theme (currently ${theme.theme.value})`"
        @click="theme.cycle()"
      >
        <Sun :size="14" />
      </button>
      <button
        type="button"
        class="rounded-sm px-2 py-1 text-ink-muted hover:text-ink hover:bg-surface-2 transition-fast ease-kenaz"
        aria-label="Open command palette (Cmd/Ctrl+K)"
        @click="palette.open()"
      >
        <Command :size="14" />
      </button>
    </div>
  </div>
</template>
