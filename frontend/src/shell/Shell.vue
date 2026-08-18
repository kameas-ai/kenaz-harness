<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref } from 'vue';
import Titlebar from './Titlebar.vue';
import Toolbar from './Toolbar.vue';
import LeftRail from './LeftRail.vue';
import LegendBar from './LegendBar.vue';
import ErrorBoundary from '@/components/ui/ErrorBoundary.vue';
import ConnectionLostBanner from '@/components/ui/ConnectionLostBanner.vue';
import LockdownBanner from '@/components/ui/LockdownBanner.vue';
import SessionExpiredBanner from '@/components/ui/SessionExpiredBanner.vue';
import BootHealthBanner from '@/components/ui/BootHealthBanner.vue';
import SearchModal from '@/components/search/SearchModal.vue';
import SearchPalette from '@/components/search/SearchPalette.vue';
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
 *
 * ⌘K is deliberately NOT bound here — it is owned solely by
 * useCommandPalette.ts's own `window` keydown listener (engineer-truth-pass-
 * 01PMTP01 WP01). Before that fix this file also toggled the search palette
 * on ⌘K, so one keypress opened both the command palette and the search
 * palette on top of each other. This now matches the OS menu accelerators
 * (core/menu/menu.go: View → Command Palette = ⌘K, View → Search = ⌘F).
 */
const connection = useConnectionState();
const isStarting = computed(() => connection.value === 'connecting');
const isLost = computed(() => connection.value === 'lost');

const searchOpen = ref(false);
const client = useHarnessClient();
const cheatSheetOpen = ref(false);
const shortcutOverrides = ref<Record<string, string>>({});

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

  // ⌘K is NOT handled here. It belongs solely to useCommandPalette.ts's own
  // `window` keydown listener (installed when CommandPalette.vue mounts).
  // Until WP01 of engineer-truth-pass-01PMTP01, this handler also toggled
  // the search palette on ⌘K, so both palettes opened on one keypress and
  // painted on top of each other (equal z-50, CommandPalette later in the
  // DOM). The search palette is still reachable: the OS menu bar's
  // View → Search item (⌘F, core/menu/menu.go) publishes `menu:search:open`,
  // which App.vue turns into searchPalette.open(). That is its ONLY entry
  // point today — UserMenu.vue's Search row moved to the native menu in
  // v0.20.0, so there is no Titlebar/UserMenu affordance to name.

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
        <!-- fleet-integrity-observability WP05: session-expired re-auth affordance. -->
        <SessionExpiredBanner />
        <!-- agent-loop-robustness-parity WP08: boot-health init-error banner. -->
        <BootHealthBanner />
        <ConnectionLostBanner v-if="isLost" />
        <div
          v-if="isStarting"
          class="grid place-items-center h-full text-ink-subtle font-mono text-sm"
        >
          starting…
        </div>
        <!-- ErrorBoundary is scoped to the surface content region only (FR-001):
             the LeftRail and window chrome stay mounted and interactive when a
             surface throws. Navigating via the rail clears the error (FR-002). -->
        <ErrorBoundary v-else>
          <router-view v-slot="{ Component }">
            <KeepAlive>
              <component :is="Component" />
            </KeepAlive>
          </router-view>
        </ErrorBoundary>
      </div>

      <div class="shell-legend border-t border-border-muted bg-surface-1">
        <LegendBar />
      </div>
    </main>
  </div>

  <!-- Search palette — floating overlay (v0.5.6, search-palette-relocation).
       Opened ONLY by the `menu:search:open` OS-menu action (View → Search,
       ⌘F — see App.vue and core/menu/menu.go), not by ⌘K
       (engineer-truth-pass-01PMTP01 WP01: ⌘K now belongs solely to the
       command palette, see useCommandPalette.ts).
       Rendered as a portal sibling so it sits above all z-index layers.
       Future: unified-search-01KX5R8C will expand to cross-entity results. -->
  <SearchPalette />
  <!-- Search modal — legacy Cmd-F full-page surface. Kept as the advanced
       search entry point; ⌘K opens the command palette (CommandPalette.vue),
       not this. -->
  <SearchModal v-if="searchOpen" @close="searchOpen = false" />
  <!-- Global overlays -->
  <CheatSheetModal
    :open="cheatSheetOpen"
    :overrides="shortcutOverrides"
    @close="cheatSheetOpen = false"
  />
</template>
