<script setup lang="ts">
/**
 * NodePalette — Category → Archetype → Kind tree picker for the
 * GraphEditor. Backs FR-025 / FR-028:
 *
 *   - Three categories: Compute, Control, State.
 *   - Archetypes are visible but disabled (greyed out) and emit a
 *     tooltip explaining that v1 archetypes are abstract.
 *   - Concrete kinds are HTML5-draggable; dragstart fires a `dragstart`
 *     CustomEvent with `{ kind: id }`. The GraphEditor owns the drop.
 *   - A filter input narrows the tree by case-insensitive substring
 *     match against id + display title + description. Archetypes whose
 *     children all filter out are hidden too, but archetypes whose own
 *     metadata matches stay visible.
 *
 * Source data comes from `useManifestStore` (FR-027).
 *
 * Node-override diagnostics (mission
 * controls-and-readouts-that-tell-the-truth-01PMZ808 WP18): the "Doctor"
 * button surfaces `client.nodes.doctor()` (cached catalog-health
 * counters) plus a fresh `client.nodes.listUserOverrides()` parse pass
 * over <DataDir>/agent_graph/nodes/ so a broken YAML's error is visible
 * without a restart. "Reload" now performs the real
 * `client.nodes.reloadOverrides()` re-scan + atomic catalog swap (the
 * button previously only re-fetched the in-memory catalog, so a
 * dropped-in override file never actually got picked up) and then
 * refreshes the palette tree from the swapped catalog.
 */
import { computed, ref } from 'vue';
import { useManifestStore } from '@/composables/useNodeManifest';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type {
  NodeManifestSummary,
  NodeDoctorReport,
  NodeUserOverrideInfo,
  NodeReloadResult,
} from '@/lib/types';

const emit = defineEmits<{
  /** Fired on dragstart from a concrete-kind row. */
  (e: 'kind-drag-start', payload: { kind: string }): void;
  /** Fired on click from a concrete-kind row. */
  (e: 'kind-select', payload: { kind: string }): void;
}>();

const store = useManifestStore();
const client = useHarnessClient();
const filterText = ref('');

// ── node-override diagnostics (WP18) ──────────────────────────────────

const diagnosticsOpen = ref(false);
const diagnosticsLoading = ref(false);
const diagnosticsError = ref<string | null>(null);
const doctorReport = ref<NodeDoctorReport | null>(null);
const userOverrides = ref<NodeUserOverrideInfo[]>([]);
const reloading = ref(false);
const lastReloadResult = ref<NodeReloadResult | null>(null);

const reloadDiffEmpty = computed(() => {
  const r = lastReloadResult.value;
  if (!r) return true;
  return r.added.length === 0 && r.removed.length === 0 && r.modified.length === 0;
});

interface ArchetypeGroup {
  archetype: NodeManifestSummary | null;
  archetypeId: string;
  kinds: NodeManifestSummary[];
}

interface CategoryGroup {
  category: string;
  label: string;
  archetypes: ArchetypeGroup[];
}

const CATEGORY_ORDER: Array<{ id: string; label: string }> = [
  { id: 'compute', label: 'Compute' },
  { id: 'control', label: 'Control' },
  { id: 'state', label: 'State' },
];

function isArchetype(m: NodeManifestSummary): boolean {
  return !m.callable;
}

function archetypeIdOf(m: NodeManifestSummary): string {
  return m.archetype || m.extends || '';
}

function matchesFilter(m: NodeManifestSummary, q: string): boolean {
  if (!q) return true;
  const needle = q.toLowerCase();
  const haystack = [
    m.id,
    m.displayName ?? '',
    m.description ?? '',
    m.kindName ?? '',
  ]
    .join(' ')
    .toLowerCase();
  return haystack.includes(needle);
}

