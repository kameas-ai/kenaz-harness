<script setup lang="ts">
/**
 * PlanApprovalModal — three-action modal (Approve / Edit / Discard) for
 * reviewing the model's plan before allowing write tools to resume.
 * (plan-mode-posture-01KZNP3F WP06)
 *
 * Wire flow:
 *   1. Model calls __exit_plan_mode(plan) → result {awaiting_user_approval:true,
 *      plan_id} is forwarded to the frontend via a toolloop event.
 *   2. The parent (SessionView / ChatView) receives the event, sets
 *      `pendingPlan` and `planId` props, and mounts this modal.
 *   3. User clicks Approve / Edit / Discard.
 *   4. The modal calls the appropriate harness RPC:
 *        Planmode_Approve / Planmode_Edit / Planmode_Discard
 *   5. On success, emits `approved`, `edited`, or `discarded`.
 *   6. The parent unmounts the modal and re-enables chat input.
 *
 * Non-dismissive: clicking outside the modal (the overlay) does NOT close
 * it. The user must explicitly choose one of the three actions per spec
 * FR-010. The Edit flow opens an inline textarea; Cancel returns to the
 * review view (does not dismiss the modal).
 */

import { ref, computed } from 'vue';

const props = defineProps<{
  /** The session this plan belongs to. */
  sessionId: string;
  /** The plan artifact ID returned by __exit_plan_mode. */
  planId: string;
  /** The plan markdown to display. */
  plan: string;
}>();

const emit = defineEmits<{
  approved: [planId: string];
  edited: [planId: string, editedPlan: string];
  discarded: [planId: string];
  error: [message: string];
}>();

const submitting = ref(false);
const lastError = ref<string | null>(null);
const editMode = ref(false);
const editedText = ref(props.plan);

const displayText = computed(() => editMode.value ? editedText.value : props.plan);

// ── Actions ──────────────────────────────────────────────────────────────────

async function doApprove() {
  if (submitting.value) return;
  submitting.value = true;
  lastError.value = null;
  try {
    await window.go?.rpc?.Bindings?.Planmode_Approve({
      session_id: props.sessionId,
      plan_id: props.planId,
    });
    emit('approved', props.planId);
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
    emit('error', lastError.value!);
  } finally {
    submitting.value = false;
  }
}

async function doDiscard() {
  if (submitting.value) return;
  submitting.value = true;
  lastError.value = null;
  try {
    await window.go?.rpc?.Bindings?.Planmode_Discard({
      session_id: props.sessionId,
      plan_id: props.planId,
    });
    emit('discarded', props.planId);
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
    emit('error', lastError.value!);
  } finally {
    submitting.value = false;
  }
}

async function doSaveEdit() {
  if (submitting.value) return;
  submitting.value = true;
  lastError.value = null;
  try {
    await window.go?.rpc?.Bindings?.Planmode_Edit({
      session_id: props.sessionId,
      plan_id: props.planId,
      edited_plan: editedText.value,
    });
    emit('edited', props.planId, editedText.value);
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
    emit('error', lastError.value!);
  } finally {
    submitting.value = false;
  }
}

function enterEditMode() {
  editedText.value = props.plan;
  editMode.value = true;
}

function cancelEdit() {
  editMode.value = false;
}
</script>

