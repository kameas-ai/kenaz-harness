<script setup lang="ts">
/**
 * DeliveryBanner — the composer-side half of the NOT DELIVERED state
 * (undelivered-message-retry, owner dogfood 2026-10-07).
 *
 * The draft stays clear: the message IS persisted, and retyping it is
 * exactly the duplicate-row workaround this replaces. Instead the banner
 * says why the last message did not reach the model and offers the two
 * moves that fix it — Retry (re-runs the same message) and, for a key or
 * permission problem, Open settings; for a request too large for the
 * model, Open tools (the composer Tools menu, where tools are unloaded).
 * While a transient failure waits for its automatic retry it shows the
 * countdown state and a Cancel.
 */
import { computed } from 'vue';
import {
  deliveryCopy,
  needsSettings,
  offersToolsMenu,
  type DeliveryFailure,
  type RequestSizeContext,
} from '@/lib/delivery';
import type { AutoRetryState } from '@/lib/useSession';

const props = withDefaults(
  defineProps<{
    failure: DeliveryFailure;
    autoRetry?: AutoRetryState | null;
    /** Hide "Open settings" where the provider form cannot be used (served). */
    settingsAvailable?: boolean;
    /** Tool tokens and model window for the request_too_large remedy. */
    sizeContext?: RequestSizeContext | null;
    /** Hide "Open tools" where the Tools menu cannot act (served). */
    toolsAvailable?: boolean;
  }>(),
  { autoRetry: null, settingsAvailable: true, sizeContext: null, toolsAvailable: true },
);

const emit = defineEmits<{
  (e: 'retry'): void;
  (e: 'cancel-retry'): void;
  (e: 'open-settings'): void;
  (e: 'open-tools'): void;
}>();

const copy = computed(() => deliveryCopy(props.failure, props.sizeContext ?? undefined));
const showTools = computed(() => props.toolsAvailable && offersToolsMenu(props.failure));
const showSettings = computed(
  () => props.settingsAvailable && needsSettings(props.failure),
);
</script>

<template>
  <div
    role="alert"
    data-testid="delivery-banner"
    class="flex items-start gap-2 rounded-md border border-signal-danger/30 bg-signal-danger/10 px-3 py-2 font-ui text-[12px]"
  >
    <div class="flex-1 min-w-0">
      <p class="leading-snug text-signal-danger" data-testid="delivery-banner-reason">
        {{ copy }}
      </p>
      <p
        v-if="autoRetry"
        class="mt-0.5 text-ink-muted"
        data-testid="delivery-banner-auto-retry"
      >
        Retrying ({{ autoRetry.attempt }}/{{ autoRetry.max }}) in
        {{ Math.round(autoRetry.delayMs / 1000) }}s…
      </p>
    </div>
    <button
      v-if="autoRetry"
      type="button"
      class="shrink-0 px-2 py-0.5 rounded-md border border-border-muted text-ink-muted hover:bg-surface-2"
      data-testid="delivery-banner-cancel"
      @click="emit('cancel-retry')"
    >
      Cancel
    </button>
    <template v-else>
      <button
        v-if="showSettings"
        type="button"
        class="shrink-0 px-2 py-0.5 rounded-md border border-border-muted text-ink hover:bg-surface-2"
        data-testid="delivery-banner-settings"
        @click="emit('open-settings')"
      >
        Open settings
      </button>
      <button
        v-if="showTools"
        type="button"
        class="shrink-0 px-2 py-0.5 rounded-md border border-border-muted text-ink hover:bg-surface-2"
        data-testid="delivery-banner-tools"
        @click="emit('open-tools')"
      >
        Open tools
      </button>
      <button
        type="button"
        class="shrink-0 px-2 py-0.5 rounded-md border border-accent text-accent uppercase tracking-[0.18em] text-[10px] hover:bg-surface-2"
        data-testid="delivery-banner-retry"
        @click="emit('retry')"
      >
        Retry
      </button>
    </template>
  </div>
</template>
