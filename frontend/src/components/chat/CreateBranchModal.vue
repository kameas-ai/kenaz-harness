<script setup lang="ts">
/**
 * CreateBranchModal — opens from the "+ Fork" button in the
 * BranchSidebar (or a future right-click "Branch from here" message
 * action). Lets the user:
 *
 *   - Type a short title + free-text task hint.
 *   - Pick the model preference: smaller / same / larger / exact.
 *   - See the kernel's recommendation chip with a one-line rationale.
 *
 * On submit the modal calls client.branches.create({...}) and emits
 * 'created' with the new Branch row so the parent view can refresh
 * the sidebar.
 */

import { computed, ref, watch } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type {
  Branch,
  BranchCreateOptions,
  BranchModelPreference,
  BranchRecommendedModel,
} from '@/lib/types';

const props = defineProps<{
  parentSessionId: string;
  open: boolean;
}>();

const emit = defineEmits<{
  close: [];
  created: [branch: Branch];
}>();

const client = useHarnessClient();

const title = ref('');
const taskHint = ref('');
const preference = ref<BranchModelPreference>('same');
const exactProviderId = ref('');
const exactModelId = ref('');
const recommendation = ref<BranchRecommendedModel | null>(null);
const submitting = ref(false);
const lastError = ref<string | null>(null);

async function refreshRecommendation() {
  if (!props.parentSessionId || preference.value === 'exact') {
    recommendation.value = null;
    return;
  }
  try {
    recommendation.value = await client.branches.recommendModel(
      props.parentSessionId,
      taskHint.value,
      preference.value,
    );
  } catch (err) {
    recommendation.value = null;
    lastError.value = err instanceof Error ? err.message : String(err);
  }
}

watch(
  [() => props.open, () => props.parentSessionId, taskHint, preference],
  () => {
    if (props.open) {
      void refreshRecommendation();
    }
  },
  { immediate: true },
);

watch(
  () => props.open,
  (next) => {
    if (next) {
      title.value = '';
      taskHint.value = '';
      preference.value = 'same';
      exactProviderId.value = '';
      exactModelId.value = '';
      lastError.value = null;
    }
  },
);

async function submit() {
  if (submitting.value) return;
  submitting.value = true;
  lastError.value = null;
  try {
    const opts: BranchCreateOptions = {
      parentSessionId: props.parentSessionId,
      title: title.value.trim(),
      taskHint: taskHint.value.trim(),
      modelPreference: preference.value,
    };
    if (preference.value === 'exact') {
      opts.exactProviderId = exactProviderId.value.trim();
      opts.exactModelId = exactModelId.value.trim();
    }
    const branch = await client.branches.create(opts);
    emit('created', branch);
    emit('close');
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
  } finally {
    submitting.value = false;
  }
}

const recommendationLabel = computed(() => {
  if (!recommendation.value) return '';
  const parts: string[] = [];
  if (recommendation.value.modelId) {
    parts.push(recommendation.value.modelId);
  }
  if (recommendation.value.tier) {
    parts.push(`(${recommendation.value.tier})`);
  }
  return parts.join(' ');
});

defineExpose({ submit, recommendation });
</script>

<template>
  <div
    v-if="open"
    class="fixed inset-0 z-50 flex items-center justify-center"
    role="dialog"
    aria-modal="true"
    aria-labelledby="create-branch-modal-title"
    data-testid="create-branch-modal"
  >
    <div class="absolute inset-0 bg-modal-overlay" @click="emit('close')" />
    <div
      class="relative w-full max-w-md rounded-md border border-accent-hairline bg-surface-1 p-5 shadow-xl"
    >
      <h2
        id="create-branch-modal-title"
        class="font-ui text-base font-semibold text-ink"
      >
        Fork a branch
      </h2>

      <div class="mt-4 flex flex-col gap-3">
        <label class="flex flex-col gap-1">
          <span class="font-ui text-xs text-ink-subtle">Title</span>
          <input
            v-model="title"
            type="text"
            class="rounded-md border border-accent-hairline bg-surface-0 px-2 py-1 font-ui text-sm text-ink"
            placeholder="What's the latest dep version?"
            data-testid="create-branch-title"
          />
        </label>
        <label class="flex flex-col gap-1">
          <span class="font-ui text-xs text-ink-subtle">Task hint</span>
          <textarea
            v-model="taskHint"
            class="rounded-md border border-accent-hairline bg-surface-0 px-2 py-1 font-ui text-sm text-ink"
            rows="3"
            data-testid="create-branch-hint"
          />
        </label>
        <label class="flex flex-col gap-1">
          <span class="font-ui text-xs text-ink-subtle">Model preference</span>
          <select
            v-model="preference"
            class="rounded-md border border-accent-hairline bg-surface-0 px-2 py-1 font-ui text-sm text-ink"
            data-testid="create-branch-preference"
          >
            <option value="same">Same as parent</option>
            <option value="smaller">Smaller (faster, cheaper)</option>
            <option value="larger">Larger (deeper reasoning)</option>
            <option value="exact">Pin a specific model</option>
          </select>
        </label>
        <div v-if="preference === 'exact'" class="flex flex-col gap-2">
          <label class="flex flex-col gap-1">
            <span class="font-ui text-xs text-ink-subtle">Provider id</span>
            <input
              v-model="exactProviderId"
              type="text"
              class="rounded-md border border-accent-hairline bg-surface-0 px-2 py-1 font-ui text-sm text-ink"
              data-testid="create-branch-exact-provider"
            />
          </label>
          <label class="flex flex-col gap-1">
            <span class="font-ui text-xs text-ink-subtle">Model id</span>
            <input
              v-model="exactModelId"
              type="text"
              class="rounded-md border border-accent-hairline bg-surface-0 px-2 py-1 font-ui text-sm text-ink"
              data-testid="create-branch-exact-model"
            />
          </label>
        </div>
        <div
          v-if="recommendation && preference !== 'exact'"
          class="rounded-md border border-accent-hairline bg-surface-0 p-2"
          data-testid="create-branch-recommendation"
        >
          <div class="font-ui text-xs text-ink-subtle">Kernel recommends</div>
          <div class="font-ui text-sm text-ink">{{ recommendationLabel }}</div>
          <div v-if="recommendation.notes" class="mt-1 font-ui text-[11px] text-ink-subtle">
            {{ recommendation.notes }}
          </div>
          <div
            v-if="recommendation.crossProviderWarning"
            class="mt-1 rounded bg-status-warn-bg p-1 font-ui text-[11px] text-status-warn-fg"
            data-testid="create-branch-cross-provider-warn"
          >
            {{ recommendation.crossProviderWarning }}
          </div>
        </div>
        <div
          v-if="lastError"
          class="font-ui text-xs text-status-error"
          role="alert"
          data-testid="create-branch-error"
        >
          {{ lastError }}
        </div>
      </div>

      <div class="mt-5 flex justify-end gap-2">
        <button
          type="button"
          class="rounded-md border border-accent-hairline px-3 py-1 font-ui text-sm text-ink"
          data-testid="create-branch-cancel"
          @click="emit('close')"
        >
          Cancel
        </button>
        <button
          type="button"
          class="rounded-md bg-accent-strong px-3 py-1 font-ui text-sm text-on-accent disabled:opacity-50"
          :disabled="submitting || !parentSessionId"
          data-testid="create-branch-submit"
          @click="submit"
        >
          Create branch
        </button>
      </div>
    </div>
  </div>
</template>
