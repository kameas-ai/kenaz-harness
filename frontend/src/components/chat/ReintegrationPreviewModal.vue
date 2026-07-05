<script setup lang="ts">
/**
 * ReintegrationPreviewModal — shown when the user clicks "Bring back to
 * parent" on a subagent branch.
 *
 * Flow:
 *   1. Opens in a "Generating…" state and calls
 *      client.branches.proposeReintegrationSummary(branchSessionId).
 *   2. Renders the proposal in an editable <textarea> with token count +
 *      model name.
 *   3. Confirm calls commitReintegration with the (possibly edited)
 *      summary and emits 'committed'.
 *   4. Regenerate re-invokes propose, replacing textarea content.
 *   5. Cancel emits 'close' — branch session stays open, nothing is
 *      inserted.
 *   6. Empty-branch case (proposedSummary === "") shows a "Discard
 *      branch" affordance instead of Insert.
 *   7. Error case (propose/commit failure) shows a Retry button.
 *
 * Keyboard: Esc → close, Enter (when textarea NOT focused) → confirm.
 */

import { onMounted, onUnmounted, ref, watch, computed } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { BranchReintegrationProposal } from '@/lib/types';

const props = defineProps<{
  /** Session id of the subagent branch to reintegrate. */
  branchSessionId: string;
  /** Parent session id — forwarded to commitReintegration. */
  parentSessionId: string;
  open: boolean;
}>();

const emit = defineEmits<{
  close: [];
  committed: [];
}>();

const client = useHarnessClient();

// Internal state
const loading = ref(false);
const submitting = ref(false);
const lastError = ref<string | null>(null);
const proposal = ref<BranchReintegrationProposal | null>(null);
const editedSummary = ref('');
// Track whether the user has changed the textarea
const wasEdited = ref(false);

const isEmpty = computed(
  () => proposal.value !== null && proposal.value.proposedSummary === '',
);

async function propose() {
  if (!props.branchSessionId) return;
  loading.value = true;
  lastError.value = null;
  wasEdited.value = false;
  try {
    const result = await client.branches.proposeReintegrationSummary(
      props.branchSessionId,
    );
    proposal.value = result;
    editedSummary.value = result.proposedSummary;
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
    proposal.value = null;
  } finally {
    loading.value = false;
  }
}

async function confirm() {
  if (submitting.value || loading.value || isEmpty.value) return;
  submitting.value = true;
  lastError.value = null;
  try {
    await client.branches.commitReintegration({
      branchSessionId: props.branchSessionId,
      finalSummaryText: editedSummary.value,
      wasEdited: wasEdited.value,
    });
    emit('committed');
    emit('close');
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
  } finally {
    submitting.value = false;
  }
}

function handleTextareaInput(event: Event) {
  const target = event.target as HTMLTextAreaElement;
  editedSummary.value = target.value;
  if (proposal.value && target.value !== proposal.value.proposedSummary) {
    wasEdited.value = true;
  }
}

// Keyboard handler: Esc → close, Enter (outside textarea) → confirm
function onKeydown(event: KeyboardEvent) {
  if (!props.open) return;
  if (event.key === 'Escape') {
    emit('close');
    return;
  }
  if (
    event.key === 'Enter' &&
    !event.shiftKey &&
    (event.target as HTMLElement).tagName !== 'TEXTAREA'
  ) {
    void confirm();
  }
}

onMounted(() => {
  window.addEventListener('keydown', onKeydown);
});
onUnmounted(() => {
  window.removeEventListener('keydown', onKeydown);
});

