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
 *
 * v0.5.3 cherry-picks (memory-inspection-ui-01KX5R8E):
 *   §2.4 — "Health" tab with MemoryHealthPanel.
 *   §2.5 — PrunePreviewModal shown before "Prune now" confirms.
 *
 * memory-narrative-layer-01KQ8TD1 additions (WP07):
 *   - Per-chunk Kind badge (raw / narrative_extractive / narrative_synthesised)
 *     so the user can see at a glance what type of memory each chunk is.
 *   - "Mark important" button calls markImportant() to raise the chunk's
 *     promotion score (userPins counter).
 *   - "N narratives unrecoverable" banner when narrativeFailedCount > 0,
 *     with a per-job "Retry" action and a "Retry all" affordance.
 */
import { computed, onMounted, ref } from 'vue';
import { useRoute } from 'vue-router';
import CanvasHead from '@/shell/CanvasHead.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { useServedMode } from '@/lib/useServedMode';
import NotAvailableInServedMode from '@/components/ui/NotAvailableInServedMode.vue';
import type {
  MemoryChunk,
  MemoryListFilter,
  MemoryPrunePreview,
  MemoryScopeKind,
  NarrativeJobStatus,
  RetrievalReport,
  ScoredChunk,
} from '@/lib/types';
import MemoryHealthPanel from './MemoryHealthPanel.vue';
import PrunePreviewModal from './PrunePreviewModal.vue';
import ProvenanceDrawer from './ProvenanceDrawer.vue';
import type { ChunkProvenance } from '@/lib/types';

const servedMode = useServedMode();
const client = useHarnessClient();
const route = useRoute();

// §2.4 — top-level tab: "Chunks" | "Health" | "Retrieval inspector"
type MainTab = 'chunks' | 'health' | 'retrieval';
const activeMainTab = ref<MainTab>('chunks');

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
// §2.5 extension: "Prune now" now first fetches a preview and shows the
// confirmation modal. The user reviews the would-prune list before the
// sweep runs.
const pruneRunning = ref(false);
const pruneLastResult = ref<string | null>(null);
const prunePreview = ref<MemoryPrunePreview | null>(null);
const pruneModalRunning = ref(false);

async function openPrunePreview() {
  pruneRunning.value = true;
  pruneLastResult.value = null;
  error.value = null;
  try {
    const scope = activeFilter.value === 'all' ? '' : activeFilter.value;
    const preview = await client.memory.prunePreview(scope);
    prunePreview.value = preview;
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    pruneRunning.value = false;
  }
}

async function confirmPruneNow() {
  if (!prunePreview.value) return;
  pruneModalRunning.value = true;
  try {
    const scope = activeFilter.value === 'all' ? '' : activeFilter.value;
    const stats = await client.memory.runPruneNow(scope);
    pruneLastResult.value =
      `kept ${stats.kept} · dropped ${stats.dropped} · ` +
      `collapsed ${stats.collapsed} · pinned ${stats.pinned}`;
    prunePreview.value = null;
    await refresh();
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    pruneModalRunning.value = false;
  }
}

