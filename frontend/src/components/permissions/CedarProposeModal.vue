<script setup lang="ts">
/**
 * CedarProposeModal — modal for agent-proposed Cedar policy snippets.
 *
 * Mission: harness-self-mcp-onboarding-01KQ8TDU WP07.
 *
 * Subscribes to `cedar:propose-pending` via useEventStream.
 * Renders the policy name and body for user review. On accept or reject,
 * delivers the decision via CedarPolicy_ResolvePropose (bound to
 * CedarProposerImpl.ResolveProposeRequestRaw in the Go layer).
 *
 * Esc = reject. 5-minute timeout = auto-reject (mirrors the Go-side timer).
 */

import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useEventStream } from '@/lib/useEventStream';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { CedarProposalPayload } from '@/lib/types';

const MAX_QUEUE = 3;
const TIMEOUT_MS = 5 * 60 * 1000;

const client = useHarnessClient();
const queue = ref<CedarProposalPayload[]>([]);
const submitting = ref(false);
const lastError = ref<string | null>(null);

let timer: ReturnType<typeof setTimeout> | null = null;

function startTimer() {
  if (timer) clearTimeout(timer);
  timer = setTimeout(() => {
    void decide('reject');
  }, TIMEOUT_MS);
}

function clearTimer() {
  if (timer) {
    clearTimeout(timer);
    timer = null;
  }
}

useEventStream<CedarProposalPayload>('cedar:propose-pending', (payload) => {
  if (!payload || !payload.request_id) return;
  if (queue.value.some((q) => q.request_id === payload.request_id)) return;
  if (queue.value.length >= MAX_QUEUE) {
    // Auto-reject overflow proposals so the Go proposer doesn't hang.
    void client.cedarPolicy.resolvePropose(payload.request_id, 'reject').catch(() => {});
    return;
  }
  queue.value = [...queue.value, payload];
});

const head = computed<CedarProposalPayload | null>(() => queue.value[0] ?? null);

watch(
  () => head.value,
  (h) => {
    if (h) {
      startTimer();
    } else {
      clearTimer();
    }
  },
  { immediate: true },
);

onBeforeUnmount(clearTimer);

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape' && head.value) {
    void decide('reject');
  }
}

onMounted(() => document.addEventListener('keydown', onKeydown));
onBeforeUnmount(() => document.removeEventListener('keydown', onKeydown));

async function decide(decision: 'accept' | 'reject') {
  const req = head.value;
  if (!req || submitting.value) return;
  submitting.value = true;
  lastError.value = null;
  clearTimer();
  try {
    await client.cedarPolicy.resolvePropose(req.request_id, decision);
    queue.value = queue.value.filter((q) => q.request_id !== req.request_id);
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
    startTimer();
  } finally {
    submitting.value = false;
  }
}

const extraPending = computed(() => Math.max(0, queue.value.length - 1));

defineExpose({ queue });
</script>

<template>
  <div
    v-if="head"
    class="fixed inset-0 z-50 flex items-center justify-center"
    role="dialog"
    aria-modal="true"
    :aria-labelledby="`cedar-propose-title-${head.request_id}`"
    data-testid="cedar-propose-modal"
  >
    <div class="absolute inset-0 bg-modal-overlay" />
    <div
      class="relative w-full max-w-2xl rounded-md border border-accent-hairline bg-surface-1 p-5 shadow-xl"
    >
      <!-- Eyebrow -->
      <div class="flex items-center gap-1.5 font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        <span aria-hidden="true">📋</span>
        <span>Cedar policy — agent proposal</span>
      </div>

      <!-- Policy name -->
      <h2
        :id="`cedar-propose-title-${head.request_id}`"
        class="mt-1 font-mono text-sm font-semibold text-ink break-all"
        data-testid="cedar-propose-name"
      >
        {{ head.name }}
      </h2>

      <!-- Rationale -->
      <p
        v-if="head.rationale"
        class="mt-2 font-ui text-[12px] text-ink-muted line-clamp-3"
        data-testid="cedar-propose-rationale"
      >
        {{ head.rationale }}
      </p>

      <!-- Policy body (syntax-highlighted as best we can with mono) -->
      <pre
        class="mt-3 max-h-60 overflow-y-auto rounded-sm border border-accent-hairline bg-surface-2 p-3 font-mono text-[11px] text-ink leading-relaxed whitespace-pre-wrap break-all"
        data-testid="cedar-propose-body"
      >{{ head.body }}</pre>

      <!-- Error feedback -->
      <div
        v-if="lastError"
        class="mt-2 rounded-sm border border-signal-warn bg-surface-2 px-3 py-2 font-ui text-[12px] text-signal-warn"
        role="alert"
        data-testid="cedar-propose-error"
      >
        {{ lastError }}
      </div>

      <!-- Buttons: Accept / Reject -->
      <div class="mt-4 grid grid-cols-2 gap-2 font-ui text-[11px] uppercase tracking-[0.18em]">
        <button
          type="button"
          class="rounded-md border border-accent bg-accent text-surface-1 px-3 py-2 hover:opacity-90 disabled:opacity-50"
          :disabled="submitting"
          data-testid="cedar-propose-accept"
          @click="decide('accept')"
        >
          Accept
        </button>
        <button
          type="button"
          class="rounded-md border border-border-muted text-ink-muted px-3 py-2 hover:bg-surface-2 disabled:opacity-50"
          :disabled="submitting"
          data-testid="cedar-propose-reject"
          @click="decide('reject')"
        >
          Reject
        </button>
      </div>

      <!-- Queue overflow -->
      <div
        v-if="extraPending > 0"
        class="mt-3 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle"
        data-testid="cedar-propose-overflow"
      >
        +{{ extraPending }} more pending
      </div>
    </div>
  </div>
</template>
