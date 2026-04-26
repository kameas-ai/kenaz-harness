<script setup lang="ts">
/**
 * AttachmentRow — render one stored Attachment within a scope-management
 * surface (Project landing, Settings → Global, ResolvedContextPanel).
 *
 * Layout:
 *   [drag-handle] [icon] [source / preview]   [scope-badge] [actions]
 *
 * Source / preview rules:
 *   - library:<path>        → render the relative path (monospace).
 *   - inline:<sha>          → render the first 60 chars of `content`
 *                             with a leading "inline:" marker.
 *
 * Scope badge surfaces the attachment's scope (global / project /
 * session) using the canonical glyphs from the WP file.
 *
 * Actions:
 *   - Refresh   — re-reads a `library:<path>` attachment from disk.
 *                 Disabled for inline-source attachments. Surfaces an
 *                 inline "source missing" warning when refresh fails
 *                 with the well-known sentinel substring (A10).
 *   - Remove    — deletes the attachment via Attachments_Remove.
 *
 * Drag handle is purely visual unless the parent passes `draggable`;
 * the parent owns the actual reorder dispatch (see ProjectLandingPage
 * / SettingsView for the HTML5 drag-and-drop wiring).
 */

import { computed, ref } from 'vue';
import {
  AlertTriangle,
  FileText,
  Folder,
  Trash2,
} from '@/shell/icons';
import type { Attachment, AttachmentScopeKind } from '@/lib/types';
import { useHarnessClient } from '@/lib/harnessClientContext';

const props = withDefaults(
  defineProps<{
    attachment: Attachment;
    /** Read-only mode hides the action buttons (used by ResolvedContextPanel). */
    readonly?: boolean;
    /** Whether the parent has wired drag-and-drop reorder. Hides the handle when false. */
    draggable?: boolean;
  }>(),
  { readonly: false, draggable: false },
);

const emit = defineEmits<{
  /** A successful refresh emits the updated attachment. */
  (e: 'refreshed', updated: Attachment): void;
  /** A successful removal emits the id. */
  (e: 'removed', id: string): void;
  /** HTML5 dragstart relay — parents wire onDragOver/Drop on the row. */
  (e: 'drag-start', evt: DragEvent, id: string): void;
  (e: 'drag-over', evt: DragEvent, id: string): void;
  (e: 'drop', evt: DragEvent, id: string): void;
  (e: 'drag-end', evt: DragEvent, id: string): void;
}>();

const client = useHarnessClient();

const refreshing = ref(false);
const removing = ref(false);
const errorMsg = ref<string | null>(null);
const sourceMissing = ref(false);

const isLibrary = computed(() =>
  props.attachment.contentSource.startsWith('library:'),
);
const isInline = computed(() =>
  props.attachment.contentSource.startsWith('inline:'),
);

const libraryPath = computed(() => {
  if (!isLibrary.value) return '';
  return props.attachment.contentSource.slice('library:'.length);
});

const inlinePreview = computed(() => {
  if (!isInline.value) return '';
  const body = props.attachment.content ?? '';
  if (body.length <= 60) return body;
  return body.slice(0, 60) + '…';
});

const scopeBadge = computed<{ glyph: string; label: string }>(() => {
  switch (props.attachment.scopeKind as AttachmentScopeKind) {
    case 'global':
      return { glyph: 'G', label: 'global' };
    case 'project':
      return { glyph: 'P', label: 'project' };
    case 'session':
    default:
      return { glyph: 'S', label: 'session' };
  }
});

async function onRefresh() {
  if (refreshing.value || !isLibrary.value) return;
  refreshing.value = true;
  errorMsg.value = null;
  sourceMissing.value = false;
  try {
    const updated = await client.attachments.refresh(props.attachment.id);
    emit('refreshed', updated);
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    errorMsg.value = msg;
    // The backend surfaces a stable "source missing" / "not found"
    // sentinel when the underlying library file has been deleted.
    // We surface a softer affordance ("source missing — Detach") so
    // the user can clean up without losing the snapshot (FR-202 / A10).
    if (
      msg.toLowerCase().includes('not found') ||
      msg.toLowerCase().includes('no such file') ||
      msg.toLowerCase().includes('source missing')
    ) {
      sourceMissing.value = true;
    }
  } finally {
    refreshing.value = false;
  }
}

