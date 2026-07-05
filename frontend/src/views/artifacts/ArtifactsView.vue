<script setup lang="ts">
/**
 * ArtifactsView — global /artifacts surface (FR-009/FR-013).
 *
 * Lists every artifact across every session/project, with filter pills:
 *   - Scope:  All / Session / Project.
 *   - Source: All / code_block / tool_output / user_pin.
 *   - Mime:   All / text/ / image/ / etc.
 *
 * Click a row → ArtifactPreview modal (lazy-fetches bytes via
 * client.artifacts.get).
 *
 * The composable owns the active filter; pill clicks update it and
 * trigger a refresh.
 */

import { computed, onMounted, ref } from 'vue';
import CanvasHead from '@/shell/CanvasHead.vue';
import ArtifactPreview from './ArtifactPreview.vue';
import { useArtifacts, useHarnessClient } from '@/lib/useHarnessAPI';
import { useServedMode } from '@/lib/useServedMode';
import NotAvailableInServedMode from '@/components/ui/NotAvailableInServedMode.vue';
import type {
  Artifact,
  ArtifactFilter,
  ArtifactScope,
  ArtifactSource,
  ArtifactWithBytes,
} from '@/lib/types';

type ScopePill = 'all' | ArtifactScope;
type SourcePill = 'all' | ArtifactSource;

const SCOPES: readonly ScopePill[] = ['all', 'session', 'project'];
const SOURCES: readonly SourcePill[] = [
  'all',
  'code_block',
  'tool_output',
  'user_pin',
];
const MIME_PREFIXES: readonly { value: string; label: string }[] = [
  { value: '', label: 'All' },
  { value: 'text/', label: 'text/' },
  { value: 'image/', label: 'image/' },
  { value: 'application/', label: 'application/' },
];

const servedMode = useServedMode();
const client = useHarnessClient();
const {
  list,
  loading,
  error,
  refresh,
  setFilter,
} = useArtifacts({});

const scope = ref<ScopePill>('all');
const source = ref<SourcePill>('all');
const mimePrefix = ref<string>('');
type SortKey = 'createdAt' | 'title' | 'mimeType' | 'byteSize';
const sortKey = ref<SortKey>('createdAt');
const sortDir = ref<'asc' | 'desc'>('desc');

const previewOpen = ref(false);
const previewPayload = ref<ArtifactWithBytes | null>(null);
const previewProjectId = ref<string>('');

function buildFilter(): ArtifactFilter {
  const f: ArtifactFilter = {};
  if (scope.value !== 'all') f.scopeKind = scope.value;
  if (source.value !== 'all') f.source = source.value;
  if (mimePrefix.value) f.mimeTypePrefix = mimePrefix.value;
  return f;
}

async function applyFilter() {
  await setFilter(buildFilter());
}

function pickScope(s: ScopePill) {
  if (scope.value === s) return;
  scope.value = s;
  void applyFilter();
}

function pickSource(s: SourcePill) {
  if (source.value === s) return;
  source.value = s;
  void applyFilter();
}

function pickMime(value: string) {
  if (mimePrefix.value === value) return;
  mimePrefix.value = value;
  void applyFilter();
}

function toggleSort(next: SortKey) {
  if (sortKey.value === next) {
    sortDir.value = sortDir.value === 'asc' ? 'desc' : 'asc';
  } else {
    sortKey.value = next;
    sortDir.value = next === 'createdAt' ? 'desc' : 'asc';
  }
}

const sortedList = computed<readonly Artifact[]>(() => {
  const dir = sortDir.value === 'asc' ? 1 : -1;
  const key = sortKey.value;
  return [...list.value].sort((a, b) => {
    const av = (a[key] ?? '') as string | number;
    const bv = (b[key] ?? '') as string | number;
    if (typeof av === 'number' && typeof bv === 'number') {
      return (av - bv) * dir;
    }
    return String(av).localeCompare(String(bv)) * dir;
  });
});

function fmtTimestamp(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

function fmtSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const kb = bytes / 1024;
  if (kb < 1024) return kb < 10 ? `${kb.toFixed(1)} KB` : `${Math.round(kb)} KB`;
  const mb = kb / 1024;
  return mb < 10 ? `${mb.toFixed(1)} MB` : `${Math.round(mb)} MB`;
}

async function openPreview(a: Artifact) {
  previewProjectId.value = a.projectId ?? '';
  try {
    previewPayload.value = await client.artifacts.get(a.id);
    previewOpen.value = true;
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  }
}

function closePreview() {
  previewOpen.value = false;
  previewPayload.value = null;
}

async function onPromote(
  id: string,
  scopeKind: ArtifactScope,
  scopeId: string,
): Promise<Artifact> {
  const updated = await client.artifacts.promote(id, scopeKind, scopeId);
  await refresh();
  if (previewPayload.value && previewPayload.value.artifact.id === id) {
    previewPayload.value = {
      artifact: updated,
      bytes: previewPayload.value.bytes,
    };
  }
  return updated;
}

async function onDelete(id: string): Promise<void> {
  await client.artifacts.remove(id);
  await refresh();
}

onMounted(() => {
  void refresh();
});
</script>

