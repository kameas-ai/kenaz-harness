<script setup lang="ts">
/**
 * CapabilitySurface — the one "Add capability" list + detail surface
 * (install-framework-01DOGF0B, FR-4). One list over every install provider
 * (Capability_List: installed state read from each runtime consumer) plus
 * the built-in tool toggles, with kind chips, source chips and search;
 * the detail pane is the row kind's plugin (plugins/registry.ts).
 *
 * WP04 renders the MCP-recipe provider and replaces Tools › Registry (the
 * retired RegistryTab.vue browse list) and KenazToolsPanel.vue. Rail
 * entries are untouched until Phase 4 — the surface mounts inside the
 * Tools view.
 *
 * Sources a provider could not list arrive as `unavailable` rows with a
 * reason and are rendered as rows, never as a hidden tab (P-5).
 */
import { computed, nextTick, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { useEventStream } from '@/lib/useEventStream';
import { categoryIconFor, categoryLabel } from '@/lib/recipeCategories';
import { Search, Zap, Plus } from '@/shell/icons';
import AddMCPServerModal from '@/views/tools/AddMCPServerModal.vue';
import { BUILTIN_TOOLS, type BuiltinTool } from './plugins/builtinTools';
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
} from '@/lib/types';

const client = useHarnessClient();
const router = useRouter();

/** Category the long-tail callout points at (carried over from RegistryTab). */
const AUTOMATION_CATEGORY = 'automation';
/** Prefilled GitHub new-issue URL for "Request a connector". */
const REQUEST_CONNECTOR_URL =
  'https://github.com/kameas-ai/kenaz-harness/issues/new?title=Connector%20request%3A%20';

type KindFilter = 'all' | 'builtin' | CapabilityKind;
type SourceFilter = 'all' | CapabilitySource;

type Row =
  | { type: 'builtin'; key: string; tool: BuiltinTool }
  | { type: 'item'; key: string; item: CapabilityItem };

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

// One event for every provider (FR-3): any install / uninstall — from
// this surface, a per-kind flow, the Marketplace, or a fleet sync pull —
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
  void refreshBuiltins();
});

// ── filters ──────────────────────────────────────────────────────────
const query = ref('');
const kindFilter = ref<KindFilter>('all');
const sourceFilter = ref<SourceFilter>('all');
const categoryFilter = ref<string | null>(null);

const kindChips = computed(() => [
  { id: 'all' as KindFilter, label: 'All' },
  { id: 'builtin' as KindFilter, label: 'Built-in tools' },
  ...KIND_PLUGINS.map((p) => ({ id: p.kind as KindFilter, label: p.label })),
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
  return out;
});

const visibleUnavailable = computed(() =>
  unavailable.value.filter(
    (u) =>
      (kindFilter.value === 'all' || kindFilter.value === u.kind) &&
      (sourceFilter.value === 'all' || !u.source || sourceFilter.value === u.source),
  ),
);

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
  return it ? { type: 'item', key: selectedKey.value, item: it } : null;
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

function kindNoun(kind: CapabilityKind): string {
  return pluginFor(kind)?.noun ?? kind;
}

function gotoLearned() {
  void router.push('/knowledge/learned');
}
</script>

<template>
  <section class="px-6 py-4 space-y-3" data-testid="capability-surface">
    <header class="flex flex-wrap items-center justify-between gap-2">
      <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Add capability
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
        v-model="query"
        type="search"
        aria-label="Search capabilities"
        placeholder="Search tools, MCP servers…"
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
          <span class="text-ink">{{ pluginFor(u.kind)?.label ?? u.kind }}</span>
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
          {{ query.trim() ? `Nothing matches “${query.trim()}”.` : 'Nothing to show for these filters.' }}
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
              v-else
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
                </div>
                <p class="mt-1 max-w-prose text-[11px] text-ink-muted line-clamp-2">{{ row.item.description }}</p>
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
                  :disabled="busy[row.key]"
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
  </section>
</template>
