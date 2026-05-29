<script setup lang="ts">
/**
 * LockdownBanner — fleet-issued emergency lockdown notification.
 * (fleet-emergency-lockdown-01NDFSEX12 WP05)
 *
 * Mounted once in Shell.vue above the main canvas. When a fleet admin
 * issues a lockdown the banner appears immediately and the chat composer
 * is disabled. The banner cannot be dismissed — it clears automatically
 * when the lockdown is lifted.
 *
 * State is seeded on mount from Settings_FleetLockdownStatus (handles the
 * boot-into-locked-state case) and kept live via the fleet:lockdown:changed
 * broker event.
 */

import { ref, onMounted } from 'vue';
import { useEventStream } from '@/lib/useEventStream';
import { useHarnessClient } from '@/lib/useHarnessAPI';

interface LockdownChangedPayload {
  active: boolean;
  reason?: string;
}

const client = useHarnessClient();

const active = ref(false);
const reason = ref('');

// Subscribe to live state changes from the Watcher.
useEventStream<LockdownChangedPayload>('fleet:lockdown:changed', (payload) => {
  active.value = payload.active;
  reason.value = payload.reason ?? '';
});

// Seed from the server on mount so we handle boot-into-locked-state correctly.
onMounted(async () => {
  try {
    const status = await client.settings.fleetLockdownStatus();
    active.value = status.active;
    reason.value = status.reason ?? '';
  } catch {
    // Best-effort: if the RPC fails, the Watcher event will catch up.
  }
});
</script>

<template>
  <div
    v-if="active"
    class="px-4 py-2 bg-signal-danger/10 border-b border-signal-danger flex items-center gap-3"
    role="alert"
    aria-live="assertive"
    data-testid="lockdown-banner"
  >
    <span class="font-ui text-sm font-semibold text-signal-danger shrink-0">
      Emergency lockdown active
    </span>
    <span
      v-if="reason"
      class="font-ui text-sm text-ink truncate"
      data-testid="lockdown-banner-reason"
    >
      — {{ reason }}
    </span>
    <span
      v-else
      class="font-ui text-sm text-ink-subtle"
    >
      — Admin has suspended all AI actions. Contact your administrator.
    </span>
  </div>
</template>