<template>
  <!--
    The outer div covers the viewport. The inner click-stop prevents
    clicks on the content panel from bubbling to the overlay. The overlay
    itself has no click handler — clicking outside does NOT dismiss.
  -->
  <div
    class="fixed inset-0 z-50 flex items-center justify-center"
    role="dialog"
    aria-modal="true"
    aria-labelledby="plan-approval-modal-title"
    data-testid="plan-approval-modal"
  >
    <!-- Non-dismissive overlay. Intentionally no @click handler. -->
    <div class="absolute inset-0 bg-modal-overlay" aria-hidden="true" />

    <div
      class="relative flex w-full max-w-2xl flex-col rounded-md border border-amber-300 bg-surface-1 shadow-xl dark:border-amber-700"
      data-testid="plan-approval-modal-content"
      @click.stop
    >
      <!-- Header -->
      <div class="flex items-center justify-between border-b border-border-muted px-5 py-3">
        <div>
          <div
            class="font-ui text-[11px] uppercase tracking-[0.18em] text-amber-600 dark:text-amber-400"
          >
            Plan review
          </div>
          <h2
            id="plan-approval-modal-title"
            class="mt-0.5 font-ui text-base font-semibold text-ink"
          >
            The model has drafted a plan
          </h2>
          <p class="mt-1 font-ui text-[12px] text-ink-muted">
            Review the plan below. Approve to allow the model to proceed,
            Edit to modify the plan, or Discard to send the model back to
            the drawing board.
          </p>
        </div>
      </div>

      <!-- Plan content / editor -->
      <div class="max-h-[50vh] min-h-[10rem] overflow-y-auto px-5 py-4">
        <textarea
          v-if="editMode"
          v-model="editedText"
          aria-label="Edit plan content"
          class="h-full min-h-[16rem] w-full resize-none rounded border border-border-muted bg-surface-2 p-3 font-mono text-sm text-ink focus:outline-none focus:ring-1 focus:ring-accent"
          data-testid="plan-approval-modal-editor"
          :disabled="submitting"
        />
        <div
          v-else
          class="whitespace-pre-wrap font-mono text-sm text-ink"
          data-testid="plan-approval-modal-plan-text"
        >{{ displayText }}</div>
      </div>

      <!-- Error notice -->
      <div
        v-if="lastError"
        class="mx-5 mb-2 rounded-sm border border-signal-danger bg-surface-2 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
        data-testid="plan-approval-modal-error"
      >
        {{ lastError }}
      </div>

      <!-- Action buttons -->
      <div class="flex items-center justify-between border-t border-border-muted px-5 py-3">
        <!-- Left: Discard (destructive, less prominent) -->
        <button
          type="button"
          class="rounded px-3 py-1.5 font-ui text-xs uppercase tracking-[0.12em] text-signal-danger hover:bg-surface-2 disabled:opacity-50"
          :disabled="submitting"
          data-testid="plan-approval-modal-discard"
          @click="doDiscard"
        >
          Discard
        </button>

        <!-- Right: Edit / Cancel-edit / Approve actions -->
        <div class="flex items-center gap-2">
          <!-- Cancel edit — only shown in edit mode -->
          <button
            v-if="editMode"
            type="button"
            class="rounded px-3 py-1.5 font-ui text-xs uppercase tracking-[0.12em] text-ink-muted hover:bg-surface-2 disabled:opacity-50"
            :disabled="submitting"
            data-testid="plan-approval-modal-cancel-edit"
            @click="cancelEdit"
          >
            Cancel edit
          </button>

          <!-- Edit (open editor) — only shown when not in edit mode -->
          <button
            v-if="!editMode"
            type="button"
            class="rounded border border-border-muted px-3 py-1.5 font-ui text-xs uppercase tracking-[0.12em] text-ink hover:bg-surface-2 disabled:opacity-50"
            :disabled="submitting"
            data-testid="plan-approval-modal-edit"
            @click="enterEditMode"
          >
            Edit
          </button>

          <!-- Save edited plan (approve with edits) — only shown in edit mode -->
          <button
            v-if="editMode"
            type="button"
            class="rounded border border-amber-500 bg-amber-50 px-3 py-1.5 font-ui text-xs uppercase tracking-[0.12em] text-amber-700 hover:bg-amber-100 disabled:opacity-50 dark:bg-amber-900 dark:text-amber-300"
            :disabled="submitting || editedText.trim() === ''"
            data-testid="plan-approval-modal-save-edit"
            @click="doSaveEdit"
          >
            Save &amp; approve
          </button>

          <!-- Approve — only shown when not in edit mode -->
          <button
            v-if="!editMode"
            type="button"
            class="rounded border border-amber-500 bg-amber-500 px-3 py-1.5 font-ui text-xs uppercase tracking-[0.12em] text-white hover:bg-amber-600 disabled:opacity-50"
            :disabled="submitting"
            data-testid="plan-approval-modal-approve"
            @click="doApprove"
          >
            Approve &amp; continue
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
