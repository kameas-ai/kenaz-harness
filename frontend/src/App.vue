<script setup lang="ts">
import { onMounted, provide, ref } from 'vue';
import { useRouter } from 'vue-router';
import Shell from '@/shell/Shell.vue';
import CommandPalette from '@/components/ui/CommandPalette.vue';
import ErrorBoundary from '@/components/ui/ErrorBoundary.vue';
import ToastRoot from '@/components/ui/ToastRoot.vue';
import OnboardingDialog from '@/views/onboarding/OnboardingDialog.vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import { setConnectionState } from '@/lib/useConnectionState';
import { restoreLastRoute, installRouteAuditing } from '@/lib/routing';
import {
  MD_EXTENSIONS_KEY,
  markdownExtensionsRef,
} from '@/lib/markdown/injectionKeys';

const client = useHarnessClient();
const router = useRouter();

// Markdown extensions dial — hydrated from settings, defaults to 'all'.
// Provided as a ref so the SettingsView write path can update the
// projection live (subsequent MarkdownBlock renders pick up the change).
provide(MD_EXTENSIONS_KEY, markdownExtensionsRef);

// Onboarding dialog — mounts when the API reports firstRun = true.
// (harness-self-mcp-onboarding-01KQ8TDU WP08)
const onboardingOpen = ref(false);

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

  // Onboarding first-run dialog is DISABLED pending reimplementation as a
  // separate window. The in-app modal greyed out + trapped the whole window
  // with no working exit (Get started → starter error, Just chat → grey-out).
  // Do NOT auto-open it here; it will return as a separate OS window.
  // (see task: reimplement onboarding "Get started" as a separate window)
  //   const state = await client.onboarding.state();
  //   if (state.firstRun) onboardingOpen.value = true;
});
</script>

<template>
  <ErrorBoundary>
    <Shell />
    <CommandPalette />
    <ToastRoot />
    <OnboardingDialog
      :open="onboardingOpen"
      @close="onboardingOpen = false"
      @navigate-to-session="onOnboardingNavigate"
    />
  </ErrorBoundary>
</template>
