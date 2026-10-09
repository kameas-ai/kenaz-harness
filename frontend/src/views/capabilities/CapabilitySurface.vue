<script setup lang="ts">
/**
 * CapabilitySurface — the one "Add capability" list + detail surface
 * (install-framework-01DOGF0B, FR-4). One list over every install provider
 * (Capability_List: installed state read from each runtime consumer) plus
 * the built-in tool toggles, with kind chips, source chips and search;
 * the detail pane is the row kind's plugin (plugins/registry.ts).
 *
 * WP04 renders the MCP-recipe provider and replaces Tools › Registry (the
 * retired RegistryTab.vue browse list) and KenazToolsPanel.vue; WP05 adds
 * the skill and workflow providers and replaces Workflows › Catalog.
 * Phase 4 WP08 folds in the retired Marketplace: the fleet-catalog rows no
 * provider lists yet (bundle / agent_pack — Phase 3 has not shipped, so
 * they stay disabled-with-reason), installed/ residue downloads, the
 * catalog listing facts and Withdraw (catalogBrowse.ts,
 * CatalogListingDetail.vue). The surface mounts in the Capabilities view
 * (views/tools/ToolsView.vue, route /tools).
 *
 * Sources a provider could not list arrive as `unavailable` rows with a
 * reason and are rendered as rows, never as a hidden tab (P-5).
 */
