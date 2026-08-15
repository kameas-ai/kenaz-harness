<script setup lang="ts">
/**
 * AskUserQuestion — rich interactive elicitation dialog.
 *
 * ## What is parked behind this dialog
 *
 * kenaz__ask_user_question is default-on and its Delegate is the real
 * elicit RPC bridge, so a model that calls it BLOCKS a goroutine inside
 * `elicitview.API.OpenDialog` for up to ten minutes. This component is
 * the only thing that can release it. Until 2026-08-14 nothing in the
 * app imported this file — the dialog existed, was tested in isolation,
 * and was never mounted — so the pause had no answering surface at all.
 * It is mounted in App.vue now, next to ConfirmToolModal, for the same
 * reason that one is: a modal that owns a blocked goroutine must be
 * reachable from every route, not from one view.
 *
 * Wire flow (WP04 synchronous single-question):
 *   1. Backend emits "elicit:pending" with an ElicitRequest payload.
 *   2. This component subscribes via useEventStream and queues requests.
 *   3. The dialog renders the head of the queue via the appropriate
 *      question-kind sub-component (RadioQuestion, CheckboxQuestion, etc.)
 *   4. When the user submits, the component calls
 *      client.elicit.submitAnswer(requestID, answer, false).
 *   5. When the user dismisses (Escape / Cancel), it calls
 *      client.elicit.submitAnswer(requestID, null, true).
 *   6. The backend's blocking OpenDialog call returns and the model
 *      receives the AskResult.
 *
 * ## Answers cross the wire as VALUES
 *
 * `submitAnswer` takes the answer value, not a JSON string of it. The Go
 * parameter is a json.RawMessage that Wails fills from the argument's own
 * JSON text, so pre-stringifying double-encodes: the model's schema
 * promises `["a","b"]` for a checkbox and would receive the string
 * "[\"a\",\"b\"]" instead.
 *
 * Accessibility:
 *   - role="dialog" + aria-modal="true" + aria-labelledby
 *   - Esc key = Cancel (submitAnswer with cancelled=true)
 *   - Focus trap: first focusable element inside the panel receives
 *     focus on mount; Tab cycles within the panel.
 *   - data-testid attributes on every interactive element.
 *
 * Preview pane:
 *   - Rendered by PreviewFrame.vue in a side-by-side layout when
 *     request.preview is present. The pane is hidden when absent.
 */

import {
  computed,
  nextTick,
  onBeforeUnmount,
  onMounted,
  ref,
  watch,
} from 'vue';
import { useEventStream } from '@/lib/useEventStream';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { ElicitRequest } from '@/lib/types';
import RadioQuestion from './RadioQuestion.vue';
import CheckboxQuestion from './CheckboxQuestion.vue';
import TextQuestion from './TextQuestion.vue';
import NumberQuestion from './NumberQuestion.vue';
import SliderQuestion from './SliderQuestion.vue';
import DateQuestion from './DateQuestion.vue';
import FileQuestion from './FileQuestion.vue';
import PreviewFrame from './PreviewFrame.vue';

const client = useHarnessClient();

// ── queue ──────────────────────────────────────────────────────────────

const queue = ref<ElicitRequest[]>([]);
const submitting = ref(false);
const lastError = ref<string | null>(null);

// Current answer value held by the active question-kind component.
// Reset each time the head changes.
const answer = ref<unknown>(null);

function enqueue(payload: ElicitRequest | null | undefined) {
  if (!payload?.request_id) return;
  // Deferred asks are fire-and-forget: nothing is blocked on them, and
  // they have their own non-modal surface. Putting one in a modal queue
  // would interrupt the user for a question the model already moved past.
  if (payload.mode === 'deferred') return;
  // Dedup on reconnect — the backend re-emits pending on reconnect.
  if (queue.value.some((q) => q.request_id === payload.request_id)) return;
  queue.value = [...queue.value, payload];
}

useEventStream<ElicitRequest>('elicit:pending', enqueue);

/**
 * Rebuild the queue from the server's parked set.
 *
 * The pause lives in the harness process, not in this component, so a
 * reload / hot-reload / WS reconnect that forgets the queue does NOT
 * un-park anything: the goroutine is still sitting there, and without
 * this the user would watch a chat that has silently stalled. Failures
 * are swallowed on purpose — "I could not reach the harness" must not be
 * rendered as "nothing is pending".
 */
async function reconcile() {
  try {
    const pending = await client.elicit.listPending();
    for (const req of pending) enqueue(req);
  } catch {
    // Live topic still delivers anything that parks from here on.
  }
}

const head = computed<ElicitRequest | null>(() => queue.value[0] ?? null);