// Reset and fetch whenever the modal opens
watch(
  () => props.open,
  (next) => {
    if (next) {
      proposal.value = null;
      editedSummary.value = '';
      wasEdited.value = false;
      lastError.value = null;
      void propose();
    }
  },
  { immediate: true },
);
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
    <!-- backdrop -->
    <div class="absolute inset-0 bg-modal-overlay" @click="emit('close')" />

    <!-- panel -->
    <div
      class="relative w-full max-w-xl rounded-md border border-accent-hairline bg-surface-1 p-5 shadow-xl"
    >
      <h2
        id="reintegration-modal-title"
        class="font-ui text-base font-semibold text-ink"
      >
        Bring back to parent
      </h2>

      <!-- loading -->
      <div
        v-if="loading"
        class="mt-4 font-ui text-sm text-ink-muted"
        data-testid="reintegration-generating"
        aria-live="polite"
      >
        Generating summary…
      </div>

      <!-- error -->
      <div
        v-else-if="lastError"
        class="mt-4 rounded-md border border-signal-warn bg-surface-0 px-3 py-2 font-ui text-[12px] text-signal-warn"
        role="alert"
        data-testid="reintegration-error"
      >
        {{ lastError }}
        <button
          type="button"
          class="ml-2 underline hover:no-underline"
          data-testid="reintegration-retry"
          @click="propose"
        >
          Retry
        </button>
      </div>

      <!-- empty-branch case -->
      <div
        v-else-if="isEmpty"
        class="mt-4 font-ui text-sm text-ink-muted"
        data-testid="reintegration-empty"
      >
        This branch has no conversation to summarise.
      </div>

      <!-- main content -->
      <div v-else-if="proposal" class="mt-4 flex flex-col gap-3">
        <!-- meta line -->
        <div
          class="flex items-center gap-3 font-ui text-[11px] text-ink-subtle"
          data-testid="reintegration-meta"
        >
          <span data-testid="reintegration-model">Model: {{ proposal.model }}</span>
          <span aria-hidden="true">·</span>
          <span data-testid="reintegration-tokens">{{ proposal.tokenCount }} tokens</span>
        </div>

        <!-- editable summary -->
        <label class="flex flex-col gap-1">
          <span class="font-ui text-xs text-ink-subtle">
            Review and edit the summary before inserting into the parent session.
          </span>
          <textarea
            class="min-h-[160px] rounded-md border border-accent-hairline bg-surface-0 px-3 py-2 font-ui text-sm text-ink focus:outline-none focus:ring-1 focus:ring-accent"
            :value="editedSummary"
            data-testid="reintegration-summary-textarea"
            @input="handleTextareaInput"
          />
        </label>
      </div>

      <!-- error banner while committing -->
      <div
        v-if="lastError && !loading && proposal !== null"
        class="mt-3 rounded-md border border-signal-warn bg-surface-0 px-3 py-2 font-ui text-[12px] text-signal-warn"
        role="alert"
        data-testid="reintegration-commit-error"
      >
        {{ lastError }}
      </div>

      <!-- action bar -->
      <div class="mt-5 flex items-center justify-end gap-2">
        <button
          type="button"
          class="rounded-md border border-accent-hairline px-3 py-1 font-ui text-sm text-ink hover:bg-surface-2"
          data-testid="reintegration-cancel"
          @click="emit('close')"
        >
          Cancel
        </button>

        <!-- Regenerate — only shown after a successful propose -->
        <button
          v-if="proposal !== null && !isEmpty"
          type="button"
          class="rounded-md border border-accent-hairline px-3 py-1 font-ui text-sm text-ink hover:bg-surface-2 disabled:opacity-50"
          :disabled="loading || submitting"
          data-testid="reintegration-regenerate"
          @click="propose"
        >
          Regenerate
        </button>

        <!-- Insert / Discard -->
        <button
          v-if="!isEmpty && proposal !== null"
          type="button"
          class="rounded-md bg-accent-strong px-3 py-1 font-ui text-sm text-on-accent disabled:opacity-50"
          :disabled="loading || submitting || editedSummary.trim().length === 0"
          data-testid="reintegration-confirm"
          @click="confirm"
        >
          {{ submitting ? 'Inserting…' : 'Insert into parent' }}
        </button>
      </div>
    </div>
  </div>
</template>
