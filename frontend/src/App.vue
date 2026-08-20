<script setup lang="ts">
import { onMounted, provide, ref } from 'vue';
import { useRouter } from 'vue-router';
import Shell from '@/shell/Shell.vue';
import CommandPalette from '@/components/ui/CommandPalette.vue';
import ToastRoot from '@/components/ui/ToastRoot.vue';
import OnboardingDialog from '@/views/onboarding/OnboardingDialog.vue';
import TelemetryOnboardingModal from '@/components/onboarding/TelemetryOnboardingModal.vue';
import AboutDialog from '@/components/about/AboutDialog.vue';
import AskUserQuestion from '@/components/dialogs/AskUserQuestion/AskUserQuestion.vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import { setConnectionState } from '@/lib/useConnectionState';
import { restoreLastRoute, installRouteAuditing } from '@/lib/routing';
import { logEvent } from '@/lib/eventLog';
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

// Resolved fleet subscription tier for TelemetryOnboardingModal's
// entitlement gate (A10 / consent-surfaces-truth-01PMTR01 WP02).
//
// null = "unresolved" — capabilities haven't been fetched yet, or the
//   fetch rejected. The modal must treat this as fail-ungated (no gate,
//   no badge): degrading toward FleetTelemetryPanel's sibling behaviour
//   (which enforces nothing), not toward "no tier" (which would show a
//   false "Requires Pro+" claim to a user we simply couldn't identify).
// '' = "resolved absent" — fleetCapabilities() answered and the account
//   genuinely has no tier. This DOES gate.
const accountTier = ref<'pro' | 'team' | 'enterprise' | '' | null>(null);

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
  } catch (err) {
    // entry-points-and-crash-reporting-01PMZD13 UNIT-9: a boot failure that
    // reports nothing is a crash report that never happens. Fallback
    // behaviour is unchanged (still sets 'lost') — this only makes the
    // failure visible.
    logEvent('warn', 'app.boot.shell_status_failed', {
      message: err instanceof Error ? err.message : String(err),
    });
    setConnectionState('lost');
  }
  // Hydrate markdown extensions + the fleet-telemetry-onboarding flag from
  // persisted settings. UNIT-9: hoisted to ONE client.settings.get() call —
  // previously fetched twice, twenty lines apart, for two unrelated fields,
  // while main.ts's boot comment boasts "exactly one AppInfo RPC" a few
  // lines away from the RPC this duplicated.
  let settingsSnapshot: Awaited<ReturnType<typeof client.settings.get>> | null = null;
  try {
    settingsSnapshot = await client.settings.get();
    if (settingsSnapshot.markdownExtensions) {
      markdownExtensionsRef.value = settingsSnapshot.markdownExtensions;
    }
  } catch (err) {
    // Keep default 'all' on error — unchanged fallback, now logged.
    logEvent('warn', 'app.boot.settings_get_failed', {
      message: err instanceof Error ? err.message : String(err),
    });
  }
  await restoreLastRoute(router, client);
  installRouteAuditing(router, client);

  // WP05: re-enabled onboarding first-run dialog via BaseDialog (Escape + focus
  // trap now work; the grey-out trap from the old overlay is fixed). The dialog
  // has a working "Skip" / "Dismiss" exit so the user can always leave.
  try {
    const state = await client.onboarding.state();
    if (state.firstRun) onboardingOpen.value = true;
  } catch (err) {
    // Best-effort: if the RPC fails, skip onboarding — unchanged fallback,
    // now logged. A persistently failing onboarding.state() previously
    // suppressed the first-run dialog forever with no signal anywhere.
    logEvent('warn', 'app.boot.onboarding_state_failed', {
      message: err instanceof Error ? err.message : String(err),
    });
  }
  // Fleet telemetry onboarding modal — shown once when not yet seen.
  // (fleet-otel-archival-01NDFSEX11 WP06)
  if (settingsSnapshot) {
    if (!settingsSnapshot.hasSeenFleetTelemetryOnboarding) {
      telemetryOnboardingOpen.value = true;
    }
  } else {
    // settingsSnapshot is only null when the hoisted fetch above already
    // failed and logged app.boot.settings_get_failed — best-effort: skip
    // the modal rather than re-fetching (which would just fail again on
    // the same underlying condition).
    logEvent('warn', 'app.boot.telemetry_onboarding_skipped_no_settings', {});
  }
  // Resolve the account tier for the telemetry modal's entitlement gate
  // (A10 / consent-surfaces-truth-01PMTR01 WP02). Runs independently of
  // the open/closed decision above so the tier is populated (or
  // explicitly left unresolved) by the time the user reaches the
  // gated radios, whether the modal was already open or opens later.
  try {
    const caps = await client.settings.fleetCapabilities();
    const known = new Set(['pro', 'team', 'enterprise', '']);
    accountTier.value = known.has(caps.tier)
      ? (caps.tier as 'pro' | 'team' | 'enterprise' | '')
      : '';
  } catch {
    // Fail-ungated (spec §5): leave accountTier at its null "unresolved"
    // default rather than falling through to '' — a rejected capability
    // fetch must not render "Requires Pro+" to a user we couldn't
    // identify.
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
    :account-tier="accountTier"
    @close="telemetryOnboardingOpen = false"
  />
  <!-- Elicitation dialog for kenaz__ask_user_question.

       Mounted here, app-wide, because the thing it answers is a BLOCKED
       GOROUTINE: the tool is default-on and its Delegate is the real
       elicit bridge, so a model that asks a question parks the turn
       inside elicitview.OpenDialog for up to ten minutes and only
       client.elicit.submitAnswer releases it. This component owned that
       return leg and was never imported by anything, so the pause had no
       answering surface and every question the model asked ran out the
       clock. Mounting it at App level (not inside a session view) means a
       question raised by a background session is still answerable from
       wherever the user happens to be — the same reasoning that puts
       ConfirmToolModal outside the chat surface. -->
  <AskUserQuestion />
  <!-- About dialog — opened by OS menu bar "About" item (menu:about:open event) -->
  <AboutDialog
    :open="aboutStore.isOpen.value"
    @update:open="(v) => (v ? aboutStore.open() : aboutStore.close())"
  />
</template>