const extraPending = computed<number>(() =>
  Math.max(0, queue.value.length - 1),
);

function popHead() {
  queue.value = queue.value.slice(1);
  answer.value = null;
  lastError.value = null;
}

// Reset the held answer whenever the head changes. Keyed on request_id,
// not on kind: two consecutive radio questions share a kind, and the
// input subtree is re-created per request (see :key in the template), so
// a kind-only watch would carry question 1's answer into question 2 and
// submit it unseen.
watch(
  () => head.value?.request_id,
  () => {
    answer.value = null;
  },
);

// ── submit / cancel ────────────────────────────────────────────────────

/**
 * True when an error means "that ask is already resolved" rather than
 * "the call failed" — the request timed out server-side (OpenDialog's
 * ten-minute deadline), or another window answered it.
 *
 * This matters more here than it looks: the dialog is app-global and
 * modal. Treating a resolved-elsewhere ask as a failure would leave a
 * full-screen overlay that neither Submit nor Cancel can clear, locking
 * the user out of the whole app over a question nobody is waiting on.
 * Matched on the message because the error crosses the Wails / HTTP
 * boundary as a string in both transports.
 */
function isAlreadyResolved(err: unknown): boolean {
  const msg = (err instanceof Error ? err.message : String(err)).toLowerCase();
  return msg.includes('unknown or already-resolved');
}

async function run(fn: () => Promise<void>) {
  if (submitting.value) return;
  submitting.value = true;
  lastError.value = null;
  try {
    await fn();
    popHead();
  } catch (err) {
    if (isAlreadyResolved(err)) {
      popHead();
    } else {
      lastError.value = err instanceof Error ? err.message : String(err);
    }
  } finally {
    submitting.value = false;
  }
}

async function submit() {
  const req = head.value;
  if (!req) return;
  // The VALUE, not JSON.stringify(value) — see the header comment.
  await run(() => client.elicit.submitAnswer(req.request_id, answer.value, false));
}

async function cancel() {
  const req = head.value;
  if (!req) return;
  await run(() => client.elicit.submitAnswer(req.request_id, null, true));
}

// ── keyboard handling ──────────────────────────────────────────────────

function onKeydown(e: KeyboardEvent) {
  if (!head.value) return;
  if (e.key === 'Escape') {
    e.preventDefault();
    void cancel();
  }
  if (e.key === 'Enter' && !e.shiftKey) {
    // Enter submits for non-textarea kinds (text kind with multiline uses
    // Shift+Enter for newlines; plain Enter = submit).
    const kind = head.value.kind;
    if (kind !== 'text') {
      e.preventDefault();
      void submit();
    }
  }
}

onMounted(() => {
  document.addEventListener('keydown', onKeydown);
  void reconcile();
});
onBeforeUnmount(() => document.removeEventListener('keydown', onKeydown));

// ── focus trap ─────────────────────────────────────────────────────────

const panelRef = ref<HTMLElement | null>(null);

function getFocusable(): HTMLElement[] {
  if (!panelRef.value) return [];
  return Array.from(
    panelRef.value.querySelectorAll<HTMLElement>(
      'button:not([disabled]), input:not([disabled]), select:not([disabled]), ' +
      'textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
    ),
  );
}

function trapFocus(e: KeyboardEvent) {
  if (!head.value || e.key !== 'Tab') return;
  const focusable = getFocusable();
  if (focusable.length === 0) return;
  const first = focusable[0];
  const last = focusable[focusable.length - 1];
  if (e.shiftKey && document.activeElement === first) {
    e.preventDefault();
    last.focus();
  } else if (!e.shiftKey && document.activeElement === last) {
    e.preventDefault();
    first.focus();
  }
}

watch(
  head,
  async (req) => {
    if (!req) return;
    await nextTick();
    const focusable = getFocusable();
    if (focusable.length > 0) focusable[0].focus();
  },
);

onMounted(() => document.addEventListener('keydown', trapFocus));
onBeforeUnmount(() => document.removeEventListener('keydown', trapFocus));

// ── expose for tests ───────────────────────────────────────────────────

defineExpose({ queue, submit, cancel, answer, reconcile });
</script>

