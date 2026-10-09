<script setup lang="ts">
/**
 * ToolsMenu — the composer's Tools menu (tool-context-budget-01TCBUD01
 * §2.2, FR-K2): every tool server with its tier for this session, whether
 * its definitions are loaded, its cost, and the moves that change the
 * next request — Load for this session, Unload, Pin for this project.
 *
 * Load is Sessions_LoadTools with sticky=true (spec Q-B: user loads are
 * sticky for the session). Unload writes the session layer: the server
 * goes off for this session, which the resolver treats as never sendable
 * even while its tools sit in the activated set (no binding clears
 * activations); Undo unload removes that entry. Pin for project writes the
 * project layer's server tier as full.
 *
 * The meter compares what the next request sends (Tools_SchemaCosts'
 * sendable tokens) with the effective schema budget, and shows the last
 * request's measured tool tokens and cache read from `composition`.
 *
 * Served mode: a boundary panel. The menu's bindings have no serve
 * dispatch, so nothing is fetched or written there.
 */
import { computed, ref, watch } from 'vue';
import NotAvailableInServedMode from '@/components/ui/NotAvailableInServedMode.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import {
  BUILTIN_SERVER,
  TIER_LABELS,
  formatTokens,
  layerServerTier,
  sendableTokens,
  sourceLabel,
  withServerTier,
} from '@/lib/toolExposure';
import type {
  ServerSchemaCost,
  ToolExposure,
  UsageComposition,
} from '@/lib/types';

const props = withDefaults(
  defineProps<{
    open: boolean;
    sessionId: string;
    /** '' for a session outside any project: Pin for project is hidden. */
    projectId?: string;
    composition?: UsageComposition | null;
    servedMode?: boolean;
  }>(),
  { projectId: '', composition: null, servedMode: false },
);

const emit = defineEmits<{
  (e: 'update:open', v: boolean): void;
  (e: 'changed'): void;
}>();

const client = useHarnessClient();

const costs = ref<ServerSchemaCost[]>([]);
const sessionLayer = ref<ToolExposure>({});
const projectLayer = ref<ToolExposure>({});
const budget = ref<number | null>(null);
const loading = ref(false);
const error = ref<string | null>(null);
const notice = ref<string | null>(null);
const busy = ref<string | null>(null);

