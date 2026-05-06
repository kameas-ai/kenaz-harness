<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref } from 'vue';
import Titlebar from './Titlebar.vue';
import Toolbar from './Toolbar.vue';
import LeftRail from './LeftRail.vue';
import LegendBar from './LegendBar.vue';
import ConnectionLostBanner from '@/components/ui/ConnectionLostBanner.vue';
import SearchModal from '@/components/search/SearchModal.vue';
import SearchPalette from '@/components/search/SearchPalette.vue';
import CheatSheetModal from '@/components/shortcuts/CheatSheetModal.vue';
import { useConnectionState } from '@/lib/useConnectionState';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { matchesEvent } from '@/lib/shortcuts/platform';
import { useSearchPalette } from '@/lib/useSearchPalette';

/**
 * Shell — the persistent app-level layout (plan §2.1).
 *
 * 3-region grid: Titlebar / [LeftRail | main]. The main column hosts
 * Toolbar + <router-view> wrapped in <KeepAlive> for per-session UI
 * state (FR-002) + LegendBar.
 *
 * The first-paint state machine (plan §4.1, FR-017) renders a quiet
 * "starting…" surface while connecting. ConnectionLostBanner appears
 * only when the bridge is lost (FR-013) — not as a toast wall.
 *
 * Cmd-F (Mac) / Ctrl-F (other) opens the SearchModal. The binding
 * short-circuits when the event target is an INPUT or TEXTAREA so the
 * browser / chat composer shortcut is not stolen.
 * Also registers the global `?` / Cmd-/ shortcut for the keyboard
 * cheat-sheet overlay (keyboard-shortcuts-settings-01KQ8TDR plan §2.6).
 * Mirrors the Cmd-F pattern in useCommandPalette.
 */
const connection = useConnectionState();
const isStarting = computed(() => connection.value === 'connecting');
const isLost = computed(() => connection.value === 'lost');

const searchOpen = ref(false);
const client = useHarnessClient();
const cheatSheetOpen = ref(false);
const shortcutOverrides = ref<Record<string, string>>({});
const searchPalette = useSearchPalette();

function onGlobalKeydown(e: KeyboardEvent) {
  const target = e.target as HTMLElement | null;
  const tag = target?.tagName?.toUpperCase();
  const isEditable =
    tag === 'INPUT' ||
    tag === 'TEXTAREA' ||
    (target?.isContentEditable ?? false);

  // ⌘K / Ctrl+K → toggle search palette.
  // Fire even when an input is focused if the palette is already open
  // (so the user can dismiss with ⌘K from inside the palette input).
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
    if (isEditable && !searchPalette.isOpen.value) return;
    e.preventDefault();
    searchPalette.toggle();
    return;
  }

  if (isEditable) return;

  // Cmd-F / Ctrl-F → search modal (legacy full-page surface)
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'f') {
    e.preventDefault();
    searchOpen.value = true;
    return;
  }
  // ? → cheat-sheet overlay
  if (matchesEvent('?', e)) {
    e.preventDefault();
    cheatSheetOpen.value = !cheatSheetOpen.value;
    return;
  }
  if (e.key === 'Escape' && cheatSheetOpen.value) {
    cheatSheetOpen.value = false;
  }
}

onMounted(async () => {
  if (typeof window !== 'undefined') {
    window.addEventListener('keydown', onGlobalKeydown);
  }
  try {
    const s = await client.settings.get();
    shortcutOverrides.value = s.keyboardShortcuts ?? {};
  } catch {
    // best-effort; cheat sheet falls back to defaults
  }
});

onBeforeUnmount(() => {
  if (typeof window !== 'undefined') {
    window.removeEventListener('keydown', onGlobalKeydown);
  }
});
</script>

<template>
  <div class="shell-grid bg-surface-0 text-ink font-ui">
    <header class="shell-titlebar border-b border-border-muted bg-surface-1">
      <Titlebar />
    </header>

    <aside
      class="shell-rail border-r border-border-muted bg-surface-1"
      aria-label="Primary navigation"
    >
      <LeftRail />
    </aside>

    <main class="shell-main bg-surface-0">
      <div class="shell-toolbar border-b border-border-muted bg-surface-1">
        <Toolbar />
      </div>

      <div class="shell-canvas" role="region" aria-label="Active surface">
        <ConnectionLostBanner v-if="isLost" />
        <div
          v-if="isStarting"
          class="grid place-items-center h-full text-ink-subtle font-mono text-sm"
        >
          starting…
        </div>
        <router-view v-else v-slot="{ Component }">
          <KeepAlive>
            <component :is="Component" />
          </KeepAlive>
        </router-view>
      </div>

      <div class="shell-legend border-t border-border-muted bg-surface-1">
        <LegendBar />
      </div>
    </main>
  </div>

  <!-- Search palette — floating ⌘K overlay (v0.5.6, search-palette-relocation).
       Rendered as a portal sibling so it sits above all z-index layers.
       Future: unified-search-01KX5R8C will expand to cross-entity results. -->
  <SearchPalette />
  <!-- Search modal — legacy Cmd-F full-page surface. Kept as the advanced
       search entry point; the ⌘K palette is the primary entry point. -->
  <SearchModal v-if="searchOpen" @close="searchOpen = false" />
  <!-- Global overlays -->
  <CheatSheetModal
    :open="cheatSheetOpen"
    :overrides="shortcutOverrides"
    @close="cheatSheetOpen = false"
  />
</template>
