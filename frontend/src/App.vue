<script setup lang="ts">
import { onMounted } from 'vue';
import { useRouter } from 'vue-router';
import Shell from '@/shell/Shell.vue';
import CommandPalette from '@/components/ui/CommandPalette.vue';
import ErrorBoundary from '@/components/ui/ErrorBoundary.vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import { setConnectionState } from '@/lib/useConnectionState';
import { restoreLastRoute, installRouteAuditing } from '@/lib/routing';

const client = useHarnessClient();
const router = useRouter();

onMounted(async () => {
  // First-paint: probe ShellStatus to drive the connection state machine.
  try {
    await client.shellStatus();
    setConnectionState('ready');
  } catch {
    setConnectionState('lost');
  }
  await restoreLastRoute(router, client);
  installRouteAuditing(router, client);
});
</script>

<template>
  <ErrorBoundary>
    <Shell />
    <CommandPalette />
  </ErrorBoundary>
</template>
