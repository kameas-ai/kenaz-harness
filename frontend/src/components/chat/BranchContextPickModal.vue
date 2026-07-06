<script setup lang="ts">
/**
 * BranchContextPickModal — the context-pick modal that opens when the
 * user clicks [Branch it off] in BranchSuggestionBanner (FR-006 / plan §arch §4).
 *
 * Fields:
 *   - Workflow title (pre-filled from BranchSuggestion.ProposedTitle)
 *   - Branch model picker (defaults to Settings.BranchAdvisorDefaultModel
 *     → Settings.CompactionModel → session's active model)
 *   - Context-pick checklist (last 4 turns, pinned memories pre-checked;
 *     attached artifacts and system prompt unchecked)
 *   - Tool grants dropdown (inherit / readonly / none)
 *
 * On Submit, calls parent's createSubagentBranch handler with the
 * collected SubagentCreateOptions. On Cancel, closes cleanly.
 */

import { computed, ref, watch } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type {
  BranchSuggestion,
  ContextItemKind,
  SubagentToolGrantMode,
  SubagentCreateOptions,
  Branch,
} from '@/lib/types';

const props = defineProps<{
  parentSessionId: string;
  open: boolean;
  suggestion?: BranchSuggestion | null;
}>();

const emit = defineEmits<{
  close: [];
  created: [branch: Branch];
}>();

const client = useHarnessClient();

// ── Form state ──────────────────────────────────────────────────────
const title = ref('');
const providerId = ref('');
const modelId = ref('');

// Context picks — default: last_4_turns + pinned_memories checked.
const contextPicks = ref<Record<ContextItemKind, boolean>>({
  last_4_turns: true,
  pinned_memories: true,
  attached_artifacts: false,
  system_prompt: false,
});

const toolGrantMode = ref<SubagentToolGrantMode>('inherit');
const submitting = ref(false);
const lastError = ref<string | null>(null);

// Reset when modal opens with a new suggestion.
watch(
  [() => props.open, () => props.suggestion],
  ([open]) => {
    if (open) {
      title.value = props.suggestion?.proposedTitle ?? '';
      providerId.value = '';
      modelId.value = '';
      contextPicks.value = {
        last_4_turns: true,
        pinned_memories: true,
        attached_artifacts: false,
        system_prompt: false,
      };
      toolGrantMode.value = 'inherit';
      lastError.value = null;
    }
  },
);

const selectedContextItems = computed((): ContextItemKind[] =>
  (Object.keys(contextPicks.value) as ContextItemKind[]).filter(
    (k) => contextPicks.value[k],
  ),
);

async function submit() {
  if (submitting.value || !props.parentSessionId) return;
  submitting.value = true;
  lastError.value = null;
  try {
    const opts: SubagentCreateOptions = {
      parentSessionId: props.parentSessionId,
      title: title.value.trim(),
      modelPreference: modelId.value ? 'exact' : 'same',
      recommendationId: props.suggestion?.id,
      advisorSignals: props.suggestion?.signals,
      advisorConfidence: props.suggestion?.confidence,
      contextItems: selectedContextItems.value,
      toolGrantMode: toolGrantMode.value,
    };
    if (modelId.value) {
      opts.exactProviderId = providerId.value.trim();
      opts.exactModelId = modelId.value.trim();
    }
    // Call existing branches.create — the subagent metadata is enriched
    // on the backend via Branches.CreateSubagentBranch (WP04).
    const branch = await client.branches.create(opts);
    emit('created', branch);
    emit('close');
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
  } finally {
    submitting.value = false;
  }
}

const contextItemLabel: Record<ContextItemKind, string> = {
  last_4_turns: 'Last 4 turns',
  pinned_memories: 'Pinned memories',
  attached_artifacts: 'Attached artifacts',
  system_prompt: 'System prompt',
};

const toolGrantLabels: Record<SubagentToolGrantMode, string> = {
  inherit: 'Inherit from parent',
  readonly: 'Read-only tools only',
  none: 'No tools',
  cedar: 'Advanced (custom security policy)',
};
</script>

