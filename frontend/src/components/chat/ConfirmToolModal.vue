<script setup lang="ts">
/**
 * ConfirmToolModal — surfaces the WP05 confirm-each tool-call modal
 * when the toolloop pauses on a confirm_each verdict.
 *
 * Wire flow:
 *   1. Backend emits `llm:tool-confirm-request` with a typed
 *      ConfirmToolRequest payload (request_id, server, tool, args
 *      summary). The toolloop goroutine is blocked until the user
 *      decides.
 *   2. This component subscribes via useEventStream, queues incoming
 *      requests, and renders the head of the queue.
 *   3. On a button click we call client.llm.resolveConfirm(id, decision)
 *      and pop the queue head; the toolloop unblocks server-side.
 *
 * Decisions:
 *   - allow         — dispatch this single call; next call re-prompts.
 *   - deny          — block this single call; conversation continues.
 *   - always_allow  — dispatch AND write a session override so this
 *                     (server, tool) skips the modal next time.
 *   - always_deny   — block AND write a session override so this
 *                     (server, tool) blocks automatically next time.
 *
 * The component is mounted at the SessionsView root so a single
 * instance handles every confirm event for the entire chat surface.
 */

import { computed, ref } from 'vue';
import { useEventStream } from '@/lib/useEventStream';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { ConfirmDecision, ConfirmToolRequest } from '@/lib/types';
import BaseDialog from '@/components/ui/BaseDialog.vue';

const client = useHarnessClient();

const queue = ref<ConfirmToolRequest[]>([]);
const submitting = ref(false);
const lastError = ref<string | null>(null);

useEventStream<ConfirmToolRequest>('llm:tool-confirm-request', (payload) => {
  if (!payload || !payload.request_id) return;
  // Dedup against an already-queued id (defensive — a reconnect can
  // replay events).
  if (queue.value.some((q) => q.request_id === payload.request_id)) return;
  queue.value = [...queue.value, payload];
});

const head = computed<ConfirmToolRequest | null>(() => queue.value[0] ?? null);

function popHead() {
  queue.value = queue.value.slice(1);
}

async function decide(decision: ConfirmDecision) {
  const current = head.value;
  if (!current || submitting.value) return;
  submitting.value = true;
  lastError.value = null;
  try {
    await client.llm.resolveConfirm(current.request_id, decision);
    popHead();
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
  } finally {
    submitting.value = false;
  }
}

const argsPretty = computed<string>(() => {
  const raw = head.value?.args_redacted ?? '';
  if (!raw) return '';
  try {
    const parsed = JSON.parse(raw);
    return JSON.stringify(parsed, null, 2);
  } catch {
    return raw;
  }
});

defineExpose({ queue });
</script>

<template>
  <BaseDialog
    :open="!!head"
    title="Tool confirmation required"
    panel-class="w-full max-w-lg rounded-md border border-accent-hairline bg-surface-1 p-5 shadow-xl"
    :close-on-overlay-click="false"
    data-testid="confirm-tool-modal"
    @close="() => {}"
  >
    <div v-if="head">
      <div class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Tool confirmation required
      </div>
      <h2
        id="confirm-tool-modal-title"
        class="mt-1 font-ui text-base font-semibold text-ink"
      >
        Allow <span class="font-mono text-accent">{{ head.server }}.{{ head.tool }}</span>?
      </h2>
      <p
        v-if="head.reason"
        class="mt-1 font-ui text-[12px] text-ink-muted"
      >
        {{ head.reason }}
      </p>
      <div
        v-if="argsPretty"
        class="mt-3 max-h-48 overflow-y-auto rounded-sm border border-border-muted bg-surface-2 p-3 font-mono text-[11px] text-ink"
        data-testid="confirm-tool-modal-args"
      >
        <pre class="whitespace-pre-wrap break-words">{{ argsPretty }}</pre>
      </div>
      <div
        v-if="lastError"
        class="mt-3 rounded-sm border border-signal-warn bg-surface-2 px-3 py-2 font-ui text-[12px] text-signal-warn"
        role="alert"
      >
        {{ lastError }}
      </div>
      <div class="mt-5 grid grid-cols-2 gap-2 font-ui text-xs uppercase tracking-[0.18em]">
        <button
          type="button"
          class="rounded-md border border-accent text-accent px-3 py-2 hover:bg-surface-2 disabled:opacity-50"
          :disabled="submitting"
          data-testid="confirm-tool-allow"
          @click="decide('allow')"
        >
          Allow
        </button>
        <button
          type="button"
          class="rounded-md border border-border-muted text-ink px-3 py-2 hover:bg-surface-2 disabled:opacity-50"
          :disabled="submitting"
          data-testid="confirm-tool-deny"
          @click="decide('deny')"
        >
          Deny
        </button>
        <button
          type="button"
          class="rounded-md border border-accent-hairline text-accent px-3 py-2 hover:bg-surface-2 disabled:opacity-50"
          :disabled="submitting"
          data-testid="confirm-tool-always-allow"
          @click="decide('always_allow')"
        >
          Always allow
        </button>
        <button
          type="button"
          class="rounded-md border border-border-muted text-ink-muted px-3 py-2 hover:bg-surface-2 disabled:opacity-50"
          :disabled="submitting"
          data-testid="confirm-tool-always-deny"
          @click="decide('always_deny')"
        >
          Always deny
        </button>
      </div>
      <div
        v-if="queue.length > 1"
        class="mt-3 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        +{{ queue.length - 1 }} more pending
      </div>
    </div>
  </BaseDialog>
</template>
