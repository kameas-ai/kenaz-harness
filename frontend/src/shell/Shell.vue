<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref } from 'vue';
import Titlebar from './Titlebar.vue';
import Toolbar from './Toolbar.vue';
import LeftRail from './LeftRail.vue';
import LegendBar from './LegendBar.vue';
import ConnectionLostBanner from '@/components/ui/ConnectionLostBanner.vue';
import SearchModal from '@/components/search/SearchModal.vue';
import CheatSheetModal from '@/components/shortcuts/CheatSheetModal.vue';
import { useConnectionState } from '@/lib/useConnectionState';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { matchesEvent } from '@/lib/shortcuts/platform';

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

function onGlobalKeydown(e: KeyboardEvent) {
  const target = e.target as HTMLElement | null;
  const tag = target?.tagName?.toUpperCase();
  const isEditable =
    tag === 'INPUT' ||
    tag === 'TEXTAREA' ||
    (target?.isContentEditable ?? false);
  if (isEditable) return;

  // Cmd-F / Ctrl-F → search modal
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

  <!-- Search modal — rendered as a portal sibling to the shell grid so
       it sits above all z-index layers without clipping. -->
  <SearchModal v-if="searchOpen" @close="searchOpen = false" />
  <!-- Global overlays -->
  <CheatSheetModal
    :open="cheatSheetOpen"
    :overrides="shortcutOverrides"
    @close="cheatSheetOpen = false"
  />
</template>
