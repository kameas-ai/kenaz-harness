<script setup lang="ts">
/**
 * ResolvedContextPanel — collapsible panel above MessageList that shows
 * the merged attachment stream for the current session.
 *
 * Default state: collapsed. The header shows the total count plus a
 * per-scope breakdown (e.g. "3 contexts · 1G / 1P / 1S") so the user
 * gets a one-glance answer to "what context is in play?" without
 * expanding.
 *
 * Expanded state: three sub-sections, in resolution order
 * (global → project → session). Each row is read-only — manage actions
 * live on the project landing page (project scope), settings page
 * (global), and a future per-session settings drawer (session). Clicking
 * a row toggles an inline content-snippet preview rendered with
 * `whitespace-pre-wrap` (no markdown — same approach as ContextPreview).
 *
 * The panel re-fetches `Attachments_ListResolved` on mount and whenever
 * the supplied `sessionId` changes (acceptance A8).
 */

import { computed, nextTick, ref, watch } from 'vue';
import { ChevronRight, Plus } from '@/shell/icons';
import AttachmentRow from '@/components/contexts/AttachmentRow.vue';
import type { Attachment, AttachmentScopeKind } from '@/lib/types';
import { useHarnessClient } from '@/lib/harnessClientContext';

const props = defineProps<{
  sessionId: string;
}>();

const client = useHarnessClient();

const expanded = ref(false);
const attachments = ref<readonly Attachment[]>([]);
const loading = ref(false);
const errorMsg = ref<string | null>(null);
const previewId = ref<string | null>(null);