import { computed, nextTick, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { useEventStream } from '@/lib/useEventStream';
import { categoryIconFor, categoryLabel } from '@/lib/recipeCategories';
import { Search, Zap, Plus } from '@/shell/icons';
import AddMCPServerModal from '@/views/tools/AddMCPServerModal.vue';
import { signedIn } from '@/lib/featureFlags';
import CatalogListingDetail from './CatalogListingDetail.vue';
import {
  CATALOG_ONLY_KINDS,
  catalogOnlyKind,
  catalogRows,
  catalogSource,
  VISIBILITY_LABELS,
  reasonElId,
} from './catalogBrowse';
import { BUILTIN_TOOLS, type BuiltinTool } from './plugins/builtinTools';
import { REVOKED_INSTALL_COPY, isRevoked, lifecycleChip } from './lifecycle';
import {
  ENTRY_POINTS,
  KIND_PLUGINS,
  SOURCE_LABELS,
  needsFlow,
  pluginFor,
  unavailableText,
  type EntryPoint,
} from './plugins/registry';
import type {
  CapabilityEvent,
  CapabilityItem,
  CapabilityKind,
  CapabilitySource,
  CapabilityUnavailable,
  CatalogItemView,
} from '@/lib/types';

const client = useHarnessClient();
const router = useRouter();
const route = useRoute();

/** Category the long-tail callout points at (carried over from RegistryTab). */
const AUTOMATION_CATEGORY = 'automation';
/** Prefilled GitHub new-issue URL for "Request a connector". */
const REQUEST_CONNECTOR_URL =
  'https://github.com/kameas-ai/kenaz-harness/issues/new?title=Connector%20request%3A%20';

type KindFilter = 'all' | 'builtin' | CapabilityKind;
type SourceFilter = 'all' | CapabilitySource;

type Row =
  | { type: 'builtin'; key: string; tool: BuiltinTool }
  | { type: 'item'; key: string; item: CapabilityItem }
  // A fleet-catalog row no provider lists (WP08 — the Marketplace fold-in).
  | { type: 'catalog'; key: string; entry: CatalogItemView; residue: boolean };

// ── listing ──────────────────────────────────────────────────────────
const items = ref<CapabilityItem[]>([]);
const unavailable = ref<CapabilityUnavailable[]>([]);
const loading = ref(false);
const loadError = ref<string | null>(null);

async function load() {
  loading.value = true;
  loadError.value = null;
  try {
    const l = await client.capabilities.list({});
    items.value = l.items ?? [];
    unavailable.value = l.unavailable ?? [];
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}

// ── fleet catalog browse (Phase 4 WP08 — folded in from MarketplaceView) ──
// Catalog_List is the only listing of bundle / agent_pack catalog items and
// of installed/ residue; it also carries the listing facts (visibility,
// published date) Withdraw needs for skill and workflow catalog rows.
// Desktop-only (no Catalog_* serve dispatch) — this surface renders only
// outside served mode (ToolsView's boundary panel).
const catalogEntries = ref<CatalogItemView[]>([]);
const catalogError = ref<string | null>(null);

async function loadCatalog() {
  catalogError.value = null;
  if (!signedIn.value) {
    catalogEntries.value = [];
    return;
  }
  try {
    catalogEntries.value = (await client.catalog.list({})) ?? [];
  } catch (e) {
    catalogEntries.value = [];
    catalogError.value = e instanceof Error ? e.message : String(e);
  }
}
watch(signedIn, () => void loadCatalog());

// The provider-less kinds' signed-out / error rows (P-5: a reason row,
// never a hidden kind).
const catalogUnavailable = computed<CapabilityUnavailable[]>(() => {
  if (!signedIn.value) {
    return CATALOG_ONLY_KINDS.map((k) => ({ kind: k.kind, source: '' as const, reason: 'signed_out' }));
  }
  if (catalogError.value) {
    return CATALOG_ONLY_KINDS.map((k) => ({
      kind: k.kind,
      source: '' as const,
      reason: 'error',
      message: catalogError.value ?? undefined,
    }));
  }
  return [];
});

/** The catalog listing a provider row also is (skill / workflow catalog items). */
function listingFor(it: CapabilityItem): CatalogItemView | null {
  let best: CatalogItemView | null = null;
  for (const e of catalogEntries.value) {
    if (e.kind !== it.kind || e.id !== it.id) continue;
    if (e.version === it.version) return e;
    best = best ?? e;
  }
  return best;
}

// One event for every provider (FR-3): any install / uninstall — from
// this surface, a per-kind flow, or a fleet sync pull —
// repaints the list from the consumers.
useEventStream<CapabilityEvent>('capability:installed', () => void load());
useEventStream<CapabilityEvent>('capability:uninstalled', () => void load());

// ── built-in tool toggles (no-install home) ─────────────────────────
const builtinOn = ref<Record<string, boolean>>({});
const builtinBusy = ref<Record<string, boolean>>({});
const builtinError = ref<Record<string, string | null>>({});

async function refreshBuiltins() {
  await Promise.all(
    BUILTIN_TOOLS.map(async (t) => {
      let v = t.fallback;
      try {
        v = await t.get(client);
      } catch {
        v = t.fallback;
      }
      builtinOn.value = { ...builtinOn.value, [t.id]: v };
    }),
  );
}

async function toggleBuiltin(tool: BuiltinTool, event: Event) {
  if (builtinBusy.value[tool.id]) return;
  const next = (event.target as HTMLInputElement).checked;
  const previous = builtinOn.value[tool.id] ?? tool.fallback;
  builtinBusy.value = { ...builtinBusy.value, [tool.id]: true };
  builtinError.value = { ...builtinError.value, [tool.id]: null };
  builtinOn.value = { ...builtinOn.value, [tool.id]: next };
  try {
    await tool.set(client, next);
  } catch (e) {
    builtinOn.value = { ...builtinOn.value, [tool.id]: previous };
    builtinError.value = {
      ...builtinError.value,
      [tool.id]: e instanceof Error ? e.message : `Failed to toggle ${tool.name}.`,
    };
  } finally {
    builtinBusy.value = { ...builtinBusy.value, [tool.id]: false };
  }
}

onMounted(() => {
  void load();
  void loadCatalog();
  void refreshBuiltins();
});

// ── filters ──────────────────────────────────────────────────────────
const query = ref('');
// `?kind=` deep links (e.g. the retired Workflows › Catalog tab redirects to
// /tools?kind=workflow) open the surface on that kind.
function kindFromQuery(): KindFilter {
  const raw = route?.query?.kind;
  const v = Array.isArray(raw) ? raw[0] : raw;
  if (v === 'builtin' || KIND_PLUGINS.some((p) => p.kind === v) || catalogOnlyKind(String(v))) {
    return v as KindFilter;
  }
  return 'all';
}
const kindFilter = ref<KindFilter>(kindFromQuery());
const sourceFilter = ref<SourceFilter>('all');
const categoryFilter = ref<string | null>(null);

const kindChips = computed(() => [
  { id: 'all' as KindFilter, label: 'All' },
  { id: 'builtin' as KindFilter, label: 'Built-in tools' },
  ...KIND_PLUGINS.map((p) => ({ id: p.kind as KindFilter, label: p.label })),
  ...CATALOG_ONLY_KINDS.map((k) => ({ id: k.kind as KindFilter, label: k.label })),
]);
const sourceChips: { id: SourceFilter; label: string }[] = [
  { id: 'all', label: 'Any source' },
  ...(Object.keys(SOURCE_LABELS) as CapabilitySource[]).map((s) => ({ id: s, label: SOURCE_LABELS[s] })),
];

function matchesQuery(fields: (string | undefined)[], q: string): boolean {
  return fields.some((f) => (f ?? '').toLowerCase().includes(q));
}

const rows = computed<Row[]>(() => {
  const q = query.value.trim().toLowerCase();
  const out: Row[] = [];
  const builtinVisible =
    (kindFilter.value === 'all' || kindFilter.value === 'builtin') &&
    (sourceFilter.value === 'all' || sourceFilter.value === 'builtin') &&
    categoryFilter.value === null;
  if (builtinVisible) {
    for (const tool of BUILTIN_TOOLS) {
      if (q && !matchesQuery([tool.name, tool.summary, tool.description, ...(tool.tools ?? [])], q)) continue;
      out.push({ type: 'builtin', key: `builtin:${tool.id}`, tool });
    }
  }
  const visible = items.value
    .filter((it) => pluginFor(it.kind))
    .filter((it) => kindFilter.value === 'all' || kindFilter.value === it.kind)
    .filter((it) => sourceFilter.value === 'all' || sourceFilter.value === it.source)
    .filter((it) => categoryFilter.value === null || it.category === categoryFilter.value)
    .filter((it) => !q || matchesQuery([it.name, it.description, it.id, ...(it.keywords ?? [])], q))
    .sort((a, b) => {
      if (a.state.installed !== b.state.installed) return a.state.installed ? -1 : 1;
      return a.name.localeCompare(b.name);
    });
  for (const it of visible) out.push({ type: 'item', key: `${it.kind}:${it.id}`, item: it });
  if (categoryFilter.value === null) {
    for (const r of catalogRows(catalogEntries.value)) {
      const e = r.entry;
      if (kindFilter.value !== 'all' && kindFilter.value !== e.kind) continue;
      if (sourceFilter.value !== 'all' && sourceFilter.value !== catalogSource(e.visibility)) continue;
      if (q && !matchesQuery([e.slug, e.description, e.kind], q)) continue;
      out.push({ type: 'catalog', key: r.key, entry: e, residue: r.residue });
    }
  }
  return out;
});

const visibleUnavailable = computed(() =>
  [...unavailable.value, ...catalogUnavailable.value].filter(
    (u) =>
      (kindFilter.value === 'all' || kindFilter.value === u.kind) &&
      (sourceFilter.value === 'all' || !u.source || sourceFilter.value === u.source),
  ),
);

// An empty list behind a FAILED listing is not an empty catalog. Keyed on
// what is rendered above (the load-error banner, or a visible error reason
// row — a Catalog_List failure arrives as one) so "see above" always
// points at something.
const listingFailed = computed(
  () => !!loadError.value || visibleUnavailable.value.some((u) => u.reason === 'error'),
);

const emptyText = computed(() => {
  const q = query.value.trim();
  if (listingFailed.value) {
    return q
      ? `Nothing matches “${q}” — a listing failed to load, so this may be incomplete.`
      : 'A listing failed to load (see above) — this list may be incomplete.';
  }
  return q ? `Nothing matches “${q}”.` : 'Nothing to show for these filters.';
});

function browseAutomation() {
  query.value = '';
  kindFilter.value = 'mcp_recipe';
  categoryFilter.value = AUTOMATION_CATEGORY;
}

function requestConnector() {
  const term = query.value.trim();
  client.openExternalURL(term ? REQUEST_CONNECTOR_URL + encodeURIComponent(term) : REQUEST_CONNECTOR_URL);
}

// ── selection + detail ───────────────────────────────────────────────
const selectedKey = ref<string | null>(null);
const selected = computed<Row | null>(() => {
  if (!selectedKey.value) return null;
  const inRows = rows.value.find((r) => r.key === selectedKey.value);
  if (inRows) return inRows;
  // A selected row filtered out of the list keeps its detail open.
  const it = items.value.find((i) => `${i.kind}:${i.id}` === selectedKey.value);
  if (it) return { type: 'item', key: selectedKey.value, item: it };
  const c = catalogRows(catalogEntries.value).find((r) => r.key === selectedKey.value);
  return c ? { type: 'catalog', key: c.key, entry: c.entry, residue: c.residue } : null;
});
const detailRef = ref<{ beginInstall?: () => void } | null>(null);

function select(row: Row) {
  selectedKey.value = row.key;
}

// ── install / update / generic remove ────────────────────────────────
const busy = ref<Record<string, boolean>>({});
const rowError = ref<Record<string, string | null>>({});

function setRowError(key: string, msg: string | null) {
  rowError.value = { ...rowError.value, [key]: msg };
}

async function install(row: Row & { type: 'item' }) {
  const it = row.item;
  setRowError(row.key, null);
  if (isRevoked(it)) {
    // The button is disabled; this keeps a stray call from fetching a
    // version fleet answers 410 for (skill-library-01SKLIB01 WP02).
    setRowError(row.key, REVOKED_INSTALL_COPY);
    return;
  }
  if (needsFlow(it)) {
    // Keys, OAuth, a directory, a warning to acknowledge: the per-kind
    // flow collects them (FR-4 detail plugin).
    selectedKey.value = row.key;
    await nextTick();
    detailRef.value?.beginInstall?.();
    return;
  }
  busy.value = { ...busy.value, [row.key]: true };
  try {
    await client.capabilities.install(it.kind, it.id, it.version ?? '');
    await load();
  } catch (e) {
    setRowError(row.key, e instanceof Error ? e.message : String(e));
  } finally {
    busy.value = { ...busy.value, [row.key]: false };
  }
}

async function update(row: Row & { type: 'item' }) {
  setRowError(row.key, null);
  busy.value = { ...busy.value, [row.key]: true };
  try {
    await client.capabilities.update(row.item.kind, row.item.id);
    await load();
  } catch (e) {
    setRowError(row.key, e instanceof Error ? e.message : String(e));
  } finally {
    busy.value = { ...busy.value, [row.key]: false };
  }
}

const confirmingRemoveKey = ref<string | null>(null);
function removeSelected() {
  const row = selected.value;
  if (row && row.type === 'item') void genericRemove(row);
}
async function genericRemove(row: Row & { type: 'item' }) {
  confirmingRemoveKey.value = null;
  setRowError(row.key, null);
  busy.value = { ...busy.value, [row.key]: true };
  try {
    await client.capabilities.uninstall(row.item.kind, row.item.id);
    await load();
  } catch (e) {
    setRowError(row.key, e instanceof Error ? e.message : String(e));
  } finally {
    busy.value = { ...busy.value, [row.key]: false };
  }
}

// ── catalog-only rows (WP08) ──────────────────────────────────────────
// Install on a bundle / agent_pack row is rendered DISABLED with the
// reason as visible text (WP02 posture). This guard keeps a stray call
// (keyboard, test, a force-enabled button) from reaching Catalog_Install,
// which refuses the kind anyway: it shows the reason and calls nothing.
function refuseCatalogInstall(row: Row & { type: 'catalog' }) {
  setRowError(row.key, catalogOnlyKind(row.entry.kind)?.reason ?? 'This item cannot be installed here.');
}

async function removeDownload(entry: CatalogItemView, key: string) {
  setRowError(key, null);
  busy.value = { ...busy.value, [key]: true };
  try {
    await client.catalog.uninstall(entry.kind, entry.id, entry.version);
    await loadCatalog();
  } catch (e) {
    setRowError(key, `Remove download failed: ${e instanceof Error ? e.message : String(e)}`);
  } finally {
    busy.value = { ...busy.value, [key]: false };
  }
}

// ── withdraw (fleet-enforcement-truth-01PMZ505 WP11, from MarketplaceView) ──
// Removes the item from the org catalog listing for everyone — distinct
// from Remove / Remove download (this device only). Confirm-guarded with
// its own copy (AC-021); the server's answer, including a 403 ("not the
// owner or an admin", never a tier message), is shown as-is.
const pendingWithdraw = ref<CatalogItemView | null>(null);
const withdrawBusy = ref(false);
const withdrawError = ref('');

function promptWithdraw(entry: CatalogItemView) {
  pendingWithdraw.value = entry;
  withdrawError.value = '';
}

function cancelWithdraw() {
  pendingWithdraw.value = null;
  withdrawError.value = '';
}

async function confirmWithdraw() {
  const entry = pendingWithdraw.value;
  if (!entry) return;
  withdrawBusy.value = true;
  withdrawError.value = '';
  try {
    await client.catalog.unpublish(entry.id);
    pendingWithdraw.value = null;
    await Promise.all([loadCatalog(), load()]);
  } catch (e) {
    withdrawError.value = e instanceof Error ? e.message : String(e);
  } finally {
    withdrawBusy.value = false;
  }
}

// ── "Add your own" entry points ─────────────────────────────────────
const entryOpen = ref<EntryPoint['id'] | null>(null);
const existingIds = computed(() =>
  items.value.filter((i) => i.kind === 'mcp_recipe' && i.state.installed).map((i) => i.id),
);
function onEntryDone() {
  entryOpen.value = null;
  void load();
}

function stateLabel(it: CapabilityItem): string {
  if (it.state.update_available) return 'Update available';
  return it.state.installed ? 'Installed' : 'Not installed';
}

function kindNoun(kind: CapabilityKind | string): string {
  return pluginFor(kind as CapabilityKind)?.noun ?? catalogOnlyKind(kind)?.noun ?? kind;
}

function gotoLearned() {
  void router.push('/knowledge/learned');
}

// The page's primary action, "Add capability" (decision record §3; the
// button lives in ToolsView's header): clear every filter and put the
// cursor in search, so the whole browse — every kind, every source — is
// one keystroke away.
const searchEl = ref<HTMLInputElement | null>(null);
function focusBrowse() {
  query.value = '';
  kindFilter.value = 'all';
  sourceFilter.value = 'all';
  categoryFilter.value = null;
  void nextTick(() => {
    searchEl.value?.scrollIntoView?.({ block: 'nearest' });
    searchEl.value?.focus();
  });
}
defineExpose({ focusBrowse });
</script>

<template>
  <section class="px-6 py-4 space-y-3" data-testid="capability-surface">
    <header class="flex flex-wrap items-center justify-between gap-2">
      <!-- The page's "Add capability" action lives in ToolsView's header
           (WP09); this heading names the list, which holds installed and
           available capabilities alike. -->
      <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        All capabilities
      </h2>
      <div class="flex flex-wrap items-center gap-2" data-testid="capability-entry-points">
        <span class="font-ui text-[11px] text-ink-muted">Add your own:</span>
        <button
          v-for="ep in ENTRY_POINTS"
          :key="ep.id"
          type="button"
          class="inline-flex items-center gap-1 rounded-sm border border-accent-hairline bg-surface-1 px-2.5 py-1 font-ui text-[11px] text-accent hover:bg-accent-glow"
          :data-testid="`capability-entry-${ep.id}`"
          @click="entryOpen = ep.id"
        >
          <Plus class="h-3.5 w-3.5" aria-hidden="true" />
          {{ ep.label }}
        </button>
      </div>
    </header>

    <!-- Search + filters -->
    <div class="relative">
      <Search
        class="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-ink-subtle"
        aria-hidden="true"
      />
      <input
        ref="searchEl"
        v-model="query"
        type="search"
        aria-label="Search capabilities"
        placeholder="Search tools, MCP servers, skills, workflows, bundles…"
        class="w-full rounded-sm border border-border-muted bg-surface-1 py-2 pl-8 pr-3 font-ui text-[12px] text-ink placeholder:text-ink-subtle focus:border-accent focus:outline-none"
        data-testid="capability-search"
      />
    </div>
    <div class="flex flex-wrap items-center gap-1.5" role="group" aria-label="Kind">
      <button
        v-for="chip in kindChips"
        :key="chip.id"
        type="button"
        :aria-pressed="kindFilter === chip.id"
        :class="[
          'rounded-sm border px-2 py-0.5 font-ui text-[11px]',
          kindFilter === chip.id ? 'border-accent text-accent' : 'border-border-muted text-ink-muted hover:text-ink',
        ]"
        :data-testid="`capability-kind-chip-${chip.id}`"
        @click="kindFilter = chip.id; categoryFilter = null"
      >
        {{ chip.label }}
      </button>
    </div>
    <div class="flex flex-wrap items-center gap-1.5" role="group" aria-label="Source">
      <button
        v-for="chip in sourceChips"
        :key="chip.id"
        type="button"
        :aria-pressed="sourceFilter === chip.id"
        :class="[
          'rounded-sm border px-2 py-0.5 font-ui text-[11px]',
          sourceFilter === chip.id ? 'border-accent text-accent' : 'border-border-muted text-ink-muted hover:text-ink',
        ]"
        :data-testid="`capability-source-chip-${chip.id}`"
        @click="sourceFilter = chip.id"
      >
        {{ chip.label }}
      </button>
      <span
        v-if="categoryFilter"
        class="ml-2 inline-flex items-center gap-1 font-ui text-[11px] text-ink-muted"
        data-testid="capability-category-filter"
      >
        Showing <span class="rounded-sm bg-surface-2 px-1.5 text-ink">{{ categoryLabel(categoryFilter) }}</span>
        <button type="button" class="text-accent hover:text-accent-muted" @click="categoryFilter = null">Clear</button>
      </span>
    </div>

    <div
      v-if="loadError"
      class="rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
      role="alert"
      data-testid="capability-load-error"
    >
      {{ loadError }}
    </div>

    <div class="grid grid-cols-1 gap-3 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
      <!-- List -->
      <div class="min-w-0 space-y-2">
        <!-- Sources that could not be listed: a row with the reason, never a hidden tab (P-5). -->
        <div
          v-for="u in visibleUnavailable"
          :key="`${u.kind}:${u.source}:${u.reason}`"
          class="rounded-sm border border-border-muted bg-surface-1 px-3 py-2 font-ui text-[12px] text-ink-muted"
          role="note"
          :data-testid="`capability-unavailable-${u.kind}-${u.source || 'all'}`"
        >
          <span class="text-ink">{{ pluginFor(u.kind)?.label ?? catalogOnlyKind(u.kind)?.label ?? u.kind }}</span>
          <span v-if="u.source"> · {{ SOURCE_LABELS[u.source as CapabilitySource] ?? u.source }}</span>
          — {{ unavailableText(u.reason, u.message) }}
        </div>

        <div v-if="loading && rows.length === 0" class="py-4 font-ui text-[12px] text-ink-muted" data-testid="capability-loading">
          Loading capabilities…
        </div>
        <div
          v-else-if="rows.length === 0"
          class="py-4 font-ui text-[12px] text-ink-muted"
          data-testid="capability-empty"
        >
          {{ emptyText }}
        </div>

        <ul v-else class="divide-y divide-border-muted rounded-sm border border-border-muted bg-surface-1" data-testid="capability-list">
          <template v-for="row in rows" :key="row.key">
            <!-- Built-in tool: a toggle, not an install. -->
            <li
              v-if="row.type === 'builtin'"
              :class="['grid items-start gap-3 px-4 py-3', selectedKey === row.key ? 'bg-surface-2' : '']"
              style="grid-template-columns: 1fr auto"
              :data-testid="`${row.tool.testid}-tool-row`"
            >
              <button type="button" class="min-w-0 text-left" @click="select(row)">
                <div class="flex items-center gap-2 font-ui text-[13px] text-ink">
                  <span>{{ row.tool.name }}</span>
                  <span class="text-[10px] uppercase tracking-[0.14em] text-ink-dim">built-in</span>
                  <span v-if="builtinOn[row.tool.id]" class="text-[10px] uppercase tracking-[0.16em] text-signal-ok">on</span>
                </div>
                <p class="mt-1 max-w-prose text-[11px] text-ink-muted">{{ row.tool.summary }}</p>
                <div v-if="builtinError[row.tool.id]" class="mt-1 text-[11px] text-signal-danger" role="alert">
                  {{ builtinError[row.tool.id] }}
                </div>
              </button>
              <label class="inline-flex select-none items-center" :class="builtinBusy[row.tool.id] ? 'cursor-wait opacity-60' : 'cursor-pointer'">
                <input
                  type="checkbox"
                  class="h-4 w-4 accent-accent"
                  :checked="builtinOn[row.tool.id] ?? row.tool.fallback"
                  :disabled="builtinBusy[row.tool.id]"
                  :aria-label="row.tool.ariaLabel"
                  :data-testid="`${row.tool.testid}-toggle`"
                  @change="toggleBuiltin(row.tool, $event)"
                />
              </label>
            </li>

            <!-- Provider item -->
            <li
              v-else-if="row.type === 'item'"
              :class="['grid items-start gap-3 px-4 py-3', selectedKey === row.key ? 'bg-surface-2' : '']"
              style="grid-template-columns: 1.25rem 1fr auto"
              :data-testid="`capability-row-${row.item.kind}-${row.item.id}`"
            >
              <component
                :is="categoryIconFor(row.item.category ?? '')"
                class="mt-0.5 h-4 w-4 text-ink-subtle"
                aria-hidden="true"
              />
              <button type="button" class="min-w-0 text-left" @click="select(row)">
                <div class="flex flex-wrap items-center gap-2 font-ui text-[13px] text-ink">
                  <span>{{ row.item.name }}</span>
                  <span class="text-[10px] uppercase tracking-[0.14em] text-ink-dim">{{ kindNoun(row.item.kind) }}</span>
                  <span
                    class="text-[10px] uppercase tracking-[0.14em] text-ink-dim"
                    :data-testid="`capability-source-${row.item.kind}-${row.item.id}`"
                  >{{ SOURCE_LABELS[row.item.source] ?? row.item.source }}</span>
                  <span
                    :class="['text-[10px] uppercase tracking-[0.14em]', row.item.state.installed ? 'text-signal-ok' : 'text-ink-subtle']"
                    :data-testid="`capability-state-${row.item.kind}-${row.item.id}`"
                  >{{ stateLabel(row.item) }}</span>
                  <span
                    v-if="lifecycleChip(row.item)"
                    :class="['rounded-sm border border-border-muted px-1 text-[10px] uppercase tracking-[0.14em]', lifecycleChip(row.item)!.tone]"
                    :title="lifecycleChip(row.item)!.title"
                    :data-testid="`capability-lifecycle-${row.item.kind}-${row.item.id}`"
                  >{{ lifecycleChip(row.item)!.label }}</span>
                </div>
                <p class="mt-1 max-w-prose text-[11px] text-ink-muted line-clamp-2">{{ row.item.description }}</p>
                <!-- Disabled-with-reason (WP02 posture): visible text, not a tooltip only. -->
                <p
                  v-if="isRevoked(row.item) && !row.item.state.installed"
                  :id="`capability-revoked-reason-${row.item.kind}-${row.item.id}`"
                  class="mt-1 max-w-prose text-[11px] text-ink-subtle"
                  :data-testid="`capability-revoked-${row.item.kind}-${row.item.id}`"
                >
                  {{ REVOKED_INSTALL_COPY }}
                </p>
                <div
                  v-if="rowError[row.key]"
                  class="mt-1 text-[11px] text-signal-danger"
                  role="alert"
                  :data-testid="`capability-row-error-${row.item.kind}-${row.item.id}`"
                >
                  {{ rowError[row.key] }}
                </div>
              </button>
              <div class="flex items-center gap-1">
                <button
                  v-if="!row.item.state.installed"
                  type="button"
                  class="rounded-sm border border-accent-hairline bg-surface-1 px-3 py-1 font-ui text-[12px] text-accent hover:bg-accent-glow disabled:cursor-not-allowed disabled:opacity-50"
                  :disabled="busy[row.key] || isRevoked(row.item)"
                  :title="isRevoked(row.item) ? REVOKED_INSTALL_COPY : undefined"
                  :aria-describedby="isRevoked(row.item) ? `capability-revoked-reason-${row.item.kind}-${row.item.id}` : undefined"
                  :data-testid="`capability-install-${row.item.kind}-${row.item.id}`"
                  @click="install(row)"
                >
                  {{ busy[row.key] ? 'Installing…' : 'Install' }}
                </button>
                <button
                  v-else-if="row.item.state.update_available"
                  type="button"
                  class="rounded-sm border border-accent-hairline bg-surface-1 px-3 py-1 font-ui text-[12px] text-accent hover:bg-accent-glow disabled:opacity-50"
                  :disabled="busy[row.key]"
                  :data-testid="`capability-update-${row.item.kind}-${row.item.id}`"
                  @click="update(row)"
                >
                  Update
                </button>
              </div>
            </li>

            <!-- Fleet-catalog row no provider lists (WP08, from the retired Marketplace). -->
            <li
              v-else-if="row.type === 'catalog'"
              :class="['grid items-start gap-3 px-4 py-3', selectedKey === row.key ? 'bg-surface-2' : '']"
              style="grid-template-columns: 1.25rem 1fr auto"
              :data-testid="`capability-catalog-row-${row.entry.kind}-${row.entry.slug}`"
            >
              <component :is="categoryIconFor('')" class="mt-0.5 h-4 w-4 text-ink-subtle" aria-hidden="true" />
              <button type="button" class="min-w-0 text-left" @click="select(row)">
                <div class="flex flex-wrap items-center gap-2 font-ui text-[13px] text-ink">
                  <span>{{ row.entry.slug }}</span>
                  <span class="text-[10px] uppercase tracking-[0.14em] text-ink-dim">{{ kindNoun(row.entry.kind) }}</span>
                  <span class="text-[10px] uppercase tracking-[0.14em] text-ink-dim">{{ VISIBILITY_LABELS[row.entry.visibility] ?? SOURCE_LABELS[catalogSource(row.entry.visibility)] }}</span>
                  <span
                    v-if="row.residue"
                    class="text-[10px] uppercase tracking-[0.14em] text-ink-muted"
                    title="An earlier version downloaded this item, but nothing on this device uses it."
                    :data-testid="`capability-catalog-downloaded-${row.entry.kind}-${row.entry.slug}`"
                  >Downloaded — not active</span>
                  <span v-else class="text-[10px] uppercase tracking-[0.14em] text-ink-subtle">Not installable yet</span>
                  <span
                    v-if="lifecycleChip(row.entry)"
                    :class="['rounded-sm border border-border-muted px-1 text-[10px] uppercase tracking-[0.14em]', lifecycleChip(row.entry)!.tone]"
                    :title="lifecycleChip(row.entry)!.title"
                    :data-testid="`capability-catalog-lifecycle-${row.entry.kind}-${row.entry.slug}`"
                  >{{ lifecycleChip(row.entry)!.label }}</span>
                </div>
                <p class="mt-1 max-w-prose text-[11px] text-ink-muted line-clamp-2">{{ row.entry.description || 'No description.' }}</p>
                <!-- Disabled-with-reason (WP02 posture): visible text, not a tooltip only. -->
                <p
                  v-if="!row.residue && catalogOnlyKind(row.entry.kind)"
                  :id="reasonElId(row.entry)"
                  class="mt-1 max-w-prose text-[11px] text-ink-subtle"
                  :data-testid="`capability-catalog-unsupported-${row.entry.kind}-${row.entry.slug}`"
                >
                  {{ catalogOnlyKind(row.entry.kind)?.reason }}
                </p>
                <div
                  v-if="rowError[row.key]"
                  class="mt-1 text-[11px] text-signal-danger"
                  role="alert"
                  :data-testid="`capability-catalog-error-${row.entry.kind}-${row.entry.slug}`"
                >
                  {{ rowError[row.key] }}
                </div>
              </button>
              <div class="flex items-center gap-1">
                <button
                  v-if="row.residue"
                  type="button"
                  class="rounded-sm border border-border-muted px-3 py-1 font-ui text-[12px] text-ink-muted hover:bg-surface-2 disabled:opacity-50"
                  :disabled="busy[row.key]"
                  :data-testid="`capability-catalog-remove-download-${row.entry.kind}-${row.entry.slug}`"
                  @click="removeDownload(row.entry, row.key)"
                >
                  {{ busy[row.key] ? 'Removing…' : 'Remove download' }}
                </button>
                <button
                  v-else
                  type="button"
                  class="rounded-sm border border-accent-hairline bg-surface-1 px-3 py-1 font-ui text-[12px] text-accent disabled:cursor-not-allowed disabled:opacity-50"
                  disabled
                  :title="catalogOnlyKind(row.entry.kind)?.reason"
                  :aria-describedby="reasonElId(row.entry)"
                  :data-testid="`capability-catalog-install-${row.entry.kind}-${row.entry.slug}`"
                  @click="refuseCatalogInstall(row)"
                >
                  Install
                </button>
              </div>
            </li>
          </template>

          <!-- Long-term memory moved to Knowledge › Learned (knowledge-home-01DOGF0E
               WP03) — a pointer, not a control. Carried over from KenazToolsPanel;
               delete in the first release after v0.87.x (decision record D3). -->
          <li
            v-if="(kindFilter === 'all' || kindFilter === 'builtin') && !query.trim() && categoryFilter === null"
            class="flex flex-wrap items-center gap-2 px-4 py-3 font-ui text-[12px] text-ink-muted"
            data-testid="memory-moved-pointer"
          >
            <span>Long-term memory moved to Knowledge › Learned.</span>
            <button
              type="button"
              class="rounded-sm border border-border-muted px-2 py-0.5 text-[10px] uppercase tracking-[0.16em] text-ink hover:bg-surface-2"
              data-testid="memory-view-link"
              @click="gotoLearned"
            >
              Open Learned →
            </button>
          </li>
        </ul>

        <!-- Long tail (carried over from the retired Registry browse). -->
        <div
          v-if="kindFilter === 'all' || kindFilter === 'mcp_recipe'"
          :class="['rounded-sm border px-4 py-3', rows.length === 0 ? 'border-accent-hairline bg-accent-glow' : 'border-border-muted bg-surface-1']"
          data-testid="capability-long-tail"
        >
          <div class="font-ui text-[12px] font-semibold text-ink">Don't see your tool?</div>
          <p class="mt-1 max-w-prose text-[11px] text-ink-muted">
            Reach thousands more apps through Automation &amp; iPaaS connectors (Zapier, Make, n8n,
            Pipedream, Composio, Workato), or add any MCP server yourself.
          </p>
          <div class="mt-2 flex flex-wrap items-center gap-2">
            <button
              type="button"
              class="inline-flex items-center gap-1 rounded-sm border border-accent-hairline bg-surface-1 px-2.5 py-1 font-ui text-[11px] text-accent hover:bg-accent-glow"
              data-testid="capability-browse-automation"
              @click="browseAutomation"
            >
              <Zap class="h-3.5 w-3.5" aria-hidden="true" />
              Browse automation connectors
            </button>
            <button
              type="button"
              class="inline-flex items-center gap-1 rounded-sm border border-accent-hairline bg-surface-1 px-2.5 py-1 font-ui text-[11px] text-accent hover:bg-accent-glow"
              data-testid="capability-add-custom"
              @click="entryOpen = 'mcp-custom'"
            >
              <Plus class="h-3.5 w-3.5" aria-hidden="true" />
              Add a custom MCP server
            </button>
            <button
              type="button"
              class="font-ui text-[11px] text-ink-muted underline hover:text-ink"
              data-testid="capability-request-connector"
              @click="requestConnector"
            >
              Request a connector →
            </button>
          </div>
        </div>
      </div>

      <!-- Detail -->
      <aside class="min-w-0 rounded-sm border border-border-muted bg-surface-1 px-4 py-3" data-testid="capability-detail">
        <p v-if="!selected" class="font-ui text-[12px] text-ink-muted" data-testid="capability-detail-empty">
          Select a capability to see its details.
        </p>
        <template v-else-if="selected.type === 'builtin'">
          <div class="space-y-2" :data-testid="`builtin-detail-${selected.tool.id}`">
            <div class="font-ui text-[13px] font-semibold text-ink">{{ selected.tool.name }}</div>
            <p class="text-[11px] text-ink-muted">{{ selected.tool.description }}</p>
            <ul v-if="selected.tool.tools" class="list-none font-mono text-[10px] text-ink-dim">
              <li v-for="t in selected.tool.tools" :key="t">{{ t }}</li>
            </ul>
            <p class="text-[11px] text-ink-subtle">
              Built-in tools are enabled, not installed — use the toggle in the list.
            </p>
          </div>
        </template>
        <CatalogListingDetail
          v-else-if="selected.type === 'catalog'"
          :key="selected.key"
          :entry="selected.entry"
          mode="entry"
          :residue="selected.residue"
          :busy="busy[selected.key]"
          @withdraw="promptWithdraw"
          @remove-download="(e) => removeDownload(e, selected!.key)"
        />
        <template v-else>
          <component
            :is="pluginFor(selected.item.kind)!.detail"
            :key="selected.key"
            ref="detailRef"
            :item="selected.item"
            @changed="load"
          />
          <div
            v-if="!pluginFor(selected.item.kind)!.ownsRemove && selected.item.state.installed"
            class="mt-3 border-t border-border-muted pt-3"
          >
            <p v-if="selected.item.read_only" class="text-[11px] text-ink-muted" :data-testid="`capability-read-only-${selected.item.kind}-${selected.item.id}`">
              {{ selected.item.read_only_reason || 'Managed by your org' }} — it can't be removed here.
            </p>
            <template v-else>
              <button
                v-if="confirmingRemoveKey !== selected.key"
                type="button"
                class="rounded-sm border border-border-muted px-2 py-0.5 font-ui text-[10px] uppercase tracking-[0.14em] text-signal-danger hover:bg-signal-danger/10"
                :data-testid="`capability-remove-${selected.item.kind}-${selected.item.id}`"
                @click="confirmingRemoveKey = selected.key"
              >
                Remove
              </button>
              <div v-else class="flex items-center gap-2">
                <span class="font-ui text-[12px] text-ink">Remove {{ selected.item.name }}?</span>
                <button
                  type="button"
                  class="rounded-sm border border-signal-danger px-2 py-0.5 font-ui text-[10px] uppercase tracking-[0.14em] text-signal-danger"
                  :data-testid="`capability-remove-confirm-${selected.item.kind}-${selected.item.id}`"
                  @click="removeSelected"
                >
                  Remove
                </button>
                <button
                  type="button"
                  class="rounded-sm border border-border-muted px-2 py-0.5 font-ui text-[10px] uppercase tracking-[0.14em] text-ink-dim"
                  @click="confirmingRemoveKey = null"
                >
                  Cancel
                </button>
              </div>
            </template>
          </div>
          <!-- A skill / workflow row that is also a fleet catalog listing:
               its listing facts and Withdraw (WP08, from the Marketplace). -->
          <CatalogListingDetail
            v-if="listingFor(selected.item)"
            class="mt-3"
            :entry="listingFor(selected.item)!"
            mode="listing"
            @withdraw="promptWithdraw"
          />
        </template>
      </aside>
    </div>

    <AddMCPServerModal
      v-if="entryOpen !== null"
      :open="true"
      :initial-tab="entryOpen === 'mcp-paste' ? 'paste' : 'custom'"
      :existing-ids="existingIds"
      @installed="onEntryDone"
      @close="entryOpen = null"
    />

    <!-- Withdraw confirm (fleet-enforcement-truth-01PMZ505 WP11; moved from MarketplaceView). -->
    <div
      v-if="pendingWithdraw !== null"
      class="fixed inset-0 z-50 flex items-center justify-center"
      role="dialog"
      aria-modal="true"
      data-testid="withdraw-confirm-modal"
    >
      <div class="absolute inset-0 bg-modal-overlay" @click="cancelWithdraw" />
      <div class="relative z-10 w-[440px] max-w-[90vw] rounded-md border border-border-muted bg-surface-0 p-5 shadow-lg">
        <h2 class="font-ui text-base font-semibold text-ink">
          Withdraw "{{ pendingWithdraw.slug }}"?
        </h2>
        <p class="mt-2 font-ui text-xs text-ink-muted" data-testid="withdraw-confirm-copy">
          This removes the item from the org catalog listing entirely —
          other members will no longer be able to find or install it. This is
          different from Uninstall, which only removes your own local copy
          and leaves the org listing untouched.
        </p>
        <div v-if="withdrawError" class="mt-2 font-ui text-xs text-signal-danger" role="alert" data-testid="withdraw-error">
          {{ withdrawError }}
        </div>
        <div class="mt-4 flex justify-end gap-2">
          <button
            type="button"
            class="px-3 py-1.5 font-ui text-xs text-ink-dim hover:text-ink"
            data-testid="withdraw-cancel"
            @click="cancelWithdraw"
          >
            Cancel
          </button>
          <button
            type="button"
            class="rounded-sm border border-signal-danger px-3 py-1.5 font-ui text-xs text-signal-danger hover:bg-surface-2 disabled:opacity-50"
            :disabled="withdrawBusy"
            data-testid="withdraw-confirm"
            @click="confirmWithdraw"
          >
            {{ withdrawBusy ? 'Withdrawing…' : 'Withdraw' }}
          </button>
        </div>
      </div>
    </div>
  </section>
</template>
