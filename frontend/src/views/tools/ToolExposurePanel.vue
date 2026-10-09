<script setup lang="ts">
/**
 * ToolExposurePanel — Settings → Capabilities' tool-definition controls
 * (tool-context-budget-01TCBUD01 WP06, FR-K1): per-server tier, per-tool
 * override drawer, schema cost when loaded, project defaults, schema
 * budget and activation TTL.
 *
 * Scope: "Your default" writes the user layer (Settings_SetToolExposure);
 * a project writes that project's layer (Projects_SetToolExposure), so a
 * project's sessions resolve its tiers first. Every row shows the tier the
 * resolver reports for the scope (Tools_SchemaCosts), not the stored
 * value, so what the row says is what the next request in that scope gets.
 *
 * Organisation rows are read-only ("set by your organisation"): an org pin
 * is decided before any layer this panel can write (spec §2.1 step 1) and
 * a tool the org added to the hot set (hot_set_extra) is always full. An
 * org default (pinned:false) sits below the user layer and stays editable.
 * An org-set schema budget (Settings.org.schemaBudgetTokens) disables the
 * budget input (tool-context-budget-01TCBUD01 WP07).
 *
 * Mounted only by ToolsView, which renders a served-mode boundary panel
 * instead of this surface.
 */
import { computed, onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import {
  BUILTIN_SERVER,
  TIER_LABELS,
  explainExposureError,
  formatTokens,
  isOrgLocked,
  layerServerTier,
  layerToolTier,
  serverStateLabel,
  sourceLabel,
  sourceSentence,
  withServerTier,
  withToolTier,
} from '@/lib/toolExposure';
import type {
  Project,
  ServerSchemaCost,
  ToolExposure,
  ToolExposureSettings,
  ToolExposureTier,
} from '@/lib/types';

const client = useHarnessClient();

const TIERS: ToolExposureTier[] = ['full', 'summary', 'off'];

const projects = ref<Project[]>([]);
/** '' = the user's default; otherwise a project id. */
const scope = ref('');
const settings = ref<ToolExposureSettings | null>(null);
const projectLayer = ref<ToolExposure>({});
const costs = ref<ServerSchemaCost[]>([]);
const loading = ref(false);
const loadError = ref<string | null>(null);
const saveError = ref<string | null>(null);
const saving = ref(false);
const expanded = ref<string | null>(null);

const layer = computed<ToolExposure>(() =>
  scope.value ? projectLayer.value : (settings.value?.exposure ?? {}),
);
const inheritLabel = computed(() => (scope.value ? 'Same as your default' : 'Harness default'));
/** The organisation's schema budget; 0 = not set by the organisation. */
const orgBudget = computed(() => settings.value?.org?.schemaBudgetTokens ?? 0);

async function loadCosts() {
  costs.value = await client.tools.schemaCosts('', scope.value);
}

async function load() {
  loading.value = true;
  loadError.value = null;
  try {
    const [s, p] = await Promise.all([
      client.settings.getToolExposure(),
      client.projects.list().catch(() => [] as Project[]),
    ]);
    settings.value = s;
    projects.value = p;
    projectLayer.value = scope.value ? await client.projects.getToolExposure(scope.value) : {};
    await loadCosts();
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}

async function onScopeChange(event: Event) {
  scope.value = (event.target as HTMLSelectElement).value;
  expanded.value = null;
  saveError.value = null;
  await load();
}

// The edit is applied to the layer as stored at write time (re-read
// first), so a change made elsewhere since mount is not overwritten.
async function writeLayer(edit: (current: ToolExposure) => ToolExposure) {
  saving.value = true;
  saveError.value = null;
  try {
    if (scope.value) {
      const pid = scope.value;
      const next = edit(await client.projects.getToolExposure(pid));
      await client.projects.setToolExposure(pid, next);
      projectLayer.value = next;
    } else {
      const current = await client.settings.getToolExposure();
      const s: ToolExposureSettings = { ...current, exposure: edit(current.exposure ?? {}) };
      await client.settings.setToolExposure(s);
      settings.value = s;
    }
    await loadCosts();
  } catch (e) {
    saveError.value = explainExposureError(e instanceof Error ? e.message : String(e));
  } finally {
    saving.value = false;
  }
}

/** The built-in hot set's bare names, which a server-wide built-in tier must not take below full. */
function hotNames(server: string): string[] {
  if (server !== BUILTIN_SERVER) return [];
  return (costs.value.find((c) => c.server === server)?.tools ?? [])
    .filter((t) => t.hot)
    .map((t) => t.name);
}

function onServerTier(server: string, event: Event) {
  const v = (event.target as HTMLSelectElement).value as ToolExposureTier | '';
  const keep = hotNames(server);
  void writeLayer((current) => withServerTier(current, server, v, keep));
}

function onToolTier(server: string, tool: string, event: Event) {
  const v = (event.target as HTMLSelectElement).value as ToolExposureTier | '';
  void writeLayer((current) => withToolTier(current, server, tool, v));
}

// Budget and TTL live on the user's settings only; 0 = harness default.
async function onNumber(field: 'schemaBudgetTokens' | 'activationTtlTurns', event: Event) {
  if (!settings.value) return;
  const raw = (event.target as HTMLInputElement).value.trim();
  const n = raw === '' ? 0 : Math.floor(Number(raw));
  if (!Number.isFinite(n) || n < 0) {
    saveError.value = 'Enter a whole number, or leave it blank for the default.';
    return;
  }
  saving.value = true;
  saveError.value = null;
  try {
    const current = await client.settings.getToolExposure();
    await client.settings.setToolExposure({ ...current, [field]: n });
    settings.value = await client.settings.getToolExposure();
  } catch (e) {
    saveError.value = explainExposureError(e instanceof Error ? e.message : String(e));
  } finally {
    saving.value = false;
  }
}

function costText(c: ServerSchemaCost): string {
  if (!c.running) return `Schema cost unknown — server not running (${serverStateLabel(c.state)})`;
  return `Schema cost ${formatTokens(c.tokenEst)} tokens when loaded`;
}

/** The organisation decided the server-wide tier (every tool), not only some tools. */
function serverPinned(c: ServerSchemaCost): boolean {
  return isOrgLocked(c.source);
}

/** The organisation decided at least one of the server's tools. */
function someOrgLocked(c: ServerSchemaCost): boolean {
  return c.pinned || c.tools.some((t) => isOrgLocked(t.source));
}

function toggle(server: string) {
  expanded.value = expanded.value === server ? null : server;
}

onMounted(() => void load());
</script>

<template>
  <section class="px-6 py-4 space-y-3" data-testid="tool-exposure-panel">
    <header class="space-y-1">
      <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Tool definitions
      </h2>
      <p class="max-w-prose font-ui text-[11px] text-ink-muted">
        Full sends a server's tool definitions with every request. Summary lists the server in
        one line and the model loads it when it needs it. Off hides it.
      </p>
    </header>

    <div class="flex flex-wrap items-end gap-4 font-ui text-[12px]">
      <label class="flex flex-col gap-1 text-ink-muted">
        <span>Defaults for</span>
        <select
          class="rounded-sm border border-border-muted bg-surface-0 px-2 py-1 text-ink"
          :value="scope"
          data-testid="tool-exposure-scope"
          @change="onScopeChange"
        >
          <option value="">Your default (every project)</option>
          <option v-for="p in projects" :key="p.id" :value="p.id">{{ p.name }}</option>
        </select>
      </label>
      <template v-if="!scope && settings">
        <label class="flex flex-col gap-1 text-ink-muted">
          <span>Schema budget (tokens)</span>
          <input
            type="number"
            min="0"
            class="w-32 rounded-sm border border-border-muted bg-surface-0 px-2 py-1 text-ink"
            :value="settings.schemaBudgetTokens || ''"
            :placeholder="String(settings.effectiveSchemaBudgetTokens)"
            :disabled="saving || orgBudget > 0"
            :title="orgBudget > 0 ? 'Set by your organisation' : undefined"
            data-testid="tool-exposure-budget"
            @change="onNumber('schemaBudgetTokens', $event)"
          />
        </label>
        <label class="flex flex-col gap-1 text-ink-muted">
          <span>Unload after (turns unused)</span>
          <input
            type="number"
            min="0"
            class="w-24 rounded-sm border border-border-muted bg-surface-0 px-2 py-1 text-ink"
            :value="settings.activationTtlTurns || ''"
            :placeholder="String(settings.effectiveActivationTtlTurns)"
            :disabled="saving"
            data-testid="tool-exposure-ttl"
            @change="onNumber('activationTtlTurns', $event)"
          />
        </label>
      </template>
    </div>
    <p
      v-if="!scope && orgBudget > 0"
      class="font-ui text-[11px] text-ink-subtle"
      data-testid="tool-exposure-budget-org"
    >
      Schema budget {{ orgBudget.toLocaleString() }} tokens — set by your organisation; it can't be
      changed here.
    </p>
    <p v-if="!scope && settings" class="font-ui text-[11px] text-ink-subtle" data-testid="tool-exposure-effective">
      In effect: {{ settings.effectiveSchemaBudgetTokens.toLocaleString() }} tokens of tool
      definitions per request at most (capped at 15% of the model's window); loaded tools unload
      after {{ settings.effectiveActivationTtlTurns }} turns unused.
    </p>

    <div
      v-if="loadError"
      class="rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
      role="alert"
      data-testid="tool-exposure-load-error"
    >
      {{ loadError }}
    </div>
    <div
      v-if="saveError"
      class="rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
      role="alert"
      data-testid="tool-exposure-save-error"
    >
      {{ saveError }}
    </div>

    <div v-if="loading && costs.length === 0" class="font-ui text-[12px] text-ink-muted">
      Loading tool servers…
    </div>
    <div
      v-else-if="!loadError && costs.length === 0"
      class="font-ui text-[12px] text-ink-muted"
      data-testid="tool-exposure-empty"
    >
      No tool servers to configure.
    </div>

    <ul
      v-else-if="costs.length > 0"
      class="divide-y divide-border-muted rounded-sm border border-border-muted bg-surface-1"
      data-testid="tool-exposure-list"
    >
      <li v-for="c in costs" :key="c.server" class="px-4 py-3" :data-testid="`tool-exposure-row-${c.server}`">
        <div class="grid items-start gap-3" style="grid-template-columns: 1fr auto">
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2 font-ui text-[13px] text-ink">
              <span class="font-mono">{{ c.server }}</span>
              <span class="text-[10px] uppercase tracking-[0.14em] text-ink-dim">
                {{ c.toolCount }} tool{{ c.toolCount === 1 ? '' : 's' }}
              </span>
              <span
                class="text-[10px] uppercase tracking-[0.14em] text-ink-muted"
                :data-testid="`tool-exposure-resolved-${c.server}`"
              >{{ TIER_LABELS[c.tier] }}<template v-if="c.source"> · {{ sourceLabel(c.source) }}</template></span>
            </div>
            <p class="mt-1 font-ui text-[11px] text-ink-muted" :data-testid="`tool-exposure-cost-${c.server}`">
              {{ costText(c) }}
            </p>
            <p
              v-if="someOrgLocked(c)"
              class="mt-1 font-ui text-[11px] text-ink-subtle"
              :data-testid="`tool-exposure-pinned-${c.server}`"
            >
              <template v-if="serverPinned(c)">{{ sourceSentence(c.source) }} — it can't be changed here.</template>
              <template v-else>Some tools are {{ sourceLabel('org_pin') }}; those can't be changed here.</template>
            </p>
          </div>
          <div class="flex items-center gap-2">
            <select
              class="rounded-sm border border-border-muted bg-surface-0 px-2 py-1 font-ui text-[11px] text-ink disabled:opacity-50"
              :value="layerServerTier(layer, c.server)"
              :disabled="saving || serverPinned(c)"
              :aria-label="`Tier for ${c.server}`"
              :data-testid="`tool-exposure-tier-${c.server}`"
              @change="onServerTier(c.server, $event)"
            >
              <option value="">{{ inheritLabel }}</option>
              <option v-for="t in TIERS" :key="t" :value="t">{{ TIER_LABELS[t] }}</option>
            </select>
            <button
              v-if="c.tools.length > 0"
              type="button"
              class="rounded-sm border border-border-muted px-2 py-1 font-ui text-[11px] text-ink-muted hover:bg-surface-2"
              :aria-expanded="expanded === c.server"
              :data-testid="`tool-exposure-drawer-toggle-${c.server}`"
              @click="toggle(c.server)"
            >
              Per tool
            </button>
          </div>
        </div>
        <p
          v-if="c.server === BUILTIN_SERVER"
          class="mt-1 font-ui text-[11px] text-ink-subtle"
        >
          Setting this server to Summary or Off keeps the core tools (files, shell, web,
          load_tools) full; change them one by one under Per tool.
        </p>
        <ul
          v-if="expanded === c.server"
          class="mt-2 space-y-1 border-t border-border-muted pt-2"
          :data-testid="`tool-exposure-drawer-${c.server}`"
        >
          <li
            v-for="t in c.tools"
            :key="t.name"
            class="grid items-center gap-3 font-ui text-[11px]"
            style="grid-template-columns: 1fr auto auto"
          >
            <span class="min-w-0 truncate font-mono text-ink">{{ t.name }}</span>
            <span class="text-ink-subtle">{{ formatTokens(t.tokenEst) }} · {{ TIER_LABELS[t.tier] }}<template
                v-if="isOrgLocked(t.source)"
              > · <span :data-testid="`tool-exposure-tool-org-${c.server}-${t.name}`">{{ sourceLabel(t.source) }}</span></template></span>
            <select
              class="rounded-sm border border-border-muted bg-surface-0 px-1.5 py-0.5 text-ink disabled:opacity-50"
              :value="layerToolTier(layer, c.server, t.name)"
              :disabled="saving || isOrgLocked(t.source)"
              :aria-label="`Tier for ${c.server} ${t.name}`"
              :data-testid="`tool-exposure-tool-tier-${c.server}-${t.name}`"
              @change="onToolTier(c.server, t.name, $event)"
            >
              <option value="">Same as server</option>
              <option v-for="tier in TIERS" :key="tier" :value="tier">{{ TIER_LABELS[tier] }}</option>
            </select>
          </li>
        </ul>
      </li>
    </ul>
  </section>
</template>