async function refresh(id: string) {
  if (!id) {
    attachments.value = [];
    return;
  }
  loading.value = true;
  errorMsg.value = null;
  try {
    attachments.value = await client.attachments.listResolved(id);
  } catch (err) {
    attachments.value = [];
    errorMsg.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}

watch(
  () => props.sessionId,
  (next) => {
    void refresh(next);
  },
  { immediate: true },
);

function toggle() {
  expanded.value = !expanded.value;
}

function togglePreview(id: string) {
  previewId.value = previewId.value === id ? null : id;
}

const grouped = computed<Record<AttachmentScopeKind, Attachment[]>>(() => {
  const out: Record<AttachmentScopeKind, Attachment[]> = {
    global: [],
    project: [],
    session: [],
  };
  for (const a of attachments.value) {
    const k = a.scopeKind as AttachmentScopeKind;
    if (out[k]) out[k].push(a);
  }
  return out;
});

const summary = computed(() => {
  const g = grouped.value.global.length;
  const p = grouped.value.project.length;
  const s = grouped.value.session.length;
  const total = g + p + s;
  return { g, p, s, total };
});

const sectionLabels: Record<AttachmentScopeKind, string> = {
  global: 'Global',
  project: 'Project',
  session: 'Session',
};

// ── Session-scope "New context folder" affordance (FR-021) ────────────────
const creatingFolder = ref(false);
const newFolderName = ref('');
const newFolderInputRef = ref<HTMLInputElement | null>(null);
const newFolderError = ref<string | null>(null);

function beginCreateFolder() {
  newFolderError.value = null;
  creatingFolder.value = true;
  newFolderName.value = '';
  void nextTick(() => newFolderInputRef.value?.focus());
}

function cancelCreateFolder() {
  creatingFolder.value = false;
  newFolderName.value = '';
  newFolderError.value = null;
}

async function confirmCreateFolder() {
  const name = newFolderName.value.trim().replace(/[/\\]+/g, '-');
  if (!name) {
    cancelCreateFolder();
    return;
  }
  newFolderError.value = null;
  try {
    await client.contexts.createFolder(name);
    await client.attachments.add({
      scopeKind: 'session',
      scopeId: props.sessionId,
      contentSource: `library:${name}`,
      content: '',
    });
    cancelCreateFolder();
    await refresh(props.sessionId);
  } catch (e) {
    newFolderError.value = e instanceof Error ? e.message : 'Folder create failed.';
  }
}
</script>

<template>
  <section
    class="mx-4 mt-2 mb-1 rounded-sm border border-border-muted bg-surface-1"
    data-testid="resolved-context-panel"
  >
    <button
      type="button"
      class="flex w-full items-center gap-2 px-3 py-2 font-ui text-[12px] text-ink-muted hover:text-ink"
      :aria-expanded="expanded"
      data-testid="resolved-context-toggle"
      @click="toggle"
    >
      <ChevronRight
        :size="12"
        :class="expanded ? 'rotate-90 transition-transform' : 'transition-transform'"
      />
      <span class="text-[10px] uppercase tracking-[0.18em] text-ink-subtle">
        Resolved context
      </span>
      <span
        v-if="summary.total > 0"
        class="text-[11px] text-ink"
        data-testid="resolved-context-summary"
      >
        {{ summary.total }}
        {{ summary.total === 1 ? 'context' : 'contexts' }}
        ·
        {{ summary.g }}G / {{ summary.p }}P / {{ summary.s }}S
      </span>
      <span v-else class="text-[11px] text-ink-dim">no contexts</span>
      <span v-if="loading" class="ml-auto text-[10px] text-ink-dim">…</span>
    </button>

    <div
      v-if="expanded"
      class="border-t border-border-muted px-3 py-2 space-y-3"
      data-testid="resolved-context-body"
    >
      <div
        v-if="errorMsg"
        class="rounded-sm border border-signal-danger bg-surface-0 px-2 py-1 font-ui text-[11px] text-signal-danger"
        role="alert"
      >
        {{ errorMsg }}
      </div>

      <template v-for="kind in ['global', 'project', 'session'] as const" :key="kind">
        <div
          v-if="grouped[kind].length > 0"
          :data-testid="`resolved-context-${kind}`"
        >
          <div class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle mb-1.5">
            {{ sectionLabels[kind] }}
            <span class="text-ink-dim">({{ grouped[kind].length }})</span>
          </div>
          <ul class="space-y-1.5">
            <li v-for="a in grouped[kind]" :key="a.id">
              <button
                type="button"
                class="block w-full text-left"
                :data-testid="`resolved-row-${a.id}`"
                @click="togglePreview(a.id)"
              >
                <AttachmentRow :attachment="a" :readonly="true" />
              </button>
              <div
                v-if="previewId === a.id"
                class="mt-1 rounded-sm border border-border-muted bg-surface-0 px-3 py-2"
                :data-testid="`resolved-preview-${a.id}`"
              >
                <pre
                  class="font-mono text-[11px] leading-relaxed text-ink whitespace-pre-wrap"
                >{{ a.content }}</pre>
              </div>
            </li>
          </ul>
        </div>
      </template>

      <p
        v-if="summary.total === 0 && !loading && !errorMsg"
        class="font-ui text-[11px] text-ink-dim"
        data-testid="resolved-context-empty"
      >
        No contexts attached at any scope.
      </p>

      <!-- Session-scope "New context folder" affordance (FR-021) -->
      <div class="pt-1 border-t border-border-muted" data-testid="session-new-folder-section">
        <div class="flex items-center justify-between">
          <span class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle">
            Session context
          </span>
          <button
            type="button"
            class="flex items-center gap-1 font-ui text-[11px] text-ink-dim hover:text-accent"
            data-testid="session-new-context-folder"
            @click.stop="beginCreateFolder"
          >
            <Plus :size="11" />
            <span>New folder</span>
          </button>
        </div>
        <!-- Inline name prompt -->
        <div
          v-if="creatingFolder"
          class="mt-1.5"
          data-testid="session-new-folder-row"
        >
          <input
            ref="newFolderInputRef"
            v-model="newFolderName"
            type="text"
            placeholder="Folder name…"
            aria-label="New session context folder name"
            spellcheck="false"
            autocomplete="off"
            class="w-full rounded-sm border border-border-muted bg-surface-0 px-2 py-1 font-ui text-[12px] text-ink focus:border-accent focus:outline-none"
            data-testid="session-new-folder-input"
            @keydown.enter.prevent="confirmCreateFolder"
            @keydown.esc.prevent="cancelCreateFolder"
            @blur="cancelCreateFolder"
          />
          <p class="mt-1 font-ui text-[10px] text-ink-subtle">
            Creates in context library and attaches at session scope · Enter to create, Esc to cancel
          </p>
          <p
            v-if="newFolderError"
            class="mt-1 font-ui text-[10px] text-signal-danger"
            data-testid="session-new-folder-error"
          >
            {{ newFolderError }}
          </p>
        </div>
      </div>
    </div>
  </section>
</template>
