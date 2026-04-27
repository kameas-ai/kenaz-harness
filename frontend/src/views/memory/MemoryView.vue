<script setup lang="ts">
/**
 * MemoryView — long-term memory management surface (NN/SECTION pattern).
 *
 * Lists every persisted chunk, lets the user prune what they no longer
 * want the harness to remember, and surfaces the empty state when the
 * store is fresh. Privacy: chunks live on disk under
 * <DataDir>/memory.gob; this view is the user's prune-knob for that
 * file.
 *
 * WP06 T005 additions:
 *   - Scope filter pill row (All / Global / Project / Session) calls
 *     `client.memory.listChunks({scopeKind: ...})`.
 *   - Per-row scope badge (🌐 / 📁 / 💬) so the user can see at a glance
 *     where each chunk lives.
 *   - "Promote scope" action opens a small target picker that resolves
 *     the new scope id (project → session's projectId; global → ""),
 *     confirms with the user, then calls `client.memory.promoteScope`.
 *     Move semantics: backend deletes the original row and inserts a
 *     new one with a new ID; we just refresh.
 *   - "Forget at scope" reuses the existing forget RPC.
 */
import { computed, onMounted, ref } from 'vue';
import { useRoute } from 'vue-router';
import CanvasHead from '@/shell/CanvasHead.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type {
  MemoryChunk,
  MemoryListFilter,
  MemoryScopeKind,
} from '@/lib/types';

const client = useHarnessClient();
const route = useRoute();

type FilterPill = 'all' | MemoryScopeKind;

function isScopeKind(v: unknown): v is MemoryScopeKind {
  return v === 'global' || v === 'project' || v === 'session';
}

interface PromoteState {
  chunk: MemoryChunk;
  target: MemoryScopeKind;
  newScopeID: string;
  resolving: boolean;
  error: string | null;
}

const chunks = ref<readonly MemoryChunk[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);
const activeFilter = ref<FilterPill>('all');
const openMenuId = ref<string | null>(null);
const promote = ref<PromoteState | null>(null);

const filterPills: readonly { id: FilterPill; label: string; glyph: string }[] = [
  { id: 'all', label: 'All', glyph: '∗' },
  { id: 'global', label: 'Global', glyph: '🌐' },
  { id: 'project', label: 'Project', glyph: '📁' },
  { id: 'session', label: 'Session', glyph: '💬' },
];

function buildFilter(pill: FilterPill): MemoryListFilter {
  if (pill === 'all') return {};
  const filter: MemoryListFilter = { scopeKind: pill };
  // The /memory route accepts a `scopeId` query param so a project
  // landing page can deep-link the user into the project-scoped view.
  // The harness backend ignores scopeId for global filters; we still
  // forward it for project / session so the row count matches the
  // ProjectLandingPage's preview (WP07 T002).
  const sid = route?.query?.scopeId;
  if (typeof sid === 'string' && sid.length > 0 && pill !== 'global') {
    filter.scopeId = sid;
  }
  return filter;
}

async function refresh() {
  loading.value = true;
  error.value = null;
  try {
    chunks.value = await client.memory.listChunks(
      buildFilter(activeFilter.value),
    );
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
    chunks.value = [];
  } finally {
    loading.value = false;
  }
}

async function setFilter(pill: FilterPill) {
  if (activeFilter.value === pill) return;
  activeFilter.value = pill;
  await refresh();
}

