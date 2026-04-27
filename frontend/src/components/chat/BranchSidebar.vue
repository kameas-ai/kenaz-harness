<script setup lang="ts">
/**
 * BranchSidebar — lists every branch off the active session and exposes
 * the "fork", "merge", "abandon", and "open" actions.
 *
 * v1 keeps the rendering flat (no nested tree) — spec §4.6 / FR-040
 * defers branch-of-branch to v2. Each row shows: title, model, status,
 * relative last-activity time, and an action cluster.
 *
 * Wire flow:
 *   - On mount: client.branches.list(parentSessionId) → render rows.
 *   - "Open" emits 'open' with the child session id; the parent view
 *     pivots the chat surface onto the branch.
 *   - "Merge" calls client.branches.merge(branchId), refreshes the list.
 *   - "Abandon" calls client.branches.abandon(branchId), refreshes.
 *   - "+ Fork" emits 'create' so the parent view can open the
 *     CreateBranchModal.
 */

import { computed, onMounted, ref, watch } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { Branch } from '@/lib/types';

const props = defineProps<{
  parentSessionId: string;
}>();

const emit = defineEmits<{
  open: [childSessionId: string];
  create: [];
}>();

const client = useHarnessClient();
const branches = ref<Branch[]>([]);
const loading = ref(false);
const lastError = ref<string | null>(null);

async function refresh() {
  if (!props.parentSessionId) {
    branches.value = [];
    return;
  }
  loading.value = true;
  lastError.value = null;
  try {
    branches.value = await client.branches.list(props.parentSessionId);
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
    branches.value = [];
  } finally {
    loading.value = false;
  }
}

onMounted(() => {
  void refresh();
});

watch(
  () => props.parentSessionId,
  () => {
    void refresh();
  },
);

async function handleMerge(branchId: string) {
  try {
    await client.branches.merge(branchId);
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
  }
  await refresh();
}

async function handleAbandon(branchId: string) {
  try {
    await client.branches.abandon(branchId);
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
  }
  await refresh();
}

function statusLabel(status: Branch['status']) {
  switch (status) {
    case 'active':
      return 'Active';
    case 'merging':
      return 'Merging…';
    case 'merged':
      return 'Merged';
    case 'abandoned':
      return 'Abandoned';
    default:
      return status;
  }
}

const hasBranches = computed(() => branches.value.length > 0);

defineExpose({ refresh, branches });
</script>

<template>
  <aside
    class="flex w-full flex-col gap-2 border-l border-accent-hairline bg-surface-1 p-3"
    aria-label="Conversation branches"
    data-testid="branch-sidebar"
  >
    <div class="flex items-center justify-between">
      <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Branches
      </h2>
      <button
        type="button"
        class="font-ui text-xs text-ink-link hover:underline"
        data-testid="branch-create-btn"
        @click="emit('create')"
      >
        + Fork
      </button>
    </div>

    <div v-if="loading" class="font-ui text-xs text-ink-subtle">Loading…</div>
    <div
      v-else-if="lastError"
      class="font-ui text-xs text-status-error"
      role="alert"
      data-testid="branch-sidebar-error"
    >
      {{ lastError }}
    </div>
    <div
      v-else-if="!hasBranches"
      class="font-ui text-xs text-ink-subtle italic"
      data-testid="branch-sidebar-empty"
    >
      No branches yet — fork off this session to explore a side question.
    </div>
    <ul v-else class="flex flex-col gap-2" data-testid="branch-list">
      <li
        v-for="branch in branches"
        :key="branch.id"
        class="rounded-md border border-accent-hairline bg-surface-0 p-2"
        :data-testid="`branch-row-${branch.id}`"
      >
        <div class="flex items-start justify-between gap-2">
          <div class="min-w-0 flex-1">
            <div class="truncate font-ui text-sm text-ink">
              {{ branch.title || 'Untitled branch' }}
            </div>
            <div class="mt-0.5 flex flex-wrap items-center gap-1 font-ui text-[11px] text-ink-subtle">
              <span :data-testid="`branch-status-${branch.id}`">{{ statusLabel(branch.status) }}</span>
              <span v-if="branch.modelId" aria-hidden="true">·</span>
              <span v-if="branch.modelId">{{ branch.modelId }}</span>
            </div>
          </div>
        </div>

        <div class="mt-2 flex flex-wrap gap-2">
          <button
            type="button"
            class="font-ui text-xs text-ink-link hover:underline"
            :data-testid="`branch-open-${branch.id}`"
            @click="emit('open', branch.childSessionId)"
          >
            Open
          </button>
          <button
            v-if="branch.status === 'active'"
            type="button"
            class="font-ui text-xs text-ink-link hover:underline"
            :data-testid="`branch-merge-${branch.id}`"
            @click="handleMerge(branch.id)"
          >
            Merge
          </button>
          <button
            v-if="branch.status === 'active'"
            type="button"
            class="font-ui text-xs text-status-error hover:underline"
            :data-testid="`branch-abandon-${branch.id}`"
            @click="handleAbandon(branch.id)"
          >
            Abandon
          </button>
        </div>
      </li>
    </ul>
  </aside>
</template>
