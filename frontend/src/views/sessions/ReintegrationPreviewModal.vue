<script setup lang="ts">
/**
 * ReintegrationPreviewModal — the "Bring back to parent" preview modal
 * (FR-008b / plan §arch §7).
 *
 * Flow:
 *   1. Opens in a "Generating…" state and fires ProposeReintegrationSummary
 *      via the branches client.
 *   2. Renders an editable textarea pre-filled with the proposed summary.
 *   3. Buttons: [Insert into parent] / [Regenerate] / [Cancel].
 *   4. Empty-branch case (empty proposedSummary): shows "Discard branch"
 *      affordance.
 *   5. Model-failure case: shows Retry button.
 */

import { ref, watch } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { ReintegrationProposal } from '@/lib/types';

const props = defineProps<{
  branchSessionId: string;
  branchTitle?: string;
  open: boolean;
}>();

const emit = defineEmits<{
  close: [];
  /** Fires after a successful Insert — parent refreshes to show the summary msg. */
  reintegrated: [branchSessionId: string];
}>();

const client = useHarnessClient();

const loading = ref(false);
const proposal = ref<ReintegrationProposal | null>(null);
const summaryText = ref('');
const lastError = ref<string | null>(null);
const submitting = ref(false);

// Track whether the user has edited the proposed text.
const wasEdited = ref(false);

watch(
  () => props.open,
  (next) => {
    if (next) {
      // Reset state and kick off the proposal fetch.
      proposal.value = null;
      summaryText.value = '';
      lastError.value = null;
      wasEdited.value = false;
      void fetchProposal();
    }
  },
);

async function fetchProposal() {
  if (!props.branchSessionId) return;
  loading.value = true;
  lastError.value = null;
  try {
    const p = await client.branches.proposeReintegrationSummary(props.branchSessionId);
    proposal.value = p;
    summaryText.value = p.proposedSummary;
    wasEdited.value = false;
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}

async function regenerate() {
  wasEdited.value = false;
  await fetchProposal();
}

function onSummaryInput(e: Event) {
  const ta = e.target as HTMLTextAreaElement;
  summaryText.value = ta.value;
  wasEdited.value = true;
}

async function insertIntoParent() {
  if (submitting.value || !props.branchSessionId) return;
  submitting.value = true;
  lastError.value = null;
  try {
    await client.branches.commitReintegration({
      branchSessionId: props.branchSessionId,
      finalSummaryText: summaryText.value,
      wasEdited: wasEdited.value,
    });
    emit('reintegrated', props.branchSessionId);
    emit('close');
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
  } finally {
    submitting.value = false;
  }
}

const isEmpty = () =>
  proposal.value !== null && proposal.value.proposedSummary === '';
</script>

<template>
  <div
    v-if="open"
    class="fixed inset-0 z-50 flex items-center justify-center"
    role="dialog"
    aria-modal="true"
    aria-labelledby="reintegration-modal-title"
    data-testid="reintegration-preview-modal"
  >
    <div class="absolute inset-0 bg-modal-overlay" @click="emit('close')" />
    <div
      class="relative flex w-full max-w-lg flex-col gap-4 rounded-md border border-accent-hairline bg-surface-1 p-5 shadow-xl"
    >
      <h2
        id="reintegration-modal-title"
        class="font-ui text-base font-semibold text-ink"
      >
        Bring back to parent{{ branchTitle ? ': ' + branchTitle : '' }}
      </h2>

      <!-- Loading state -->
      <div
        v-if="loading"
        class="font-ui text-sm text-ink-subtle"
        data-testid="reintegration-generating"
      >
        Generating summary…
      </div>

      <!-- Error state -->
      <div
        v-else-if="lastError && !proposal"
        class="flex flex-col gap-2"
      >
        <div
          class="font-ui text-sm text-status-error"
          role="alert"
          data-testid="reintegration-error"
        >
          {{ lastError }}
        </div>
        <button
          type="button"
          class="self-start font-ui text-xs text-ink-link hover:underline"
          data-testid="reintegration-retry"
          @click="fetchProposal"
        >
          Retry
        </button>
      </div>

      <!-- Empty-branch case: subagent has no turns yet -->
      <div
        v-else-if="isEmpty()"
        class="flex flex-col gap-2"
        data-testid="reintegration-empty"
      >
        <p class="font-ui text-sm text-ink-subtle">
          This subagent branch has no conversation yet. Nothing to bring back.
        </p>
        <p class="font-ui text-sm text-ink-muted">
          You can discard the branch or continue working in it.
        </p>
      </div>

      <!-- Normal case: show editable summary -->
      <div v-else-if="proposal" class="flex flex-col gap-2">
        <div class="flex items-center justify-between">
          <label for="reintegration-summary" class="font-ui text-xs text-ink-subtle">
            Summary (editable)
          </label>
          <span
            v-if="proposal.model"
            class="font-ui text-[11px] text-ink-subtle"
          >
            {{ proposal.model }} · ~{{ proposal.tokenCount }} tokens
          </span>
        </div>
        <textarea
          id="reintegration-summary"
          :value="summaryText"
          rows="12"
          class="w-full rounded-md border border-accent-hairline bg-surface-0 px-2 py-1 font-ui text-sm text-ink"
          data-testid="reintegration-summary-textarea"
          @input="onSummaryInput"
        />
        <div
          v-if="lastError"
          class="font-ui text-xs text-status-error"
          role="alert"
          data-testid="reintegration-insert-error"
        >
          {{ lastError }}
        </div>
      </div>

      <!-- Action row -->
      <div class="flex justify-end gap-2">
        <button
          type="button"
          class="rounded-md border border-accent-hairline px-3 py-1 font-ui text-sm text-ink"
          data-testid="reintegration-cancel"
          @click="emit('close')"
        >
          Cancel
        </button>
        <button
          v-if="!isEmpty() && proposal"
          type="button"
          class="font-ui text-sm text-ink-link hover:underline"
          :disabled="loading"
          data-testid="reintegration-regenerate"
          @click="regenerate"
        >
          Regenerate
        </button>
        <button
          v-if="!isEmpty() && proposal"
          type="button"
          class="rounded-md bg-accent-strong px-3 py-1 font-ui text-sm text-on-accent disabled:opacity-50"
          :disabled="submitting || !summaryText.trim()"
          data-testid="reintegration-insert"
          @click="insertIntoParent"
        >
          {{ submitting ? 'Inserting…' : 'Insert into parent' }}
        </button>
      </div>
    </div>
  </div>
</template>