<template>
  <NotAvailableInServedMode
    v-if="servedMode"
    feature="Artifacts"
  />
  <div
    v-else
    class="h-full flex flex-col"
    data-testid="artifacts-view"
  >
    <CanvasHead
      number="09"
      section="ARTIFACTS"
      title="Artifacts"
      subtitle="Code blocks, tool outputs, and pinned snippets — captured automatically and addressable across sessions."
    />

    <div class="px-6 py-4 space-y-3">
      <!-- filter pills -->
      <div class="flex flex-wrap items-center gap-3">
        <div class="flex items-center gap-1" data-testid="artifacts-filter-scope">
          <span
            class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle"
          >
            Scope
          </span>
          <button
            v-for="s in SCOPES"
            :key="s"
            type="button"
            class="rounded-sm border px-2 py-0.5 font-ui text-[11px]"
            :class="
              scope === s
                ? 'border-accent bg-surface-2 text-accent'
                : 'border-border-muted bg-surface-1 text-ink-muted hover:bg-surface-2 hover:text-ink'
            "
            :data-testid="`artifacts-pill-scope-${s}`"
            @click="pickScope(s)"
          >
            {{ s }}
          </button>
        </div>
        <div class="flex items-center gap-1" data-testid="artifacts-filter-source">
          <span
            class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle"
          >
            Source
          </span>
          <button
            v-for="s in SOURCES"
            :key="s"
            type="button"
            class="rounded-sm border px-2 py-0.5 font-ui text-[11px]"
            :class="
              source === s
                ? 'border-accent bg-surface-2 text-accent'
                : 'border-border-muted bg-surface-1 text-ink-muted hover:bg-surface-2 hover:text-ink'
            "
            :data-testid="`artifacts-pill-source-${s}`"
            @click="pickSource(s)"
          >
            {{ s }}
          </button>
        </div>
        <label class="flex items-center gap-2">
          <span
            class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle"
          >
            Mime
          </span>
          <select
            v-model="mimePrefix"
            class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1 font-ui text-[11px] text-ink"
            data-testid="artifacts-filter-mime"
            @change="pickMime(mimePrefix)"
          >
            <option v-for="m in MIME_PREFIXES" :key="m.value" :value="m.value">
              {{ m.label }}
            </option>
          </select>
        </label>
      </div>

      <div
        v-if="error"
        class="rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[11px] text-signal-danger"
        role="alert"
        data-testid="artifacts-error"
      >
        {{ error }}
      </div>
    </div>

    <div class="flex-1 overflow-y-auto px-6 pb-6">
      <div
        v-if="loading"
        class="font-ui text-xs text-ink-muted"
        data-testid="artifacts-loading"
      >
        Loading…
      </div>
      <table
        v-else-if="sortedList.length > 0"
        class="w-full text-left font-ui text-xs"
        data-testid="artifacts-table"
      >
        <thead>
          <tr
            class="text-[10px] uppercase tracking-[0.18em] text-ink-subtle"
          >
            <th class="px-3 py-1.5 font-medium">Source</th>
            <th
              class="px-3 py-1.5 font-medium cursor-pointer"
              data-testid="artifacts-sort-title"
              @click="toggleSort('title')"
            >
              Title
            </th>
            <th
              class="px-3 py-1.5 font-medium cursor-pointer"
              data-testid="artifacts-sort-mime"
              @click="toggleSort('mimeType')"
            >
              Mime
            </th>
            <th
              class="px-3 py-1.5 font-medium cursor-pointer"
              data-testid="artifacts-sort-size"
              @click="toggleSort('byteSize')"
            >
              Size
            </th>
            <th class="px-3 py-1.5 font-medium">Scope</th>
            <th
              class="px-3 py-1.5 font-medium cursor-pointer"
              data-testid="artifacts-sort-createdAt"
              @click="toggleSort('createdAt')"
            >
              Created
            </th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="a in sortedList"
            :key="a.id"
            class="cursor-pointer hover:bg-surface-2"
            :data-testid="`artifacts-row-${a.id}`"
            @click="openPreview(a)"
          >
            <td class="px-3 py-2 text-ink-dim font-mono text-[11px]">
              {{ a.source }}
            </td>
            <td class="px-3 py-2 text-ink truncate max-w-[40ch]">
              {{ a.title }}
            </td>
            <td class="px-3 py-2 text-ink-muted font-mono text-[11px]">
              {{ a.mimeType }}
            </td>
            <td class="px-3 py-2 text-ink-muted font-mono text-[11px]">
              {{ fmtSize(a.byteSize) }}
            </td>
            <td class="px-3 py-2 text-ink-muted">{{ a.scopeKind }}</td>
            <td class="px-3 py-2 text-ink-dim font-mono text-[11px]">
              {{ fmtTimestamp(a.createdAt) }}
            </td>
          </tr>
        </tbody>
      </table>
      <p
        v-else
        class="font-ui text-xs text-ink-muted"
        data-testid="artifacts-empty"
      >
        No artifacts match these filters.
      </p>
    </div>

    <ArtifactPreview
      :open="previewOpen"
      :payload="previewPayload"
      :project-id="previewProjectId"
      :on-promote="onPromote"
      :on-delete="onDelete"
      @close="closePreview"
    />
  </div>
</template>
