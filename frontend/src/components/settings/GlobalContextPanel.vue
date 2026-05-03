<script setup lang="ts">
/**
 * GlobalContextPanel — global-scope attachment list with drag-reorder.
 * Lives at the top of the Contexts page (extracted from SettingsView).
 *
 * Global context applies to every session — the resolved stream
 * concatenates global → project → session, so ordering here affects
 * the prefix every conversation receives.
 */
import { onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { Plus } from '@/shell/icons';
import AttachmentRow from '@/components/contexts/AttachmentRow.vue';
import AttachmentTreePicker from '@/components/contexts/AttachmentTreePicker.vue';
import type { Attachment } from '@/lib/types';

const client = useHarnessClient();

const globalAttachments = ref<Attachment[]>([]);
const globalAttachmentsError = ref<string | null>(null);
const globalAttachmentsLoading = ref(false);
const globalPickerOpen = ref(false);
const draggedId = ref<string | null>(null);

async function loadGlobalAttachments() {
  globalAttachmentsLoading.value = true;
  globalAttachmentsError.value = null;
  try {
    const rows = await client.attachments.list({ scopeKind: 'global', scopeId: '' });
    globalAttachments.value = [...rows];
  } catch (err) {
    globalAttachments.value = [];
    globalAttachmentsError.value = err instanceof Error ? err.message : String(err);
  } finally {
    globalAttachmentsLoading.value = false;
  }
}

function openGlobalPicker() {
  globalPickerOpen.value = true;
}

function onGlobalAdded(att: Attachment) {
  globalAttachments.value = [...globalAttachments.value, att];
}

function onGlobalRefreshed(updated: Attachment) {
  globalAttachments.value = globalAttachments.value.map((a) =>
    a.id === updated.id ? updated : a,
  );
}

function onGlobalRemoved(id: string) {
  globalAttachments.value = globalAttachments.value.filter((a) => a.id !== id);
}

function onDragStart(_evt: DragEvent, id: string) {
  draggedId.value = id;
}

function onDragOver(evt: DragEvent, _overId: string) {
  evt.preventDefault();
  if (evt.dataTransfer) evt.dataTransfer.dropEffect = 'move';
}

async function onDrop(_evt: DragEvent, overId: string) {
  const dragged = draggedId.value;
  draggedId.value = null;
  if (!dragged || dragged === overId) return;
  const list = [...globalAttachments.value];
  const fromIdx = list.findIndex((a) => a.id === dragged);
  const toIdx = list.findIndex((a) => a.id === overId);
  if (fromIdx < 0 || toIdx < 0) return;
  const [moved] = list.splice(fromIdx, 1);
  list.splice(toIdx, 0, moved);
  globalAttachments.value = list;
  try {
    await client.attachments.reorder('global', '', list.map((a) => a.id));
  } catch (err) {
    globalAttachmentsError.value = err instanceof Error ? err.message : String(err);
    await loadGlobalAttachments();
  }
}

function onDragEnd() {
  draggedId.value = null;
}

onMounted(() => {
  void loadGlobalAttachments();
});
</script>

<template>
  <section data-testid="global-context-section" class="max-w-3xl">
    <div class="flex items-center justify-between">
      <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Global context
        <span class="ml-1 text-ink-dim">({{ globalAttachments.length }})</span>
      </h2>
      <button
        type="button"
        class="flex items-center gap-1 rounded-sm border border-accent-hairline bg-surface-1 px-2 py-1 font-ui text-[11px] text-accent hover:bg-accent-glow"
        data-testid="global-add-context"
        @click="openGlobalPicker"
      >
        <Plus :size="12" />
        <span>Add context</span>
      </button>
    </div>
    <p class="mt-1 font-ui text-[11px] text-ink-dim">
      Global context applies to every session in the harness. The
      resolved stream concatenates global → project → session, so
      ordering here affects the prefix every conversation receives.
    </p>
    <div
      v-if="globalAttachmentsError"
      class="mt-2 rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[11px] text-signal-danger"
      role="alert"
      data-testid="global-attachments-error"
    >
      {{ globalAttachmentsError }}
    </div>
    <div
      v-if="globalAttachmentsLoading"
      class="mt-2 font-ui text-xs text-ink-muted"
      data-testid="global-attachments-loading"
    >
      Loading…
    </div>
    <ul
      v-else-if="globalAttachments.length > 0"
      class="mt-2 space-y-2"
      data-testid="global-attachments-list"
    >
      <li v-for="a in globalAttachments" :key="a.id">
        <AttachmentRow
          :attachment="a"
          :draggable="true"
          @refreshed="onGlobalRefreshed"
          @removed="onGlobalRemoved"
          @drag-start="onDragStart"
          @drag-over="onDragOver"
          @drop="onDrop"
          @drag-end="onDragEnd"
        />
      </li>
    </ul>
    <p
      v-else
      class="mt-2 font-ui text-xs text-ink-muted"
      data-testid="global-attachments-empty"
    >
      No global context yet. Pick a file from the library to attach.
    </p>

    <AttachmentTreePicker
      :open="globalPickerOpen"
      scope-kind="global"
      scope-id=""
      @added="onGlobalAdded"
      @close="globalPickerOpen = false"
    />
  </section>
</template>
