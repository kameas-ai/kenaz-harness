<script setup lang="ts">
/**
 * AuthFailureToast — surfaces the WP05 auth-failure key-rotation UI.
 * (provider-keychain-rotation-01KQ8TD9)
 *
 * The backend emits a `provider:auth-failed` broker event when a chat turn
 * fails with *ErrProviderAuthFailed. This component renders a toast that lets
 * the user paste a new API key and hit Enter to rotate it inline. On success:
 *   - If Settings.autoResumeOnKeyRotation is true AND the result contains an
 *     auto_resume_token, the paused turn is automatically redriven.
 *   - Otherwise the toast shows "Key updated — resend your last message."
 *
 * Dedup: only one entry per profileID is kept in the queue. A second
 * `provider:auth-failed` for the same profileID within 60 seconds of a
 * dismiss is silently dropped to prevent toast spam on sustained outages.
 *
 * Mounted once at the SessionsView root so a single instance handles every
 * auth failure across the chat surface — matches MergeSuggestionToast pattern.
 *
 * role="alertdialog" + aria-label for keyboard accessibility (WP05 a11y
 * acceptance criterion).
 */

import { computed, ref } from 'vue';
import { useEventStream } from '@/lib/useEventStream';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { AuthFailedPayload } from '@/lib/types';

const props = defineProps<{
  /** Optional manual seed for tests: pre-populate without a backend event. */
  initial?: AuthFailedPayload | null;
  /** Whether the auto-resume setting is currently enabled (read by parent). */
  autoResumeEnabled?: boolean;
}>();

const emit = defineEmits<{
  rotated: [profileID: string];
  dismissed: [profileID: string];
}>();

const client = useHarnessClient();

// ── Queue ──────────────────────────────────────────────────────────────────

interface QueueEntry {
  payload: AuthFailedPayload;
  /** Timestamp of the most recent dismiss for this profileID (for 60s cooldown). */
  dismissedAt?: number;
}

const queue = ref<QueueEntry[]>(
  props.initial ? [{ payload: props.initial }] : [],
);

/** Per-profileID cooldown map: dismiss → suppress for 60 s. */
const suppressUntil: Record<string, number> = {};

useEventStream<AuthFailedPayload>('provider:auth-failed', (payload) => {
  if (!payload?.profile_id) return;
  // Dedup: already in queue?
  if (queue.value.some((q) => q.payload.profile_id === payload.profile_id)) return;
  // Cooldown: dismissed < 60 s ago?
  const until = suppressUntil[payload.profile_id] ?? 0;
  if (Date.now() < until) return;
  queue.value = [...queue.value, { payload }];
});

const head = computed<QueueEntry | null>(() => queue.value[0] ?? null);

function popHead() {
  queue.value = queue.value.slice(1);
}

// ── Form state ────────────────────────────────────────────────────────────

const apiKeyInput = ref('');
const submitting = ref(false);
const lastError = ref<string | null>(null);
const rotatedOk = ref(false);
const rotatedMessage = ref('');

// ── Actions ────────────────────────────────────────────────────────────────

async function submit() {
  const cur = head.value;
  if (!cur || submitting.value) return;
  const key = apiKeyInput.value.trim();
  if (!key) return;

  submitting.value = true;
  lastError.value = null;
  rotatedOk.value = false;

  try {
    const result = await client.llm.testAndRotateKey(
      cur.payload.profile_id,
      key,
      'inline-toast',
    );

    if (!result.success) {
      lastError.value = result.message ?? 'Key test failed — check the value and try again.';
      return;
    }

    // Key test passed.
    const autoResume = props.autoResumeEnabled !== false;
    if (autoResume && result.auto_resume_token) {
      // Redrive the paused turn silently — toast self-dismisses.
      await client.llm.resumeAfterKeyRotation(result.auto_resume_token);
      emit('rotated', cur.payload.profile_id);
      apiKeyInput.value = '';
      popHead();
    } else {
      // Auto-resume disabled or no paused turn: show confirmation copy.
      rotatedOk.value = true;
      rotatedMessage.value = result.auto_resume_token
        ? 'Key updated — resend your last message.'
        : 'Key updated.';
      emit('rotated', cur.payload.profile_id);
    }
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
  } finally {
    submitting.value = false;
  }
}

function handleKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault();
    void submit();
  }
}

function dismiss() {
  const cur = head.value;
  if (!cur) return;
  // Suppress further events for this profileID for 60 s.
  suppressUntil[cur.payload.profile_id] = Date.now() + 60_000;
  apiKeyInput.value = '';
  lastError.value = null;
  rotatedOk.value = false;
  emit('dismissed', cur.payload.profile_id);
  popHead();
}

function dismissAfterRotate() {
  apiKeyInput.value = '';
  lastError.value = null;
  rotatedOk.value = false;
  popHead();
}

function providerLabel(payload: AuthFailedPayload): string {
  const p = payload.provider;
  if (!p) return 'Provider';
  return p.charAt(0).toUpperCase() + p.slice(1);
}

defineExpose({ queue });
</script>

<template>
  <div
    v-if="head"
    class="pointer-events-none fixed inset-x-0 bottom-4 z-50 flex justify-center"
    role="alertdialog"
    aria-label="API key rotation required"
    aria-modal="false"
    data-testid="auth-failure-toast"
  >
    <div
      class="pointer-events-auto flex max-w-md flex-col gap-2 rounded-md border border-signal-warning bg-surface-1 p-3 shadow-lg"
    >
      <div class="font-ui text-[11px] uppercase tracking-[0.18em] text-signal-warning">
        API Key Invalid
      </div>

      <!-- Rotated-ok confirmation state -->
      <template v-if="rotatedOk">
        <div
          class="font-ui text-sm text-ink"
          data-testid="auth-failure-rotated-ok"
        >
          {{ rotatedMessage }}
        </div>
        <div class="mt-1 flex justify-end">
          <button
            type="button"
            class="font-ui text-xs text-ink-link hover:underline"
            data-testid="auth-failure-rotated-ok-close"
            @click="dismissAfterRotate"
          >
            Close
          </button>
        </div>
      </template>

      <!-- Normal rotation input state -->
      <template v-else>
        <div class="font-ui text-sm text-ink" data-testid="auth-failure-body">
          {{ providerLabel(head.payload) }} rejected your API key. Paste a new key
          below and press Enter to rotate without re-opening Settings.
        </div>
        <div v-if="head.payload.reason" class="font-ui text-[11px] text-ink-muted">
          {{ head.payload.reason }}
        </div>

        <input
          v-model="apiKeyInput"
          type="password"
          autocomplete="off"
          class="mt-1 w-full rounded border border-border bg-surface-0 px-2 py-1 font-mono text-[12px] text-ink outline-none focus:border-accent"
          placeholder="Paste new API key…"
          aria-label="New API key"
          data-testid="auth-failure-key-input"
          :disabled="submitting"
          @keydown="handleKeydown"
        />

        <div
          v-if="lastError"
          class="font-ui text-[11px] text-status-error"
          data-testid="auth-failure-error"
        >
          {{ lastError }}
        </div>

        <div class="mt-1 flex justify-end gap-2">
          <button
            type="button"
            class="font-ui text-xs text-ink-link hover:underline"
            data-testid="auth-failure-dismiss"
            @click="dismiss"
          >
            Dismiss
          </button>
          <button
            type="button"
            class="font-ui text-xs text-ink-link hover:underline disabled:opacity-40"
            :disabled="submitting || !apiKeyInput.trim()"
            data-testid="auth-failure-rotate"
            @click="submit"
          >
            {{ submitting ? 'Testing…' : 'Rotate key' }}
          </button>
        </div>
      </template>
    </div>
  </div>
</template>
