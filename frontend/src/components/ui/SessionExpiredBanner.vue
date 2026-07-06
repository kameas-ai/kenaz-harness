<script setup lang="ts">
/**
 * SessionExpiredBanner — fleet session-expired re-auth affordance.
 * (fleet-integrity-observability WP05 / FR-005)
 *
 * Appears when the fleet token refresh fails (access token expired +
 * refresh exchange failed). The user must sign in again to restore
 * fleet capabilities (config bundles, team policy, sync).
 *
 * State is driven by the fleet:session:expired broker event. The banner
 * persists until the user signs in via the Account panel.
 */
import { ref } from 'vue';
import { useRouter } from 'vue-router';
import { useEventStream } from '@/lib/useEventStream';

interface SessionExpiredPayload {
  reason?: string;
}

const router = useRouter();

const visible = ref(false);
const reason = ref('');

useEventStream<SessionExpiredPayload>('fleet:session:expired', (payload) => {
  visible.value = true;
  reason.value = payload.reason ?? '';
});

function signInAgain() {
  visible.value = false;
  void router.push({ path: '/settings', query: { tab: 'account' } });
}

function dismiss() {
  visible.value = false;
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
      Fleet session expired
    </span>
    <span class="font-ui text-sm text-ink flex-1 truncate">
      — Re-authenticate to restore fleet capabilities.
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