<template>
  <div
    v-if="open"
    class="fixed inset-0 z-50 flex items-center justify-center"
    role="dialog"
    aria-modal="true"
    aria-labelledby="branch-context-pick-modal-title"
    data-testid="branch-context-pick-modal"
  >
    <div class="absolute inset-0 bg-modal-overlay" @click="emit('close')" />
    <div
      class="relative w-full max-w-md rounded-md border border-accent-hairline bg-surface-1 p-5 shadow-xl"
    >
      <h2
        id="branch-context-pick-modal-title"
        class="font-ui text-base font-semibold text-ink"
      >
        Spawn a subagent branch
      </h2>

      <div class="mt-4 flex flex-col gap-3">
        <!-- Workflow title -->
        <label class="flex flex-col gap-1">
          <span class="font-ui text-xs text-ink-subtle">Workflow title</span>
          <input
            v-model="title"
            type="text"
            class="rounded-md border border-accent-hairline bg-surface-0 px-2 py-1 font-ui text-sm text-ink"
            placeholder="What should this subagent work on?"
            data-testid="branch-pick-title"
          />
        </label>

        <!-- Branch model (optional override) -->
        <div class="flex flex-col gap-1">
          <span class="font-ui text-xs text-ink-subtle">
            Branch model (leave blank to use default)
          </span>
          <div class="flex gap-2">
            <input
              v-model="providerId"
              type="text"
              aria-label="Branch provider id (e.g. anthropic)"
              class="w-1/2 rounded-md border border-accent-hairline bg-surface-0 px-2 py-1 font-ui text-sm text-ink"
              placeholder="Provider id (e.g. anthropic)"
              data-testid="branch-pick-provider"
            />
            <input
              v-model="modelId"
              type="text"
              aria-label="Branch model id (e.g. claude-haiku-4)"
              class="w-1/2 rounded-md border border-accent-hairline bg-surface-0 px-2 py-1 font-ui text-sm text-ink"
              placeholder="Model id (e.g. claude-haiku-4)"
              data-testid="branch-pick-model"
            />
          </div>
        </div>

        <!-- Context picks -->
        <fieldset class="flex flex-col gap-1">
          <legend class="font-ui text-xs text-ink-subtle">
            Seed the branch with
          </legend>
          <div
            v-for="kind in (Object.keys(contextPicks) as ContextItemKind[])"
            :key="kind"
            class="flex items-center gap-2"
          >
            <input
              :id="`ctx-pick-${kind}`"
              v-model="contextPicks[kind]"
              type="checkbox"
              class="rounded"
              :data-testid="`branch-pick-ctx-${kind}`"
            />
            <label
              :for="`ctx-pick-${kind}`"
              class="font-ui text-sm text-ink"
            >
              {{ contextItemLabel[kind] }}
            </label>
          </div>
        </fieldset>

        <!-- Tool grants -->
        <label class="flex flex-col gap-1">
          <span class="font-ui text-xs text-ink-subtle">Tool grants</span>
          <select
            v-model="toolGrantMode"
            class="rounded-md border border-accent-hairline bg-surface-0 px-2 py-1 font-ui text-sm text-ink"
            data-testid="branch-pick-tool-grant"
          >
            <option value="inherit">{{ toolGrantLabels.inherit }}</option>
            <option value="readonly">{{ toolGrantLabels.readonly }}</option>
            <option value="none">{{ toolGrantLabels.none }}</option>
            <!-- "cedar" option is intentionally not rendered in v1 until
                 cedar-policy-editor-ui is shipped (plan §arch §4). -->
          </select>
        </label>

        <!-- Error state -->
        <div
          v-if="lastError"
          class="font-ui text-xs text-status-error"
          role="alert"
          data-testid="branch-pick-error"
        >
          {{ lastError }}
        </div>
      </div>

      <div class="mt-5 flex justify-end gap-2">
        <button
          type="button"
          class="rounded-md border border-accent-hairline px-3 py-1 font-ui text-sm text-ink"
          data-testid="branch-pick-cancel"
          @click="emit('close')"
        >
          Cancel
        </button>
        <button
          type="button"
          class="rounded-md bg-accent-strong px-3 py-1 font-ui text-sm text-on-accent disabled:opacity-50"
          :disabled="submitting || !parentSessionId"
          data-testid="branch-pick-submit"
          @click="submit"
        >
          {{ submitting ? 'Creating…' : 'Create subagent branch' }}
        </button>
      </div>
    </div>
  </div>
</template>
