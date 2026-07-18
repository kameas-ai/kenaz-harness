<script setup lang="ts">
import { onMounted, provide, ref } from 'vue';
import { useRouter } from 'vue-router';
import Shell from '@/shell/Shell.vue';
import CommandPalette from '@/components/ui/CommandPalette.vue';
import ToastRoot from '@/components/ui/ToastRoot.vue';
import OnboardingDialog from '@/views/onboarding/OnboardingDialog.vue';
import TelemetryOnboardingModal from '@/components/onboarding/TelemetryOnboardingModal.vue';
import AboutDialog from '@/components/about/AboutDialog.vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import { setConnectionState } from '@/lib/useConnectionState';
import { restoreLastRoute, installRouteAuditing } from '@/lib/routing';
import {
  MD_EXTENSIONS_KEY,
  markdownExtensionsRef,
} from '@/lib/markdown/injectionKeys';
import { useAboutDialogStore } from '@/lib/aboutDialogStore';
import { useSearchPalette } from '@/lib/useSearchPalette';
import { useCommandPalette } from '@/lib/useCommandPalette';
import { useTheme } from '@/lib/useTheme';
import { useEventStream } from '@/lib/useEventStream';

const client = useHarnessClient();
const router = useRouter();

// Markdown extensions dial — hydrated from settings, defaults to 'all'.
// Provided as a ref so the SettingsView write path can update the
// projection live (subsequent MarkdownBlock renders pick up the change).
provide(MD_EXTENSIONS_KEY, markdownExtensionsRef);

// Onboarding dialog — mounts when the API reports firstRun = true.
// (harness-self-mcp-onboarding-01KQ8TDU WP08)
const onboardingOpen = ref(false);

// Fleet telemetry onboarding modal — shown once on first launch after the user
// has not yet seen it. Wires to the existing consent backend.
// (fleet-otel-archival-01NDFSEX11 WP06)
const telemetryOnboardingOpen = ref(false);

// About dialog — opened by the OS menu bar "About" item.
// (os-menu-bar-01NDFSEX16 WP05)
const aboutStore = useAboutDialogStore();
const searchPalette = useSearchPalette();
const cmdPalette = useCommandPalette();
const theme = useTheme();

// Menu broker event subscribers (os-menu-bar-01NDFSEX16 WP05).
// Each topic published by core/menu/handlers.go is dispatched here to the
// appropriate frontend composable so the menu bar actions land correctly.
//
// The single-subscriber pattern (all menu events in App.vue) keeps the
// dispatch table in one place and avoids multiple listeners per topic.
useEventStream<null>('menu:about:open', () => {
  aboutStore.open();
});

useEventStream<null>('menu:search:open', () => {
  searchPalette.open();
});

useEventStream<null>('menu:cmd-palette:open', () => {
  cmdPalette.open();
});

interface ThemePayload { mode: 'light' | 'dark' | 'system' }
useEventStream<ThemePayload>('menu:theme:set', (payload) => {
  if (payload?.mode) {
    theme.set(payload.mode);
  }
});

interface RoutePayload { path: string }
useEventStream<RoutePayload>('menu:route', (payload) => {
  if (payload?.path) {
    void router.push(payload.path);
  }
});

useEventStream<null>('menu:cheat-sheet:toggle', () => {
  // The cheat-sheet toggle is handled by Shell.vue's keydown handler
  // via the '?' shortcut. Re-emit as a keyboard event so the existing
  // handler fires without duplicating the logic here.
  const evt = new KeyboardEvent('keydown', { key: '?', bubbles: true });
  document.dispatchEvent(evt);
});

function onOnboardingNavigate(sessionId: string) {
  if (sessionId) {
    void router.push(`/sessions/${sessionId}`);
  }
}

onMounted(async () => {
  // First-paint: probe ShellStatus to drive the connection state machine.
  try {
    await client.shellStatus();
    setConnectionState('ready');
  } catch {
    setConnectionState('lost');
  }
  // Hydrate markdown extensions from persisted settings.
  try {
    const s = await client.settings.get();
    if (s.markdownExtensions) markdownExtensionsRef.value = s.markdownExtensions;
  } catch {
    // Keep default 'all' on error.
  }
  await restoreLastRoute(router, client);
  installRouteAuditing(router, client);

  // WP05: re-enabled onboarding first-run dialog via BaseDialog (Escape + focus
  // trap now work; the grey-out trap from the old overlay is fixed). The dialog
  // has a working "Skip" / "Dismiss" exit so the user can always leave.
  try {
    const state = await client.onboarding.state();
    if (state.firstRun) onboardingOpen.value = true;
  } catch {
    // Best-effort: if the RPC fails, skip onboarding.
  }
  // Fleet telemetry onboarding modal — shown once when not yet seen.
  // (fleet-otel-archival-01NDFSEX11 WP06)
  try {
    const s = await client.settings.get();
    if (!s.hasSeenFleetTelemetryOnboarding) telemetryOnboardingOpen.value = true;
  } catch {
    // Best-effort: if the RPC fails, skip the modal.
  }
});
</script>

<template>
  <Shell />
  <CommandPalette />
  <ToastRoot />
  <OnboardingDialog
    :open="onboardingOpen"
    @close="onboardingOpen = false"
    @navigate-to-session="onOnboardingNavigate"
  />
  <!-- Fleet telemetry onboarding modal (fleet-otel-archival-01NDFSEX11 WP06) -->
  <TelemetryOnboardingModal
    v-if="telemetryOnboardingOpen"
    @close="telemetryOnboardingOpen = false"
  />
  <!-- About dialog — opened by OS menu bar "About" item (menu:about:open event) -->
  <AboutDialog
    :open="aboutStore.isOpen.value"
    @update:open="(v) => (v ? aboutStore.open() : aboutStore.close())"
  />
</template>