<template>
  <div
    v-if="head"
    class="fixed inset-0 z-50 flex items-center justify-center"
    role="dialog"
    aria-modal="true"
    :aria-labelledby="`ask-dialog-title-${head.request_id}`"
    data-testid="ask-user-question-dialog"
  >
    <!-- Overlay -->
    <div class="absolute inset-0 bg-modal-overlay" />

    <!-- Panel -->
    <div
      ref="panelRef"
      class="relative flex w-full max-w-4xl rounded-md border border-accent-hairline bg-surface-1 shadow-xl"
      :class="head.preview ? 'flex-row' : 'flex-col'"
    >
      <!-- Question column -->
      <div
        class="flex min-w-0 flex-1 flex-col p-5"
        :class="head.preview ? 'border-r border-border-muted' : ''"
      >
        <!-- Eyebrow -->
        <div
          class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
          data-testid="ask-dialog-eyebrow"
        >
          Question from model
        </div>

        <!-- Question text -->
        <h2
          :id="`ask-dialog-title-${head.request_id}`"
          class="mt-1 font-ui text-sm font-semibold text-ink"
          data-testid="ask-dialog-question"
        >
          {{ head.question }}
        </h2>

        <!-- Question kind input.
             Keyed on request_id so the input subtree is REBUILT per
             question. Without the key, two consecutive questions of the
             same kind reuse one child instance, whose onMounted default
             emit never re-runs — so question 2 would submit question 1's
             answer, or null. -->
        <div :key="head.request_id" class="mt-4 flex-1" data-testid="ask-dialog-input">
          <RadioQuestion
            v-if="head.kind === 'radio'"
            :options="head.options ?? []"
            :default-value="head.default_value"
            @update:model-value="(v) => (answer = v)"
          />
          <CheckboxQuestion
            v-else-if="head.kind === 'checkbox'"
            :options="head.options ?? []"
            :default-value="head.default_value"
            @update:model-value="(v) => (answer = v)"
          />
          <TextQuestion
            v-else-if="head.kind === 'text'"
            :placeholder="head.placeholder"
            :default-value="head.default_value"
            :aria-label="head.question"
            @update:model-value="(v) => (answer = v)"
            @submit="submit"
          />
          <NumberQuestion
            v-else-if="head.kind === 'number'"
            :min="head.min"
            :max="head.max"
            :step="head.step"
            :default-value="head.default_value"
            :aria-label="head.question"
            @update:model-value="(v) => (answer = v)"
          />
          <SliderQuestion
            v-else-if="head.kind === 'slider'"
            :min="head.min ?? 0"
            :max="head.max ?? 100"
            :step="head.step ?? 1"
            :default-value="head.default_value"
            :aria-label="head.question"
            @update:model-value="(v) => (answer = v)"
          />
          <DateQuestion
            v-else-if="head.kind === 'date'"
            :default-value="head.default_value"
            :aria-label="head.question"
            @update:model-value="(v) => (answer = v)"
          />
          <FileQuestion
            v-else-if="head.kind === 'file'"
            @update:model-value="(v) => (answer = v)"
          />
          <!-- Fallback for unknown kinds (forward-compat) -->
          <div
            v-else
            class="font-ui text-[12px] text-signal-warn"
            data-testid="ask-dialog-unknown-kind"
          >
            Unsupported question kind: {{ head.kind }}
          </div>
        </div>

        <!-- Error -->
        <div
          v-if="lastError"
          class="mt-3 rounded-sm border border-signal-warn bg-surface-2 px-3 py-2 font-ui text-[12px] text-signal-warn"
          role="alert"
          data-testid="ask-dialog-error"
        >
          {{ lastError }}
        </div>

        <!-- Actions -->
        <div
          class="mt-5 flex items-center gap-2 font-ui text-[11px] uppercase tracking-[0.18em]"
          data-testid="ask-dialog-actions"
        >
          <button
            type="button"
            class="rounded-md border border-accent bg-accent/10 px-4 py-2 text-accent hover:bg-accent/20 disabled:cursor-not-allowed disabled:opacity-50"
            :disabled="submitting"
            data-testid="ask-dialog-submit"
            @click="submit"
          >
            Submit
          </button>
          <button
            type="button"
            class="rounded-md border border-border-muted px-4 py-2 text-ink-muted hover:bg-surface-2 disabled:cursor-not-allowed disabled:opacity-50"
            :disabled="submitting"
            data-testid="ask-dialog-cancel"
            @click="cancel"
          >
            Cancel
          </button>
        </div>

        <!-- Queue overflow badge -->
        <div
          v-if="extraPending > 0"
          class="mt-3 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle"
          data-testid="ask-dialog-queue-badge"
        >
          +{{ extraPending }} more pending
        </div>
      </div>

      <!-- Preview column (WP03) -->
      <div
        v-if="head.preview"
        class="w-[420px] flex-shrink-0 overflow-hidden rounded-r-md"
        data-testid="ask-dialog-preview-pane"
      >
        <PreviewFrame
          :kind="head.preview.kind"
          :content="head.preview.content"
          :language="head.preview.language"
        />
      </div>
    </div>
  </div>
</template>