function cancelPrunePreview() {
  prunePreview.value = null;
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

// ── Narrative layer (memory-narrative-layer-01KQ8TD1 WP07) ──────────────

// Failed narrative jobs banner.
const narrativeFailedCount = ref(0);
const narrativeFailedJobs = ref<NarrativeJobStatus[]>([]);
const narrativeBannerOpen = ref(false);
const narrativeRetrying = ref<string | null>(null); // jobID being retried

async function refreshNarrativeFailed() {
  try {
    narrativeFailedCount.value = await client.memory.narrativeFailedCount();
    if (narrativeBannerOpen.value && narrativeFailedCount.value > 0) {
      narrativeFailedJobs.value = await client.memory.narrativeFailedList();
    } else if (narrativeFailedCount.value === 0) {
      narrativeBannerOpen.value = false;
      narrativeFailedJobs.value = [];
    }
  } catch {
    // Best-effort: the narrative subsystem may not be wired yet.
  }
}

async function openNarrativeBanner() {
  narrativeBannerOpen.value = true;
  try {
    narrativeFailedJobs.value = await client.memory.narrativeFailedList();
  } catch {
    narrativeFailedJobs.value = [];
  }
}

async function retryNarrativeJob(jobID: string) {
  narrativeRetrying.value = jobID;
  try {
    await client.memory.retryFailedNarrative(jobID);
    await refreshNarrativeFailed();
    if (narrativeBannerOpen.value) {
      narrativeFailedJobs.value = await client.memory.narrativeFailedList();
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    narrativeRetrying.value = null;
  }
}

async function retryAllNarrativeJobs() {
  for (const job of narrativeFailedJobs.value) {
    await retryNarrativeJob(job.id);
  }
}

// "Mark important" — increments the user_pins promotion counter.
const markingImportant = ref<string | null>(null); // chunkID being marked

async function toggleMarkImportant(chunk: MemoryChunk) {
  if (markingImportant.value === chunk.id) return;
  markingImportant.value = chunk.id;
  try {
    // "Mark important" is a toggle: currently pinned → unpin; else pin.
    // We reuse the narrative userPins path, not the prune-pin path.
    // The chunk doesn't carry a "narrativeImportant" field in the UI yet,
    // so we always set pinned=true on first click. A future iteration can
    // expose a toggle state.
    await client.memory.markImportant(chunk.id, true);
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    markingImportant.value = null;
  }
}

// ── Provenance Drawer (memory-inspection-ui-01KX5R8E WP08) ──────────────

const provenanceDrawerOpen = ref(false);
const provenanceChunk = ref<ChunkProvenance | null>(null);
const provenanceLoading = ref(false);
const provenanceError = ref<string | null>(null);

async function openProvenance(chunkID: string) {
  provenanceDrawerOpen.value = true;
  provenanceLoading.value = true;
  provenanceError.value = null;
  provenanceChunk.value = null;
  try {
    provenanceChunk.value = await client.memory.getChunkProvenance(chunkID);
  } catch (err) {
    provenanceError.value = err instanceof Error ? err.message : String(err);
  } finally {
    provenanceLoading.value = false;
  }
}

function closeProvenance() {
  provenanceDrawerOpen.value = false;
  provenanceChunk.value = null;
  provenanceError.value = null;
}

// Kind badge helpers.
function kindLabel(kind: string | undefined): string {
  switch (kind) {
    case 'narrative_synthesised': return 'narrative';
    case 'narrative_extractive': return 'extractive';
    case 'narrative_extractive_fallback': return 'fallback';
    case 'raw':
    default: return 'raw';
  }
}

function kindClass(kind: string | undefined): string {
  switch (kind) {
    case 'narrative_synthesised': return 'text-accent border-accent';
    case 'narrative_extractive': return 'text-signal-success border-signal-success';
    case 'narrative_extractive_fallback': return 'text-signal-warning border-signal-warning';
    default: return 'text-ink-dim border-border-muted';
  }
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

// ── Capstone: Retrieval Inspector + Embedding Probe (memory-inspection-ui-01KX5R8E WP07) ──

// Active-session retrieval inspector (§2.1).
const retrievalReport = ref<RetrievalReport | null>(null);
const retrievalLoading = ref(false);
const retrievalError = ref<string | null>(null);
const retrievalSessionID = ref('');

async function loadRetrieval() {
  retrievalLoading.value = true;
  retrievalError.value = null;
  try {
    const sid = retrievalSessionID.value.trim();
    retrievalReport.value = await client.memory.lastRetrieval(sid);
  } catch (err) {
    retrievalError.value = err instanceof Error ? err.message : String(err);
  } finally {
    retrievalLoading.value = false;
  }
}

// Embedding probe (§2.2).
const probeQuery = ref('');
const probeResults = ref<ScoredChunk[]>([]);
const probeLoading = ref(false);
const probeError = ref<string | null>(null);

async function runProbe() {
  if (!probeQuery.value.trim()) return;
  probeLoading.value = true;
  probeError.value = null;
  probeResults.value = [];
  try {
    probeResults.value = await client.memory.embeddingProbe(probeQuery.value, 10);
  } catch (err) {
    probeError.value = err instanceof Error ? err.message : String(err);
  } finally {
    probeLoading.value = false;
  }
}

function formatSimilarity(v: number): string {
  return v.toFixed(2);
}

onMounted(() => {
  // Honour ?scopeKind=… query so deep-links from the project landing
  // page land on the right pill (WP07 T002).
  const qk = route?.query?.scopeKind;
  if (isScopeKind(qk)) {
    activeFilter.value = qk;
  }
  void refresh();
  void refreshNarrativeFailed();
});

defineExpose({ refresh });
</script>

<template>
  <NotAvailableInServedMode
    v-if="servedMode"
    feature="Memory"
  />
  <div v-else>
    <CanvasHead
      number="07"
      section="MEMORY"
      title="Long-term memory"
      subtitle="Every snippet you have asked the harness to remember. These are pulled into future conversations across all sessions."
    />

    <!-- §2.4 — Main tab bar: Chunks / Health / Retrieval -->
    <div
      class="flex border-b border-border-muted px-6"
      role="tablist"
      aria-label="Memory view tabs"
      data-testid="memory-main-tabs"
    >
      <button
        v-for="tab in [
          { id: 'chunks', label: 'Chunks' },
          { id: 'health', label: 'Health' },
          { id: 'retrieval', label: 'Retrieval inspector' },
        ]"
        :key="tab.id"
        type="button"
        role="tab"
        :aria-selected="activeMainTab === tab.id"
        :data-testid="`memory-main-tab-${tab.id}`"
        class="px-4 py-2 font-ui text-[11px] uppercase tracking-[0.18em] border-b-2 -mb-px transition-colors"
        :class="
          activeMainTab === tab.id
            ? 'border-accent text-accent'
            : 'border-transparent text-ink-dim hover:text-ink'
        "
        @click="activeMainTab = tab.id as MainTab"
      >
        {{ tab.label }}
      </button>
    </div>

    <!-- §2.4 — Health tab panel -->
    <MemoryHealthPanel
      v-if="activeMainTab === 'health'"
      data-testid="memory-health-tab-panel"
    />

    <!-- §2.1 + §2.2 — Retrieval inspector tab panel (capstone) -->
    <div
      v-if="activeMainTab === 'retrieval'"
      class="px-6 py-4 max-w-4xl"
      data-testid="memory-retrieval-tab-panel"
    >
      <!-- Last retrieval section -->
      <section class="mb-6">
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle mb-2">
          Last turn retrieval
        </h2>
        <div class="mb-3 flex items-center gap-2">
          <input
            v-model="retrievalSessionID"
            type="text"
            aria-label="Session ID for retrieval"
            placeholder="Session ID (leave empty for active session)"
            class="flex-1 rounded-sm border border-border-muted bg-surface-0 px-3 py-1.5 font-mono text-[12px] text-ink placeholder:text-ink-dim focus:border-accent focus:outline-none"
            data-testid="memory-retrieval-session-input"
            @keyup.enter="loadRetrieval"
          />
          <button
            type="button"
            class="px-3 py-1.5 rounded-sm border border-border-muted font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2 disabled:opacity-50"
            :disabled="retrievalLoading"
            data-testid="memory-retrieval-load"
            @click="loadRetrieval"
          >
            {{ retrievalLoading ? 'Loading…' : 'Load' }}
          </button>
        </div>
        <div
          v-if="retrievalError"
          class="mb-2 rounded-sm border border-signal-danger px-3 py-2 font-ui text-[12px] text-signal-danger"
          role="alert"
          data-testid="memory-retrieval-error"
        >
          {{ retrievalError }}
        </div>
        <!-- Empty state -->
        <div
          v-if="!retrievalLoading && retrievalReport !== null && !retrievalReport.query"
          class="rounded-md border border-border-muted bg-surface-1 px-4 py-6 text-center"
          data-testid="memory-retrieval-empty"
        >
          <p class="font-ui text-sm text-ink-dim">
            No retrievals recorded for this session yet. Memory retrieval data is
            captured after the first prompt is sent.
          </p>
        </div>
        <!-- Retrieval report -->
        <div
          v-else-if="retrievalReport && retrievalReport.query"
          data-testid="memory-retrieval-report"
        >
          <div class="mb-2 font-ui text-[12px]">
            <span class="text-ink-dim">Query: </span>
            <span class="font-mono text-ink" data-testid="memory-retrieval-query">{{ retrievalReport.query }}</span>
          </div>
          <!-- Injected chunks -->
          <div class="mb-3">
            <p class="font-ui text-[10px] uppercase tracking-[0.18em] text-signal-success mb-1">
              Injected into context
            </p>
            <ul class="space-y-1" data-testid="memory-retrieval-injected">
              <li
                v-for="r in retrievalReport.results.filter(r => r.injected)"
                :key="r.chunk.id"
                class="rounded-sm border-l-2 border-signal-success bg-surface-1 px-3 py-2"
                :data-testid="`memory-retrieval-injected-${r.chunk.id}`"
              >
                <div class="flex items-baseline gap-2 mb-1">
                  <span class="font-mono text-[11px] font-bold text-signal-success">
                    {{ formatSimilarity(r.similarity) }}
                  </span>
                  <span
                    v-if="r.chunk.kind"
                    class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim"
                  >
                    {{ r.chunk.kind }}
                  </span>
                  <span
                    v-if="r.chunk.pinned"
                    class="font-ui text-[10px] uppercase tracking-[0.18em] text-accent"
                    title="Pinned"
                  >★ pinned</span>
                </div>
                <p class="font-ui text-[12px] text-ink line-clamp-2">
                  {{ r.chunk.content }}
                </p>
              </li>
              <li
                v-if="retrievalReport.results.filter(r => r.injected).length === 0"
                class="font-ui text-[12px] text-ink-dim"
              >
                No chunks were injected.
              </li>
            </ul>
          </div>
          <!-- Below-threshold chunks -->
          <div>
            <p class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim mb-1">
              Below threshold (not injected, threshold {{ formatSimilarity(retrievalReport.threshold) }})
            </p>
            <ul class="space-y-1" data-testid="memory-retrieval-below">
              <li
                v-for="r in retrievalReport.results.filter(r => !r.injected)"
                :key="r.chunk.id"
                class="rounded-sm border-l-2 border-border-muted bg-surface-1 px-3 py-2 opacity-70"
                :data-testid="`memory-retrieval-below-${r.chunk.id}`"
              >
                <div class="flex items-baseline gap-2 mb-1">
                  <span class="font-mono text-[11px] text-ink-dim">
                    {{ formatSimilarity(r.similarity) }}
                  </span>
                  <span
                    v-if="r.chunk.kind"
                    class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim"
                  >
                    {{ r.chunk.kind }}
                  </span>
                </div>
                <p class="font-ui text-[12px] text-ink-dim line-clamp-1">
                  {{ r.chunk.content }}
                </p>
              </li>
              <li
                v-if="retrievalReport.results.filter(r => !r.injected).length === 0"
                class="font-ui text-[12px] text-ink-dim"
              >
                All candidates were above the threshold.
              </li>
            </ul>
          </div>
        </div>
      </section>

      <!-- Embedding probe section (§2.2) -->
      <section>
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle mb-2">
          Embedding probe
        </h2>
        <p class="font-ui text-[12px] text-ink-dim mb-3">
          Enter any text to see which memory chunks it would retrieve, ranked by cosine similarity. Useful for debugging "why didn't the model recall X?"
        </p>
        <div class="mb-3 flex items-center gap-2">
          <input
            v-model="probeQuery"
            type="text"
            aria-label="Memory probe query"
            placeholder="Enter probe query…"
            class="flex-1 rounded-sm border border-border-muted bg-surface-0 px-3 py-1.5 font-mono text-[12px] text-ink placeholder:text-ink-dim focus:border-accent focus:outline-none"
            data-testid="memory-probe-input"
            @keyup.enter="runProbe"
          />
          <button
            type="button"
            class="px-3 py-1.5 rounded-sm border border-accent font-ui text-[11px] uppercase tracking-[0.18em] text-accent hover:bg-surface-2 disabled:opacity-50"
            :disabled="probeLoading || !probeQuery.trim()"
            data-testid="memory-probe-run"
            @click="runProbe"
          >
            {{ probeLoading ? 'Probing…' : 'Probe' }}
          </button>
        </div>
        <div
          v-if="probeError"
          class="mb-2 rounded-sm border border-signal-danger px-3 py-2 font-ui text-[12px] text-signal-danger"
          role="alert"
          data-testid="memory-probe-error"
        >
          {{ probeError }}
        </div>
        <ul
          v-if="probeResults.length > 0"
          class="space-y-1"
          data-testid="memory-probe-results"
        >
          <li
            v-for="r in probeResults"
            :key="r.chunk.id"
            class="rounded-sm border border-border-muted bg-surface-1 px-3 py-2"
            :data-testid="`memory-probe-result-${r.chunk.id}`"
          >
            <div class="flex items-baseline gap-2 mb-1">
              <span class="font-mono text-[11px] font-bold text-accent">
                {{ formatSimilarity(r.similarity) }}
              </span>
              <span
                v-if="r.chunk.kind"
                class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim"
              >
                {{ r.chunk.kind }}
              </span>
            </div>
            <p class="font-ui text-[12px] text-ink line-clamp-2">
              {{ r.chunk.content }}
            </p>
          </li>
        </ul>
        <div
          v-else-if="!probeLoading && probeQuery.trim() && probeResults.length === 0"
          class="font-ui text-[12px] text-ink-dim"
          data-testid="memory-probe-no-results"
        >
          No matching chunks found. Try a different query or check that embeddings are configured.
        </div>
      </section>
    </div><!-- end retrieval tab panel -->

    <div v-if="activeMainTab === 'chunks'" class="px-6 py-4 max-w-4xl">
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

      <!-- Prune controls (Bundle E WP15 + §2.5 preview modal) -->
      <div
        class="mb-3 flex flex-wrap items-center gap-2"
        data-testid="memory-prune-controls"
      >
        <button
          type="button"
          class="px-3 py-1 rounded-sm border border-border-muted font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2 disabled:opacity-50"
          :disabled="pruneRunning"
          data-testid="memory-prune-now"
          @click="openPrunePreview"
        >
          {{ pruneRunning ? 'Loading preview…' : 'Prune now' }}
        </button>
        <span
          v-if="pruneLastResult"
          class="font-mono text-[11px] text-ink-subtle"
          data-testid="memory-prune-result"
        >
          {{ pruneLastResult }}
        </span>
      </div>

      <!-- Narrative layer: "N narratives unrecoverable" banner (WP07) -->
      <div
        v-if="narrativeFailedCount > 0"
        class="mb-3 rounded-md border border-signal-warning bg-surface-1 px-3 py-2 font-ui text-[12px]"
        role="alert"
        data-testid="memory-narrative-failed-banner"
      >
        <div class="flex items-center justify-between gap-2">
          <span class="text-signal-warning">
            {{ narrativeFailedCount }} narrative{{ narrativeFailedCount === 1 ? '' : 's' }} unrecoverable — synthesis exhausted all retries.
          </span>
          <div class="flex gap-2">
            <button
              type="button"
              class="px-2 py-0.5 rounded-sm border border-signal-warning text-[10px] uppercase tracking-[0.18em] text-signal-warning hover:bg-surface-2"
              data-testid="memory-narrative-banner-toggle"
              @click="openNarrativeBanner"
            >
              Show jobs
            </button>
            <button
              type="button"
              class="px-2 py-0.5 rounded-sm border border-signal-warning text-[10px] uppercase tracking-[0.18em] text-signal-warning hover:bg-surface-2"
              data-testid="memory-narrative-retry-all"
              @click="retryAllNarrativeJobs"
            >
              Retry all
            </button>
          </div>
        </div>
        <!-- Expanded job list -->
        <ul
          v-if="narrativeBannerOpen && narrativeFailedJobs.length > 0"
          class="mt-2 space-y-1"
          data-testid="memory-narrative-failed-list"
        >
          <li
            v-for="job in narrativeFailedJobs"
            :key="job.id"
            class="flex items-center justify-between gap-2 rounded-sm bg-surface-2 px-2 py-1"
          >
            <span class="font-mono text-[10px] text-ink-dim truncate max-w-[16rem]" :title="job.lastError">
              turn {{ job.turnId }} — {{ job.lastError || 'unknown error' }}
            </span>
            <button
              type="button"
              :disabled="narrativeRetrying === job.id"
              class="px-2 py-0.5 rounded-sm border border-border-muted text-[10px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2 disabled:opacity-50"
              :data-testid="`memory-narrative-retry-${job.id}`"
              @click="retryNarrativeJob(job.id)"
            >
              {{ narrativeRetrying === job.id ? 'Retrying…' : 'Retry' }}
            </button>
          </li>
        </ul>
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
            <!-- Narrative kind badge (WP07) -->
            <span
              v-if="chunk.kind"
              class="font-ui text-[10px] uppercase tracking-[0.18em] px-1.5 py-0.5 rounded-sm border"
              :class="kindClass(chunk.kind)"
              :data-testid="`memory-kind-badge-${chunk.id}`"
            >
              {{ kindLabel(chunk.kind) }}
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
              <!-- Mark important (narrative promotion pin) (WP07) -->
              <button
                type="button"
                :disabled="markingImportant === chunk.id"
                class="px-2 py-1 rounded-sm border border-border-muted text-[10px] uppercase tracking-[0.18em] text-ink-dim hover:text-accent hover:bg-surface-2 disabled:opacity-50"
                :data-testid="`memory-mark-important-${chunk.id}`"
                :title="'Boost this memory\'s promotion score'"
                @click="toggleMarkImportant(chunk)"
              >
                {{ markingImportant === chunk.id ? '…' : 'Important' }}
              </button>
              <!-- Provenance (WP08) -->
              <button
                type="button"
                class="px-2 py-1 rounded-sm border border-border-muted text-[10px] uppercase tracking-[0.18em] text-ink-dim hover:text-accent hover:bg-surface-2"
                :data-testid="`memory-provenance-${chunk.id}`"
                :title="'View full audit chain for this chunk'"
                @click="openProvenance(chunk.id)"
              >
                Provenance
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
    </div><!-- end v-if chunks tab -->

    <!-- §2.5 — Prune preview confirmation modal -->
    <PrunePreviewModal
      v-if="prunePreview"
      :preview="prunePreview"
      :running="pruneModalRunning"
      @confirm="confirmPruneNow"
      @cancel="cancelPrunePreview"
    />

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

    <!-- §2.6 — Provenance drawer (WP08) -->
    <ProvenanceDrawer
      v-if="provenanceDrawerOpen && (provenanceChunk || provenanceLoading || provenanceError)"
      :provenance="provenanceChunk ?? { chunkId: '', scopePath: '', pinned: false, retrievalCount: 0, citationCount: 0, promotionScore: 0, createdAt: '' }"
      :loading="provenanceLoading"
      :error="provenanceError"
      @close="closeProvenance"
    />
  </div>
</template>