const grouped = computed<CategoryGroup[]>(() => {
  const all = store.manifests.value ?? [];
  const q = filterText.value.trim();

  const out: CategoryGroup[] = [];
  for (const cat of CATEGORY_ORDER) {
    const inCat = all.filter((m) => (m.category ?? '') === cat.id);
    if (inCat.length === 0) continue;

    const archetypes = inCat.filter(isArchetype);
    const kinds = inCat.filter((m) => m.callable);

    // Bucket kinds under their archetype.
    const byArchetype = new Map<string, NodeManifestSummary[]>();
    for (const k of kinds) {
      const aid = archetypeIdOf(k) || cat.id;
      const arr = byArchetype.get(aid) ?? [];
      arr.push(k);
      byArchetype.set(aid, arr);
    }

    // Build groups in archetype-declaration order; fall back to any
    // unattached kinds under a synthetic archetype (uses the category).
    const groups: ArchetypeGroup[] = [];
    const seenArchIds = new Set<string>();
    for (const a of archetypes) {
      const k = (byArchetype.get(a.id) ?? []).slice();
      groups.push({ archetype: a, archetypeId: a.id, kinds: k });
      seenArchIds.add(a.id);
    }
    // Any archetype-id keys not matched by an actual archetype row.
    for (const [aid, arr] of byArchetype.entries()) {
      if (seenArchIds.has(aid)) continue;
      groups.push({ archetype: null, archetypeId: aid, kinds: arr });
    }
    // Sort kinds inside each archetype alphabetically by displayName.
    for (const g of groups) {
      g.kinds.sort((a, b) =>
        (a.displayName ?? a.id).localeCompare(b.displayName ?? b.id),
      );
    }

    // Apply filter: a group is kept if (archetype matches) OR (any kind
    // matches). Kinds inside the group are filtered to the matches when
    // a query is set; if archetype itself matches the query we still
    // show its full kind list.
    const filteredGroups: ArchetypeGroup[] = [];
    for (const g of groups) {
      const archMatches = g.archetype ? matchesFilter(g.archetype, q) : false;
      const kinds = q
        ? archMatches
          ? g.kinds
          : g.kinds.filter((k) => matchesFilter(k, q))
        : g.kinds;
      if (kinds.length === 0 && !archMatches) continue;
      filteredGroups.push({ ...g, kinds });
    }
    if (filteredGroups.length === 0) continue;

    out.push({ category: cat.id, label: cat.label, archetypes: filteredGroups });
  }
  return out;
});

