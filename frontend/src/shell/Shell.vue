<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref } from 'vue';
import Titlebar from './Titlebar.vue';
import Toolbar from './Toolbar.vue';
import LeftRail from './LeftRail.vue';
import LegendBar from './LegendBar.vue';
import ConnectionLostBanner from '@/components/ui/ConnectionLostBanner.vue';
import LockdownBanner from '@/components/ui/LockdownBanner.vue';
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

// ── Resizable left rail ───────────────────────────────────────────────
// Width is user-dragged and persisted to localStorage (pure UI chrome, no
// backend setting). The grid reads it via the --rail-width custom property;
// below the 960px breakpoint shell.css forces the 56px collapsed rail and
// the handle is hidden.
const RAIL_MIN = 200;
const RAIL_MAX = 480;
const RAIL_DEFAULT = 240;
const RAIL_STORAGE_KEY = 'kenaz.railWidth';

function clampRail(px: number): number {
  if (!Number.isFinite(px)) return RAIL_DEFAULT;
  return Math.min(RAIL_MAX, Math.max(RAIL_MIN, Math.round(px)));
}

const railWidth = ref(RAIL_DEFAULT);
const railResizing = ref(false);

function loadRailWidth() {
  if (typeof window === 'undefined') return;
  const raw = window.localStorage?.getItem(RAIL_STORAGE_KEY);
  if (raw) railWidth.value = clampRail(Number(raw));
}

function onRailHandlePointerDown(e: PointerEvent) {
  railResizing.value = true;
  (e.target as HTMLElement).setPointerCapture?.(e.pointerId);
  e.preventDefault();
}

function onRailHandlePointerMove(e: PointerEvent) {
  if (!railResizing.value) return;
  // The rail's left edge is at viewport x=0, so clientX is the width.
  railWidth.value = clampRail(e.clientX);
}

function onRailHandlePointerUp(e: PointerEvent) {
  if (!railResizing.value) return;
  railResizing.value = false;
  (e.target as HTMLElement).releasePointerCapture?.(e.pointerId);
  try {
    window.localStorage?.setItem(RAIL_STORAGE_KEY, String(railWidth.value));
  } catch {
    // best-effort; non-persisted width is still applied for the session
  }
}

function resetRailWidth() {
  railWidth.value = RAIL_DEFAULT;
  try {
    window.localStorage?.setItem(RAIL_STORAGE_KEY, String(RAIL_DEFAULT));
  } catch {
    /* ignore */
  }
}

function onRailHandleKeydown(e: KeyboardEvent) {
  if (e.key === 'ArrowLeft') {
    railWidth.value = clampRail(railWidth.value - 16);
  } else if (e.key === 'ArrowRight') {
    railWidth.value = clampRail(railWidth.value + 16);
  } else {
    return;
  }
  e.preventDefault();
  try {
    window.localStorage?.setItem(RAIL_STORAGE_KEY, String(railWidth.value));
  } catch {
    /* ignore */
  }
}

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
  loadRailWidth();
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
  <!-- Skip-to-content link: visible on keyboard focus, hidden otherwise.
       Allows keyboard-only users to bypass the nav rail and jump to the
       main content area (WCAG 2.4.1 Bypass Blocks). -->
  <a
    href="#shell-canvas"
    class="skip-to-content"
  >Skip to content</a>

  <div
    class="shell-grid bg-surface-0 text-ink font-ui"
    :style="{ '--rail-width': railWidth + 'px' }"
  >
    <header class="shell-titlebar border-b border-border-muted bg-surface-1">
      <Titlebar />
    </header>

    <aside
      class="shell-rail border-r border-border-muted bg-surface-1"
      aria-label="Primary navigation"
    >
      <LeftRail />
      <!-- Drag handle to resize the rail. Hidden below the 960px breakpoint
           where the rail collapses to a fixed icon strip. -->
      <div
        class="hidden two-col:block absolute top-0 right-0 z-20 h-full w-1.5 -mr-0.5 cursor-col-resize hover:bg-accent-hairline"
        :class="railResizing ? 'bg-accent-hairline' : ''"
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize sidebar"
        :aria-valuenow="railWidth"
        :aria-valuemin="RAIL_MIN"
        :aria-valuemax="RAIL_MAX"
        tabindex="0"
        data-testid="rail-resize-handle"
        @pointerdown="onRailHandlePointerDown"
        @pointermove="onRailHandlePointerMove"
        @pointerup="onRailHandlePointerUp"
        @dblclick="resetRailWidth"
        @keydown="onRailHandleKeydown"
      />
    </aside>

    <main class="shell-main bg-surface-0">
      <div class="shell-toolbar border-b border-border-muted bg-surface-1">
        <Toolbar />
      </div>

      <div id="shell-canvas" class="shell-canvas" role="region" aria-label="Active surface">
        <!-- fleet-emergency-lockdown-01NDFSEX12 WP05: persistent lockdown banner. -->
        <LockdownBanner />
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
