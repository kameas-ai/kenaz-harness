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
 */
import { computed, ref } from 'vue';
import { useManifestStore } from '@/composables/useNodeManifest';
import type { NodeManifestSummary } from '@/lib/types';

const emit = defineEmits<{
  /** Fired on dragstart from a concrete-kind row. */
  (e: 'kind-drag-start', payload: { kind: string }): void;
  /** Fired on click from a concrete-kind row. */
  (e: 'kind-select', payload: { kind: string }): void;
}>();

const store = useManifestStore();
const filterText = ref('');

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
    ev.dataTransfer.setData('application/x-kaneaz-node-kind', kindId);
    ev.dataTransfer.setData('text/plain', kindId);
    ev.dataTransfer.effectAllowed = 'copy';
  }
  emit('kind-drag-start', { kind: kindId });
}

async function onReload() {
  await store.reload();
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
        <button
          type="button"
          class="rounded-sm border border-border-muted px-1.5 py-0.5 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2"
          data-testid="palette-reload"
          @click="onReload"
        >
          Reload
        </button>
      </div>
      <input
        v-model="filterText"
        type="text"
        placeholder="Filter kinds…"
        aria-label="Filter node types"
        class="w-full rounded-sm border border-border-muted bg-surface-0 px-2 py-1 font-ui text-[12px] text-ink placeholder:text-ink-dim"
        data-testid="palette-filter"
      />
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