async function onRemove() {
  if (removing.value) return;
  removing.value = true;
  errorMsg.value = null;
  try {
    await client.attachments.remove(props.attachment.id);
    emit('removed', props.attachment.id);
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : String(err);
  } finally {
    removing.value = false;
  }
}

function onDragStart(evt: DragEvent) {
  emit('drag-start', evt, props.attachment.id);
}
function onDragOver(evt: DragEvent) {
  emit('drag-over', evt, props.attachment.id);
}
function onDrop(evt: DragEvent) {
  emit('drop', evt, props.attachment.id);
}
function onDragEnd(evt: DragEvent) {
  emit('drag-end', evt, props.attachment.id);
}
</script>

<template>
  <div
    class="rounded-sm border border-border-muted bg-surface-1 px-3 py-2 font-ui text-sm"
    :draggable="draggable === true"
    :data-testid="`attachment-row-${attachment.id}`"
    @dragstart="onDragStart"
    @dragover.prevent="onDragOver"
    @drop.prevent="onDrop"
    @dragend="onDragEnd"
  >
    <div class="flex items-center gap-2">
      <span
        v-if="draggable"
        class="cursor-grab select-none text-ink-dim"
        :data-testid="`attachment-drag-handle-${attachment.id}`"
        aria-hidden="true"
      >
        ⋮⋮
      </span>
      <span class="shrink-0 text-ink-muted">
        <Folder v-if="isLibrary" :size="14" />
        <FileText v-else :size="14" />
      </span>
      <div class="flex-1 min-w-0">
        <div
          v-if="isLibrary"
          class="font-mono text-xs text-ink truncate"
          :title="libraryPath"
          :data-testid="`attachment-source-${attachment.id}`"
        >
          {{ libraryPath }}
        </div>
        <div
          v-else
          class="text-xs text-ink truncate"
          :title="attachment.content"
          :data-testid="`attachment-source-${attachment.id}`"
        >
          <span class="text-ink-dim">inline:</span>
          {{ inlinePreview }}
        </div>
      </div>
      <span
        class="shrink-0 rounded-sm border border-border-muted bg-surface-2 px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-[0.18em] text-ink-muted"
        :data-testid="`attachment-scope-${attachment.id}`"
      >
        {{ scopeBadge.glyph }}/{{ scopeBadge.label }}
      </span>
      <div v-if="!readonly" class="shrink-0 flex items-center gap-1">
        <button
          v-if="isLibrary"
          type="button"
          class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1 text-[11px] text-ink-muted hover:bg-surface-2 hover:text-ink disabled:opacity-50"
          :disabled="refreshing"
          :data-testid="`attachment-refresh-${attachment.id}`"
          @click="onRefresh"
        >
          {{ refreshing ? '…' : 'Refresh' }}
        </button>
        <button
          type="button"
          class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1 text-[11px] text-ink-muted hover:bg-surface-2 hover:text-signal-danger disabled:opacity-50"
          :disabled="removing"
          :aria-label="`Remove attachment`"
          :data-testid="`attachment-remove-${attachment.id}`"
          @click="onRemove"
        >
          <Trash2 :size="12" />
        </button>
      </div>
    </div>
    <div
      v-if="sourceMissing"
      class="mt-2 flex items-start gap-1.5 rounded-sm border border-signal-warn bg-surface-1 px-2 py-1.5 text-[11px] text-signal-warn"
      role="alert"
      :data-testid="`attachment-source-missing-${attachment.id}`"
    >
      <AlertTriangle :size="12" class="mt-[1px] shrink-0" />
      <span class="flex-1">
        Source missing — the snapshot is preserved. Detach to remove.
      </span>
      <button
        type="button"
        class="text-[11px] underline hover:text-ink"
        :data-testid="`attachment-detach-${attachment.id}`"
        @click="onRemove"
      >
        Detach
      </button>
    </div>
    <div
      v-else-if="errorMsg"
      class="mt-2 rounded-sm border border-signal-danger bg-surface-1 px-2 py-1 text-[11px] text-signal-danger"
      role="alert"
      :data-testid="`attachment-error-${attachment.id}`"
    >
      {{ errorMsg }}
    </div>
  </div>
</template>
