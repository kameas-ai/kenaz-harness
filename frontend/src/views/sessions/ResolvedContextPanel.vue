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

import { computed, ref, watch } from 'vue';
import { ChevronRight } from '@/shell/icons';
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
    </div>
  </section>
</template>
