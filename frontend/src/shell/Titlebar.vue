<script setup lang="ts">
import { Sun } from './icons';
import { useCommandPalette } from '@/lib/useCommandPalette';
import { useSearchPalette } from '@/lib/useSearchPalette';
import { useTheme } from '@/lib/useTheme';
import UpdateIndicator from '@/components/updates/UpdateIndicator.vue';

defineProps<{
  /** Per-surface override; defaults to true so AI-output disclaimer is
   *  visible on every surface unless explicitly suppressed (FR-001h). */
  showDisclaimer?: boolean;
}>();

const palette = useCommandPalette();
const searchPalette = useSearchPalette();
const theme = useTheme();
</script>

<template>
  <div class="flex items-center justify-between pl-[88px] pr-4 py-2 select-none">
    <div class="flex items-center gap-3">
      <span
        v-if="showDisclaimer !== false"
        class="font-ui text-[11px] text-ink-subtle"
        aria-label="Disclaimer"
      >
        Content is user-generated and unverified.
      </span>
    </div>

    <div class="flex items-center gap-1">
      <!-- WP04 auto-update indicator — hidden when no update is offered.
           Sits to the left of the theme toggle so the user's eye finds it
           in the same Chrome-style chrome region as the menu / settings. -->
      <UpdateIndicator />
      <!-- Search icon — opens the floating ⌘K search palette (v0.5.6).
           Future: unified-search-01KX5R8C will expand to cross-entity results. -->
      <button
        type="button"
        class="rounded-sm px-2 py-1 text-ink-muted hover:text-ink hover:bg-surface-2 transition-fast ease-kenaz"
        aria-label="Search"
        title="Search ⌘K"
        data-testid="search-palette-trigger"
        @click="searchPalette.open()"
      >
        <svg
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          aria-hidden="true"
        >
          <circle cx="11" cy="11" r="8" />
          <path d="m21 21-4.35-4.35" />
        </svg>
      </button>
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
        class="rounded-sm px-2 py-1 font-mono text-[11px] text-ink-muted hover:text-ink hover:bg-surface-2 transition-fast ease-kenaz"
        aria-label="Open command palette (Cmd/Ctrl+K)"
        @click="palette.open()"
      >
        ⌘K
      </button>
    </div>
  </div>
</template>
