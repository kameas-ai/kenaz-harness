<script setup lang="ts">
/**
 * UndeliveredBadge — the sticky NOT DELIVERED state on a user message that
 * never reached the model (undelivered-message-retry, owner dogfood
 * 2026-10-07).
 *
 * The owner's OpenRouter account ran out of credits; the provider refused
 * the request before a single token; the chat showed a transient banner
 * and nothing on the message, so after topping up they retyped it — a
 * duplicate user row. This badge sits ON the message (not a transient
 * banner), survives reload (it is derived from the persisted run outcome,
 * Sessions_TurnRuns), names the classified reason, and offers Retry,
 * which re-runs this same message instead of sending a new one.
 *
 * Rendered by MessageList as a sibling of the user's MessageBubble.
 */
import { computed } from 'vue';
import {
  deliveryCopy,
  offersToolsMenu,
  type DeliveryFailure,
  type RequestSizeContext,
} from '@/lib/delivery';
import type { AutoRetryState } from '@/lib/useSession';

const props = defineProps<{
  failure: DeliveryFailure;
  /**
   * True on the message a Retry would re-run — the newest user message.
   * An older undelivered message shows the badge but no button: it rides
   * along in history with the next delivered turn.
   */
  canRetry: boolean;
  /** Non-null while a transient failure waits for its automatic retry. */
  autoRetry?: AutoRetryState | null;
  /** Tool tokens and model window for the request_too_large remedy. */
  sizeContext?: RequestSizeContext | null;
  /** Show "Open tools" for request_too_large (false where the menu cannot act). */
  toolsAvailable?: boolean;
}>();

const emit = defineEmits<{
  (e: 'retry'): void;
  (e: 'cancel-retry'): void;
  (e: 'open-tools'): void;
}>();

const copy = computed(() => deliveryCopy(props.failure, props.sizeContext ?? undefined));
const showTools = computed(
  () => props.toolsAvailable !== false && props.canRetry && offersToolsMenu(props.failure),
);
const retryingCopy = computed(() => {
  const r = props.autoRetry;
  if (!r) return '';
  return `Retrying (${r.attempt}/${r.max}) in ${Math.round(r.delayMs / 1000)}s…`;
});
</script>

<template>
  <div
    class="mt-1 flex flex-col items-end gap-1 font-ui text-[11px]"
    role="status"
    data-testid="undelivered-badge"
  >
    <div class="flex items-center gap-2 text-signal-danger">
      <span
        class="rounded-sm border border-signal-danger px-1.5 py-[1px] text-[10px] uppercase tracking-[0.18em]"
        data-testid="undelivered-label"
      >
        Not delivered
      </span>
      <span class="text-ink" data-testid="undelivered-reason">{{ copy }}</span>
    </div>
    <p
      v-if="failure.message"
      class="max-w-[60ch] break-words text-right text-ink-subtle"
      data-testid="undelivered-provider-message"
    >
      {{ failure.provider ? `${failure.provider}: ` : '' }}{{ failure.message }}
    </p>
    <div v-if="canRetry" class="flex items-center gap-2">
      <button
        v-if="showTools"
        type="button"
        class="px-2 py-0.5 rounded-md border border-border-muted text-ink hover:bg-surface-2"
        data-testid="undelivered-open-tools"
        @click="emit('open-tools')"
      >
        Open tools
      </button>
      <template v-if="autoRetry">
        <span class="text-ink-muted" data-testid="undelivered-auto-retry">{{ retryingCopy }}</span>
        <button
          type="button"
          class="px-2 py-0.5 rounded-md border border-border-muted text-ink-muted hover:bg-surface-2"
          data-testid="undelivered-cancel-retry"
          @click="emit('cancel-retry')"
        >
          Cancel
        </button>
      </template>
      <button
        v-else
        type="button"
        class="px-2 py-0.5 rounded-md border border-accent text-accent uppercase tracking-[0.18em] text-[10px] hover:bg-surface-2"
        data-testid="undelivered-retry"
        @click="emit('retry')"
      >
        Retry
      </button>
    </div>
  </div>
</template>