async function forget(id: string) {
  try {
    await client.memory.forget(id);
    await refresh();
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
}

// Bundle E WP16 — pin / unpin a chunk so the prune sweep skips it.
async function togglePin(chunk: MemoryChunk) {
  try {
    const next = !chunk.pinned;
    await client.memory.pin(chunk.id, next);
    await refresh();
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
}

// Bundle E WP15 — prune controls.
const pruneRunning = ref(false);
const pruneLastResult = ref<string | null>(null);

async function runPruneNow() {
  pruneRunning.value = true;
  pruneLastResult.value = null;
  try {
    const scope = activeFilter.value === 'all' ? '' : activeFilter.value;
    const stats = await client.memory.runPruneNow(scope);
    pruneLastResult.value =
      `kept ${stats.kept} · dropped ${stats.dropped} · ` +
      `collapsed ${stats.collapsed} · pinned ${stats.pinned}`;
    await refresh();
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    pruneRunning.value = false;
  }
}

function toggleMenu(id: string) {
  openMenuId.value = openMenuId.value === id ? null : id;
}

function closeMenu() {
  openMenuId.value = null;
}

const promotionTargets = computed(() => {
  return (chunk: MemoryChunk): readonly MemoryScopeKind[] => {
    if (chunk.scopeKind === 'session') {
      const targets: MemoryScopeKind[] = [];
      if ((chunk.projectId ?? '').length > 0) targets.push('project');
      targets.push('global');
      return targets;
    }
    if (chunk.scopeKind === 'project') return ['global'];
    return [];
  };
});

async function resolveScopeID(
  chunk: MemoryChunk,
  target: MemoryScopeKind,
): Promise<string> {
  if (target === 'global') return '';
  if (target === 'project') {
    if ((chunk.projectId ?? '').length > 0) {
      return chunk.projectId as string;
    }
    if ((chunk.sessionId ?? '').length > 0) {
      const sess = await client.sessions.get(chunk.sessionId as string);
      return sess.projectId ?? '';
    }
    return '';
  }
  // target === 'session' — promotion never targets session, but keep
  // the resolver total: fall back to the chunk's own session id.
  return chunk.sessionId ?? '';
}

async function startPromote(chunk: MemoryChunk, target: MemoryScopeKind) {
  closeMenu();
  promote.value = {
    chunk,
    target,
    newScopeID: '',
    resolving: true,
    error: null,
  };
  try {
    const id = await resolveScopeID(chunk, target);
    if (target === 'project' && id === '') {
      promote.value = {
        ...promote.value,
        resolving: false,
        error: 'Could not resolve project for this chunk.',
      };
      return;
    }
    promote.value = {
      ...promote.value,
      newScopeID: id,
      resolving: false,
    };
  } catch (err) {
    promote.value = {
      ...promote.value,
      resolving: false,
      error: err instanceof Error ? err.message : String(err),
    };
  }
}

async function confirmPromote() {
  const state = promote.value;
  if (!state || state.resolving) return;
  if (state.target === 'project' && state.newScopeID === '') return;
  try {
    await client.memory.promoteScope(
      state.chunk.id,
      state.target,
      state.newScopeID,
    );
    promote.value = null;
    await refresh();
  } catch (err) {
    promote.value = {
      ...state,
      error: err instanceof Error ? err.message : String(err),
    };
  }
}

function cancelPromote() {
  promote.value = null;
}

function preview(content: string): string {
  if (content.length <= 200) return content;
  return content.slice(0, 200) + '…';
}

function shortLabel(chunk: MemoryChunk): string {
  if (chunk.title && chunk.title.length > 0) return chunk.title;
  const first = chunk.content.replace(/\s+/g, ' ').trim();
  if (first.length <= 60) return first;
  return first.slice(0, 60) + '…';
}

function scopeGlyph(kind: MemoryScopeKind): string {
  if (kind === 'global') return '🌐';
  if (kind === 'project') return '📁';
  return '💬';
}

function scopeLabel(kind: MemoryScopeKind): string {
  return kind;
}

function formatTimestamp(iso: string): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

onMounted(() => {
  // Honour ?scopeKind=… query so deep-links from the project landing
  // page land on the right pill (WP07 T002).
  const qk = route?.query?.scopeKind;
  if (isScopeKind(qk)) {
    activeFilter.value = qk;
  }
  void refresh();
});

defineExpose({ refresh });
</script>

<template>
  <div>
    <CanvasHead
      number="07"
      section="MEMORY"
      title="Long-term memory"
      subtitle="Every snippet you have asked the harness to remember. These are pulled into future conversations across all sessions."
    />

    <div class="px-6 py-4 max-w-4xl">
      <!-- Scope filter pills (WP06 T005) -->
      <div
        class="mb-4 flex flex-wrap gap-2"
        role="tablist"
        aria-label="Filter memories by scope"
        data-testid="memory-scope-filter"
      >
        <button
          v-for="pill in filterPills"
          :key="pill.id"
          type="button"
          role="tab"
          :aria-selected="activeFilter === pill.id"
          :data-testid="`memory-scope-pill-${pill.id}`"
          class="px-3 py-1 rounded-sm border text-[11px] uppercase tracking-[0.18em] font-ui"
          :class="
            activeFilter === pill.id
              ? 'border-accent text-accent bg-surface-2'
              : 'border-border-muted text-ink-dim hover:bg-surface-2'
          "
          @click="setFilter(pill.id)"
        >
          <span class="mr-1" aria-hidden="true">{{ pill.glyph }}</span>
          {{ pill.label }}
        </button>
      </div>

      <!-- Prune controls (Bundle E WP15) -->
      <div
        class="mb-3 flex flex-wrap items-center gap-2"
        data-testid="memory-prune-controls"
      >
        <button
          type="button"
          class="px-3 py-1 rounded-sm border border-border-muted font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2 disabled:opacity-50"
          :disabled="pruneRunning"
          data-testid="memory-prune-now"
          @click="runPruneNow"
        >
          {{ pruneRunning ? 'Pruning…' : 'Prune now' }}
        </button>
        <span
          v-if="pruneLastResult"
          class="font-mono text-[11px] text-ink-subtle"
          data-testid="memory-prune-result"
        >
          {{ pruneLastResult }}
        </span>
      </div>

      <div
        v-if="error"
        class="mb-3 rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
      >
        {{ error }}
      </div>

      <div
        v-if="loading"
        class="font-ui text-[12px] text-ink-muted"
        role="status"
      >
        Loading memories…
      </div>

      <div
        v-else-if="chunks.length === 0"
        class="rounded-md border border-border-muted bg-surface-1 px-4 py-6 text-center"
        data-testid="memory-empty-state"
      >
        <p class="font-ui text-sm text-ink">
          No memories saved yet. Hit the 📌 button on a message to start.
        </p>
      </div>

      <ul v-else class="space-y-2" data-testid="memory-chunk-list">
        <li
          v-for="chunk in chunks"
          :key="chunk.id"
          class="relative rounded-md border border-border-muted bg-surface-1 px-4 py-3"
          :data-testid="`memory-chunk-${chunk.id}`"
        >
          <div class="flex flex-wrap items-baseline gap-3">
            <span
              class="font-ui text-[10px] uppercase tracking-[0.18em] px-1.5 py-0.5 rounded-sm border border-border-muted text-ink-dim"
              :data-testid="`memory-scope-badge-${chunk.id}`"
            >
              <span aria-hidden="true">{{ scopeGlyph(chunk.scopeKind) }}</span>
              {{ scopeLabel(chunk.scopeKind) }}
            </span>
            <span class="font-mono text-[10px] uppercase tracking-[0.18em] text-ink-subtle">
              {{ formatTimestamp(chunk.createdAt) }}
            </span>
            <span
              v-if="chunk.sessionId"
              class="font-mono text-[10px] text-ink-dim"
            >
              session {{ chunk.sessionId }}
            </span>
            <span
              v-if="chunk.sourceTurn"
              class="font-mono text-[10px] text-ink-dim"
            >
              · {{ chunk.sourceTurn }}
            </span>
            <div class="ml-auto flex items-center gap-2">
              <div class="relative">
                <button
                  type="button"
                  class="px-2 py-1 rounded-sm border border-border-muted text-[10px] uppercase tracking-[0.18em] text-ink-dim hover:text-accent hover:bg-surface-2 disabled:opacity-50 disabled:cursor-not-allowed"
                  :disabled="promotionTargets(chunk).length === 0"
                  :data-testid="`memory-promote-${chunk.id}`"
                  :aria-haspopup="'menu'"
                  :aria-expanded="openMenuId === chunk.id"
                  @click="toggleMenu(chunk.id)"
                >
                  Promote scope
                </button>
                <div
                  v-if="openMenuId === chunk.id"
                  class="absolute right-0 top-full z-20 mt-1 w-48 rounded-sm border border-border-muted bg-surface-1 shadow-lg"
                  role="menu"
                  :data-testid="`memory-promote-menu-${chunk.id}`"
                >
                  <button
                    v-for="target in promotionTargets(chunk)"
                    :key="target"
                    type="button"
                    role="menuitem"
                    class="block w-full px-3 py-1.5 text-left font-ui text-[12px] text-ink hover:bg-surface-2 hover:text-accent"
                    :data-testid="`memory-promote-${chunk.id}-${target}`"
                    @click="startPromote(chunk, target)"
                  >
                    <span class="mr-2" aria-hidden="true">{{ scopeGlyph(target) }}</span>
                    Promote to {{ scopeLabel(target) }}
                  </button>
                </div>
              </div>
              <button
                type="button"
                class="px-2 py-1 rounded-sm border border-border-muted text-[10px] uppercase tracking-[0.18em] text-ink-dim hover:text-accent hover:bg-surface-2"
                :data-testid="`memory-pin-${chunk.id}`"
                @click="togglePin(chunk)"
              >
                {{ chunk.pinned ? 'Unpin' : 'Pin' }}
              </button>
              <button
                type="button"
                class="px-2 py-1 rounded-sm border border-border-muted text-[10px] uppercase tracking-[0.18em] text-ink-dim hover:text-signal-danger hover:bg-surface-2"
                :data-testid="`memory-forget-${chunk.id}`"
                @click="forget(chunk.id)"
              >
                Forget
              </button>
            </div>
          </div>
          <p class="mt-2 font-ui text-sm text-ink whitespace-pre-wrap">
            {{ preview(chunk.content) }}
          </p>
        </li>
      </ul>
    </div>

    <!-- Promotion confirmation modal -->
    <div
      v-if="promote"
      class="fixed inset-0 z-40 grid place-items-center bg-surface-0/70"
      role="dialog"
      aria-modal="true"
      aria-labelledby="memory-promote-title"
      data-testid="memory-promote-modal"
    >
      <div class="w-[28rem] max-w-[92vw] rounded-md border border-border-muted bg-surface-1 p-4 shadow-lg">
        <h3
          id="memory-promote-title"
          class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
        >
          Promote scope
        </h3>
        <p class="mt-2 font-ui text-sm text-ink">
          Promote
          <span class="font-mono text-[12px]">{{ shortLabel(promote.chunk) }}</span>
          from {{ scopeLabel(promote.chunk.scopeKind) }} to {{ scopeLabel(promote.target) }}?
          The original entry will be deleted and re-inserted at the new scope.
        </p>
        <p
          v-if="promote.error"
          class="mt-2 font-ui text-[12px] text-signal-danger"
          role="alert"
        >
          {{ promote.error }}
        </p>
        <p
          v-if="promote.resolving"
          class="mt-2 font-ui text-[12px] text-ink-muted"
          role="status"
        >
          Resolving target scope…
        </p>
        <div class="mt-4 flex justify-end gap-2">
          <button
            type="button"
            class="px-3 py-1 rounded-sm border border-border-muted font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2"
            data-testid="memory-promote-cancel"
            @click="cancelPromote"
          >
            Cancel
          </button>
          <button
            type="button"
            class="px-3 py-1 rounded-sm border border-accent font-ui text-[11px] uppercase tracking-[0.18em] text-accent hover:bg-surface-2 disabled:opacity-50 disabled:cursor-not-allowed"
            :disabled="
              promote.resolving ||
              (promote.target === 'project' && promote.newScopeID === '')
            "
            data-testid="memory-promote-confirm"
            @click="confirmPromote"
          >
            Promote
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
