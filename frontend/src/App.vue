<script setup lang="ts">
import { onMounted, provide } from 'vue';
import { useRouter } from 'vue-router';
import Shell from '@/shell/Shell.vue';
import CommandPalette from '@/components/ui/CommandPalette.vue';
import ErrorBoundary from '@/components/ui/ErrorBoundary.vue';
import ToastRoot from '@/components/ui/ToastRoot.vue';
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
});
</script>

<template>
  <ErrorBoundary>
    <Shell />
    <CommandPalette />
    <ToastRoot />
  </ErrorBoundary>
</template>