async function refresh() {
  if (props.servedMode || !props.sessionId) return;
  loading.value = true;
  error.value = null;
  try {
    const [c, s, st, p] = await Promise.all([
      client.tools.schemaCosts(props.sessionId, ''),
      client.sessions.getToolExposure(props.sessionId),
      client.settings.getToolExposure(),
      props.projectId ? client.projects.getToolExposure(props.projectId) : Promise.resolve({}),
    ]);
    costs.value = c;
    sessionLayer.value = s.exposure ?? {};
    budget.value = st.effectiveSchemaBudgetTokens || null;
    projectLayer.value = p;
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}

watch(
  () => [props.open, props.sessionId, props.projectId] as const,
  ([open]) => {
    if (open) {
      notice.value = null;
      void refresh();
    }
  },
  { immediate: true },
);

const nextTokens = computed(() => sendableTokens(costs.value));
const overBudget = computed(() => budget.value !== null && nextTokens.value > budget.value);
const meterPct = computed(() =>
  budget.value ? Math.min(100, Math.round((nextTokens.value / budget.value) * 100)) : 0,
);

type RowState = 'stopped' | 'pinned' | 'unloaded' | 'off' | 'full' | 'loaded' | 'summary';

function rowState(c: ServerSchemaCost): RowState {
  if (!c.running) return 'stopped';
  if (c.pinned) return 'pinned';
  if (layerServerTier(sessionLayer.value, c.server) === 'off') return 'unloaded';
  if (c.tier === 'off') return 'off';
  if (c.tier === 'full') return 'full';
  if (c.sendableTokenEst > 0) return 'loaded';
  return 'summary';
}

function stateText(c: ServerSchemaCost): string {
  switch (rowState(c)) {
    case 'stopped':
      return `Not running (${c.state || 'stopped'})`;
    case 'pinned':
      return `${TIER_LABELS[c.tier]} — set by your organisation`;
    case 'unloaded':
      return 'Unloaded for this session';
    case 'off':
      return `Off — ${sourceLabel(c.source)}`;
    case 'full':
      return `Loaded — ${sourceLabel(c.source)}`;
    case 'loaded':
      return 'Loaded for this session';
    default:
      return 'Summary — the model can load it';
  }
}

function canLoad(c: ServerSchemaCost): boolean {
  return rowState(c) === 'summary';
}

function canUnload(c: ServerSchemaCost): boolean {
  if (c.server === BUILTIN_SERVER) return false;
  const s = rowState(c);
  return s === 'full' || s === 'loaded';
}

function canPin(c: ServerSchemaCost): boolean {
  if (!props.projectId || !c.running || c.pinned) return false;
  return layerServerTier(projectLayer.value, c.server) !== 'full';
}

function isPinnedForProject(c: ServerSchemaCost): boolean {
  return !!props.projectId && layerServerTier(projectLayer.value, c.server) === 'full';
}

async function act(server: string, fn: () => Promise<void>) {
  busy.value = server;
  error.value = null;
  notice.value = null;
  try {
    await fn();
    emit('changed');
    await refresh();
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = null;
  }
}

function load(c: ServerSchemaCost) {
  return act(c.server, async () => {
    const r = await client.sessions.loadTools(props.sessionId, [c.server], [], true);
    if (r.not_loaded.length > 0) {
      notice.value = r.not_loaded.map((n) => `${n.name}: ${n.reason}`).join('; ');
    }
  });
}

function unload(c: ServerSchemaCost) {
  return act(c.server, () =>
    client.sessions.setToolExposure(
      props.sessionId,
      withServerTier(sessionLayer.value, c.server, 'off'),
    ),
  );
}

function undoUnload(c: ServerSchemaCost) {
  return act(c.server, () =>
    client.sessions.setToolExposure(
      props.sessionId,
      withServerTier(sessionLayer.value, c.server, null),
    ),
  );
}

function pin(c: ServerSchemaCost) {
  return act(c.server, async () => {
    const current = await client.projects.getToolExposure(props.projectId);
    await client.projects.setToolExposure(
      props.projectId,
      withServerTier(current, c.server, 'full'),
    );
  });
}

function toggle() {
  emit('update:open', !props.open);
}
</script>

<template>
  <div class="relative" data-testid="tools-menu">
    <button
      type="button"
      class="flex items-center gap-1.5 rounded-sm px-1.5 py-0.5 hover:bg-surface-2 hover:text-ink"
      :aria-expanded="open"
      data-testid="tools-menu-toggle"
      @click="toggle"
    >
      <span class="uppercase tracking-[0.14em] text-ink-subtle">tools</span>
      <span
        v-if="composition"
        class="font-mono text-ink-muted"
        data-testid="tools-menu-chip-tokens"
      >{{ formatTokens(composition.tools) }}</span>
      <span aria-hidden="true">▾</span>
    </button>
    <div
      v-if="open"
      class="absolute z-30 bottom-full mb-1 left-0 max-h-96 w-[26rem] overflow-y-auto rounded-sm border border-border-muted bg-surface-1 shadow-lg"
      role="dialog"
      aria-label="Tools for this session"
      data-testid="tools-menu-panel"
    >
      <NotAvailableInServedMode
        v-if="servedMode"
        feature="The Tools menu"
        reason="Loading, unloading and pinning tools for a session is not wired into the in-workbench build yet. The session still sends the tools its settings select."
      />
      <template v-else>
        <div class="space-y-1 border-b border-border-muted px-3 py-2" data-testid="tools-menu-meter">
          <div class="flex items-center justify-between text-[11px]">
            <span class="text-ink-muted">Next request</span>
            <span class="font-mono text-ink" data-testid="tools-menu-next-tokens">
              {{ formatTokens(nextTokens) }}<template v-if="budget"> of {{ budget.toLocaleString() }} budget</template>
            </span>
          </div>
          <span v-if="budget" class="block h-1 w-full overflow-hidden rounded-full bg-surface-2">
            <span
              class="block h-full"
              :class="overBudget ? 'bg-signal-danger' : 'bg-signal-ok'"
              :style="{ width: meterPct + '%' }"
            ></span>
          </span>
          <p v-if="composition" class="text-[10px] text-ink-subtle" data-testid="tools-menu-last">
            Last request: {{ composition.tools.toLocaleString() }} tokens of tool definitions
            ({{ composition.toolsFull }} sent<template v-if="composition.toolsSummary">, {{ composition.toolsSummary }} listed by summary</template>),
            {{ composition.cached.toLocaleString() }} cached.
          </p>
          <p
            v-if="composition?.budgetWarning"
            class="text-[11px] text-signal-warn"
            role="status"
            data-testid="tools-menu-budget-warning"
          >
            {{ composition.budgetWarning }}
          </p>
        </div>
        <div v-if="error" class="px-3 py-2 text-[11px] text-signal-danger" role="alert" data-testid="tools-menu-error">
          {{ error }}
        </div>
        <div v-if="notice" class="px-3 py-2 text-[11px] text-ink-muted" role="status" data-testid="tools-menu-notice">
          {{ notice }}
        </div>
        <div v-if="loading && costs.length === 0" class="px-3 py-2 text-[11px] text-ink-muted">
          Loading tools…
        </div>
        <div
          v-else-if="!error && costs.length === 0"
          class="px-3 py-2 text-[11px] text-ink-muted"
          data-testid="tools-menu-empty"
        >
          No tool servers for this session.
        </div>
        <ul v-else class="divide-y divide-border-muted">
          <li
            v-for="c in costs"
            :key="c.server"
            class="px-3 py-2"
            :data-testid="`tools-menu-row-${c.server}`"
          >
            <div class="flex items-center justify-between gap-2">
              <span class="font-mono text-[12px] text-ink">{{ c.server }}</span>
              <span class="font-mono text-[11px] text-ink-subtle">
                {{ c.running ? formatTokens(c.tokenEst) : '—' }}
              </span>
            </div>
            <div class="mt-0.5 text-[11px] text-ink-muted" :data-testid="`tools-menu-state-${c.server}`">
              {{ stateText(c) }}<template v-if="isPinnedForProject(c)"> · pinned for this project</template>
            </div>
            <div class="mt-1 flex flex-wrap gap-1.5">
              <button
                v-if="canLoad(c)"
                type="button"
                class="rounded-sm border border-accent-hairline px-2 py-0.5 text-[11px] text-accent hover:bg-accent-glow disabled:opacity-50"
                :disabled="busy !== null"
                :data-testid="`tools-menu-load-${c.server}`"
                @click="load(c)"
              >
                Load for this session
              </button>
              <button
                v-if="rowState(c) === 'unloaded'"
                type="button"
                class="rounded-sm border border-accent-hairline px-2 py-0.5 text-[11px] text-accent hover:bg-accent-glow disabled:opacity-50"
                :disabled="busy !== null"
                :data-testid="`tools-menu-undo-${c.server}`"
                @click="undoUnload(c)"
              >
                Undo unload
              </button>
              <button
                v-if="canUnload(c)"
                type="button"
                class="rounded-sm border border-border-muted px-2 py-0.5 text-[11px] text-ink-muted hover:bg-surface-2 disabled:opacity-50"
                :disabled="busy !== null"
                :data-testid="`tools-menu-unload-${c.server}`"
                @click="unload(c)"
              >
                Unload
              </button>
              <button
                v-if="canPin(c)"
                type="button"
                class="rounded-sm border border-border-muted px-2 py-0.5 text-[11px] text-ink-muted hover:bg-surface-2 disabled:opacity-50"
                :disabled="busy !== null"
                :data-testid="`tools-menu-pin-${c.server}`"
                @click="pin(c)"
              >
                Pin for this project
              </button>
            </div>
          </li>
        </ul>
        <p class="border-t border-border-muted px-3 py-2 text-[10px] text-ink-subtle">
          Unload turns the server off for this session; the model cannot load it again until you
          undo. Defaults for every session are in Capabilities.
        </p>
      </template>
    </div>
  </div>
</template>
