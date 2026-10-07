<script setup lang="ts">
/**
 * SessionExpiredBanner — fleet session-expired re-auth affordance.
 * (fleet-integrity-observability WP05 / FR-005)
 *
 * Appears when fleet has DEFINITELY rejected the session (refresh token
 * refused). fleet-session-truth-01DOGF0A review F8: this used to be a second
 * reader of fleet state, driven by its own fleet:session:expired event and
 * never cleared except by a click — so it could keep saying "expired" after
 * the session recovered. It now renders the shared fleet-session store:
 * shown while the snapshot is signed_out/session_expired, gone the moment
 * the session is back (sign-in, or the backend's recovery probe).
 *
 * Also the "removed by admin" terminal state (fleet 403 node_removed,
 * device-keys-handoff-01DEVKH01 WP02): same affordance — signing in again
 * registers this install as a new device.
 */
import { computed, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { fleetSession } from '@/lib/fleetSession';

const router = useRouter();

const removed = computed(
  () =>
    fleetSession.value?.state === 'signed_out' &&
    fleetSession.value?.reason === 'node_removed',
);
const expired = computed(
  () =>
    removed.value ||
    (fleetSession.value?.state === 'signed_out' &&
      fleetSession.value?.reason === 'session_expired'),
);
const dismissed = ref(false);
// A new expiry after recovery shows the banner again.
watch(expired, (now) => {
  if (!now) dismissed.value = false;
});
const visible = computed(() => expired.value && !dismissed.value);

function signInAgain() {
  dismissed.value = true;
  void router.push({ path: '/settings', query: { tab: 'account' } });
}

function dismiss() {
  dismissed.value = true;
}
</script>

<template>
  <div
    v-if="visible"
    class="px-4 py-2 bg-signal-warn/10 border-b border-signal-warn flex items-center gap-3"
    role="alert"
    aria-live="polite"
    data-testid="session-expired-banner"
  >
    <span class="font-ui text-sm font-semibold text-signal-warn shrink-0">
      {{ removed ? 'Removed by an org admin' : 'Fleet session expired' }}
    </span>
    <span class="font-ui text-sm text-ink flex-1 truncate" data-testid="session-expired-detail">
      {{
        removed
          ? '— This device was removed from your organization. Sign in again to re-register it.'
          : '— Re-authenticate to restore fleet capabilities.'
      }}
    </span>
    <button
      type="button"
      class="font-ui text-xs text-ink-muted border border-border-muted rounded px-2 py-0.5 hover:bg-surface-2 shrink-0"
      data-testid="session-expired-signin"
      @click="signInAgain"
    >
      Sign in
    </button>
    <button
      type="button"
      class="font-ui text-xs text-ink-muted hover:text-ink shrink-0"
      aria-label="Dismiss"
      data-testid="session-expired-dismiss"
      @click="dismiss"
    >
      ✕
    </button>
  </div>
</template>