function highlight(text: string): string {
  const q = filterText.value.trim();
  if (!q) return escapeHtml(text);
  const haystack = text;
  const lc = haystack.toLowerCase();
  const lq = q.toLowerCase();
  const idx = lc.indexOf(lq);
  if (idx < 0) return escapeHtml(text);
  const before = escapeHtml(haystack.slice(0, idx));
  const match = escapeHtml(haystack.slice(idx, idx + q.length));
  const after = escapeHtml(haystack.slice(idx + q.length));
  return `${before}<mark class="bg-accent/30 text-accent">${match}</mark>${after}`;
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function onDragStart(ev: DragEvent, kindId: string) {
  if (ev.dataTransfer) {
    ev.dataTransfer.setData('application/x-kenaz-node-kind', kindId);
    ev.dataTransfer.setData('text/plain', kindId);
    ev.dataTransfer.effectAllowed = 'copy';
  }
  emit('kind-drag-start', { kind: kindId });
}

/** Fetches the doctor report + a fresh per-file override parse pass. */
async function loadDiagnostics() {
  diagnosticsLoading.value = true;
  diagnosticsError.value = null;
  try {
    const [doctor, overrides] = await Promise.all([
      client.nodes.doctor(),
      client.nodes.listUserOverrides(),
    ]);
    doctorReport.value = doctor;
    userOverrides.value = overrides;
  } catch (err) {
    diagnosticsError.value = err instanceof Error ? err.message : String(err);
  } finally {
    diagnosticsLoading.value = false;
  }
}

/** "Doctor" button: opens the diagnostics panel and loads a fresh report. */
async function onDoctor() {
  diagnosticsOpen.value = true;
  await loadDiagnostics();
}

/**
 * "Reload" button: performs the real on-disk re-scan + atomic catalog
 * swap (`reloadOverrides`), then refreshes the palette tree so a fixed
 * or newly-dropped override kind shows up without an app restart. When
 * the diagnostics panel is open, also re-runs the doctor/list pass so
 * a previously-reported parse error updates in place.
 */
async function onReload() {
  reloading.value = true;
  diagnosticsError.value = null;
  try {
    lastReloadResult.value = await client.nodes.reloadOverrides();
  } catch (err) {
    diagnosticsError.value = err instanceof Error ? err.message : String(err);
  } finally {
    reloading.value = false;
  }
  await store.reload();
  if (diagnosticsOpen.value) {
    await loadDiagnostics();
  }
}

defineExpose({ filterText });
</script>

<template>
  <div
    class="flex h-full flex-col gap-2 border-r border-border-muted bg-surface-1"
    data-testid="node-palette"
  >
    <div class="border-b border-border-muted px-3 py-2">
      <div class="mb-2 flex items-center justify-between">
        <span
          class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim"
          >Node palette</span
        >
        <div class="flex items-center gap-1">
          <button
            type="button"
            class="rounded-sm border border-border-muted px-1.5 py-0.5 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2"
            data-testid="palette-doctor"
            @click="onDoctor"
          >
            Doctor
          </button>
          <button
            type="button"
            class="rounded-sm border border-border-muted px-1.5 py-0.5 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2 disabled:opacity-50 disabled:cursor-wait"
            data-testid="palette-reload"
            :disabled="reloading"
            @click="onReload"
          >
            {{ reloading ? 'Reloading…' : 'Reload' }}
          </button>
        </div>
      </div>
      <input
        v-model="filterText"
        type="text"
        placeholder="Filter kinds…"
        aria-label="Filter node types"
        class="w-full rounded-sm border border-border-muted bg-surface-0 px-2 py-1 font-ui text-[12px] text-ink placeholder:text-ink-dim"
        data-testid="palette-filter"
      />

      <!-- Node-override diagnostics panel (WP18): reachable via the
           "Doctor" button. Shows the cached catalog-health counters
           plus a fresh per-file parse pass over the user-override
           directory, and the diff/errors from the most recent Reload. -->
      <div
        v-if="diagnosticsOpen"
        class="mt-2 space-y-2 rounded-sm border border-border-muted bg-surface-0 px-2 py-2"
        data-testid="node-diagnostics-panel"
      >
        <div class="flex items-center justify-between">
          <span
            class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-muted"
            >Node-override diagnostics</span
          >
          <button
            type="button"
            class="font-ui text-[10px] text-ink-dim hover:text-ink"
            data-testid="diagnostics-close"
            @click="diagnosticsOpen = false"
          >
            Close
          </button>
        </div>

        <div
          v-if="diagnosticsLoading"
          class="font-ui text-[11px] text-ink-dim"
          data-testid="diagnostics-loading"
        >
          Checking…
        </div>
        <div
          v-else-if="diagnosticsError"
          class="font-ui text-[11px] text-signal-danger"
          role="alert"
          data-testid="diagnostics-error"
        >
          {{ diagnosticsError }}
        </div>
        <template v-else>
          <dl
            v-if="doctorReport"
            class="grid grid-cols-2 gap-x-3 gap-y-0.5 font-ui text-[11px]"
            data-testid="diagnostics-doctor"
          >
            <dt class="text-ink-subtle">Shipped</dt>
            <dd class="font-mono text-ink" data-testid="doctor-shipped">{{ doctorReport.shippedCount }}</dd>
            <dt class="text-ink-subtle">User overrides</dt>
            <dd class="font-mono text-ink" data-testid="doctor-user-overrides">{{ doctorReport.userOverrideCount }}</dd>
            <dt class="text-ink-subtle">Archetypes</dt>
            <dd class="font-mono text-ink">{{ doctorReport.archetypeCount }}</dd>
            <dt class="text-ink-subtle">Callable</dt>
            <dd class="font-mono text-ink">{{ doctorReport.callableCount }}</dd>
            <dt class="text-ink-subtle">Aliases</dt>
            <dd class="font-mono text-ink">{{ doctorReport.aliasCount }}</dd>
            <dt class="text-ink-subtle">Hot reload</dt>
            <dd class="font-mono text-ink" data-testid="doctor-hot-reload">
              {{ doctorReport.hotReloadEnabled ? 'enabled' : 'disabled' }}
            </dd>
            <dt class="text-ink-subtle">Last reload</dt>
            <dd class="font-mono text-ink" data-testid="doctor-last-reload">
              {{ doctorReport.lastReloadAt || 'never' }}
            </dd>
          </dl>

          <ul
            v-if="userOverrides.length > 0"
            class="space-y-0.5"
            data-testid="diagnostics-overrides-list"
          >
            <li
              v-for="o in userOverrides"
              :key="o.filename"
              class="flex items-center gap-2 font-ui text-[11px]"
              :data-testid="`override-row-${o.filename}`"
            >
              <span
                :class="o.status === 'error' ? 'text-signal-danger' : 'text-signal-success'"
                :data-testid="`override-status-${o.filename}`"
                >{{ o.status }}</span
              >
              <span class="font-mono text-ink">{{ o.filename }}</span>
              <span
                v-if="o.error"
                class="truncate text-ink-dim"
                :title="o.error"
                :data-testid="`override-error-${o.filename}`"
                >{{ o.error }}</span
              >
            </li>
          </ul>
          <p
            v-else
            class="font-ui text-[11px] text-ink-dim"
            data-testid="diagnostics-overrides-empty"
          >
            No user-override files found.
          </p>
        </template>

        <div
          v-if="lastReloadResult"
          class="border-t border-border-muted pt-2 font-ui text-[11px] text-ink-muted"
          data-testid="diagnostics-reload-diff"
        >
          <span v-if="reloadDiffEmpty" data-testid="reload-diff-none">No changes.</span>
          <template v-else>
            <span v-if="lastReloadResult.added.length" data-testid="reload-diff-added" class="mr-2"
              >+{{ lastReloadResult.added.length }} added</span
            >
            <span v-if="lastReloadResult.removed.length" data-testid="reload-diff-removed" class="mr-2"
              >-{{ lastReloadResult.removed.length }} removed</span
            >
            <span v-if="lastReloadResult.modified.length" data-testid="reload-diff-modified"
              >{{ lastReloadResult.modified.length }} modified</span
            >
          </template>
          <p
            v-if="lastReloadResult.errors && lastReloadResult.errors.length > 0"
            class="mt-1 text-signal-danger"
            role="alert"
            data-testid="reload-errors"
          >
            <span v-for="(e, i) in lastReloadResult.errors" :key="i">{{ e }}</span>
          </p>
        </div>
      </div>
    </div>

    <div class="min-h-0 flex-1 overflow-y-auto px-2 py-1">
      <div
        v-if="store.loading.value && grouped.length === 0"
        class="px-2 py-2 font-ui text-[11px] text-ink-dim"
        data-testid="palette-loading"
      >
        Loading catalog…
      </div>
      <div
        v-else-if="store.error.value"
        class="px-2 py-2 font-ui text-[11px] text-signal-danger"
        data-testid="palette-error"
      >
        {{ store.error.value }}
      </div>
      <div
        v-else-if="grouped.length === 0"
        class="px-2 py-2 font-ui text-[11px] text-ink-dim"
        data-testid="palette-empty"
      >
        No matches.
      </div>
      <ul v-else class="space-y-2">
        <li
          v-for="cat in grouped"
          :key="cat.category"
          :data-testid="`palette-category-${cat.category}`"
        >
          <div
            class="px-1 py-0.5 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-muted"
          >
            {{ cat.label }}
          </div>
          <ul class="space-y-1">
            <li
              v-for="group in cat.archetypes"
              :key="`${cat.category}-${group.archetypeId}`"
              :data-testid="`palette-archetype-${group.archetypeId}`"
            >
              <div
                v-if="group.archetype"
                class="flex items-center gap-1 px-1 text-ink-dim"
                :title="`v1: archetypes are abstract — pick a concrete kind below (${group.archetype.id})`"
                data-testid="palette-archetype-row"
                aria-disabled="true"
              >
                <span
                  class="inline-flex h-3 w-3 items-center justify-center rounded-full border border-border-muted text-[8px] text-ink-muted"
                  aria-hidden="true"
                  >i</span
                >
                <span
                  class="font-ui text-[12px] uppercase tracking-[0.14em] opacity-60"
                  >{{ group.archetype.displayName || group.archetype.id }}</span
                >
                <span
                  class="ml-auto rounded-sm border border-border-muted px-1 font-ui text-[9px] uppercase tracking-[0.18em] text-ink-muted"
                  >abstract</span
                >
              </div>
              <ul class="ml-3 mt-0.5 space-y-0.5">
                <li
                  v-for="kind in group.kinds"
                  :key="kind.id"
                  :draggable="true"
                  class="flex cursor-grab items-center gap-1 rounded-sm px-1 py-0.5 hover:bg-surface-2 active:cursor-grabbing"
                  :data-testid="`palette-kind-${kind.id}`"
                  :title="kind.description || kind.id"
                  @dragstart="(ev) => onDragStart(ev, kind.id)"
                  @click="emit('kind-select', { kind: kind.id })"
                >
                  <span
                    class="font-ui text-[12px] text-ink"
                    v-html="highlight(kind.displayName || kind.id)"
                  />
                  <span
                    class="ml-auto font-mono text-[10px] text-ink-dim"
                    >{{ kind.id }}</span
                  >
                </li>
              </ul>
            </li>
          </ul>
        </li>
      </ul>
    </div>
  </div>
</template>
