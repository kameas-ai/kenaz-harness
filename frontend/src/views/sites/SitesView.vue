<script setup lang="ts">
/**
 * SitesView — Fleet Sites hosting surface (sites-ui-01NSITE06).
 *
 * Agent-first design: sites are primarily created and managed by agents via
 * the fleet-sites MCP server. This view provides a read + light-action pane
 * (deploy from folder, view logs, delete) over the fleet API.
 *
 * All fleet I/O is gated on sites_hosting capability; the component itself is
 * only mounted when the route is reachable (gated in LeftRail + main.ts).
 *
 * Deploy progress is streamed via the "sites:deploy:progress" Wails topic
 * (window.runtime.EventsOn) and renders in a live stage panel.
 */
import { onMounted, onBeforeUnmount, ref } from 'vue';
import SettingsShell from '@/views/settings/SettingsShell.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { SiteSummary, DeployProgressEvent, SiteEnvEntry } from '@/lib/types';

const client = useHarnessClient();

// ── list state ────────────────────────────────────────────────────────────
const sites = ref<SiteSummary[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);

async function refresh() {
  loading.value = true;
  error.value = null;
  try {
    sites.value = await client.sites.list();
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load sites.';
    sites.value = [];
  } finally {
    loading.value = false;
  }
}

// ── deploy flow ───────────────────────────────────────────────────────────
const deployBusy = ref(false);
const deployError = ref<string | null>(null);
const deployStages = ref<DeployProgressEvent[]>([]);
const deployDone = ref(false);

let unsubscribeProgress: (() => void) | undefined;

function subscribeProgress() {
  if (typeof window !== 'undefined' && window.runtime?.EventsOn) {
    unsubscribeProgress = window.runtime.EventsOn(
      'sites:deploy:progress',
      (payload: unknown) => {
        const ev = payload as DeployProgressEvent;
        deployStages.value = [...deployStages.value, ev];
        if (ev.stage === 'done' || ev.stage === 'error') {
          deployDone.value = true;
        }
      },
    );
  }
}

function unsubscribe() {
  if (unsubscribeProgress) {
    unsubscribeProgress();
    unsubscribeProgress = undefined;
  }
}

async function pickAndDeploy() {
  deployError.value = null;
  deployStages.value = [];
  deployDone.value = false;

  let rootDir: string;
  try {
    rootDir = await client.tools.pickDirectory('Select site folder', '');
  } catch {
    // User cancelled the dialog.
    return;
  }
  if (!rootDir) return;

  deployBusy.value = true;
  subscribeProgress();
  try {
    await client.sites.deploy(rootDir);
    await refresh();
  } catch (e) {
    deployError.value = e instanceof Error ? e.message : 'Deploy failed.';
  } finally {
    deployBusy.value = false;
    deployDone.value = true;
    unsubscribe();
  }
}

function resetDeploy() {
  deployStages.value = [];
  deployDone.value = false;
  deployError.value = null;
}

// ── logs modal ────────────────────────────────────────────────────────────
const logsModalSite = ref<string | null>(null);
const logsContent = ref<string | null>(null);
const logsBusy = ref(false);
const logsError = ref<string | null>(null);

async function openLogs(site: string) {
  logsModalSite.value = site;
  logsContent.value = null;
  logsError.value = null;
  logsBusy.value = true;
  try {
    logsContent.value = await client.sites.logs(site, 200);
  } catch (e) {
    logsError.value = e instanceof Error ? e.message : 'Failed to fetch logs.';
  } finally {
    logsBusy.value = false;
  }
}

function closeLogs() {
  logsModalSite.value = null;
  logsContent.value = null;
}

// ── env vars modal (fleet-enforcement-truth-01PMZ505 WP09) ─────────────────
//
// Values are write-only: Sites_EnvList never returns a value (only names +
// metadata), and the form field below is cleared immediately after a
// successful Sites_EnvSet call — this component never holds a submitted
// value in state past that point.
const envModalSite = ref<string | null>(null);
const envEntries = ref<SiteEnvEntry[]>([]);
const envLoading = ref(false);
const envListError = ref<string | null>(null);
const envDrafts = ref<Record<string, string>>({});
const envSaving = ref<string | null>(null);
const envSaveError = ref<string | null>(null);

async function openEnvModal(site: string) {
  envModalSite.value = site;
  envEntries.value = [];
  envListError.value = null;
  envSaveError.value = null;
  envDrafts.value = {};
  envLoading.value = true;
  try {
    envEntries.value = await client.sites.envList(site);
  } catch (e) {
    envListError.value = e instanceof Error ? e.message : 'Failed to load env vars.';
  } finally {
    envLoading.value = false;
  }
}

function closeEnvModal() {
  envModalSite.value = null;
  envEntries.value = [];
  envDrafts.value = {};
  envSaveError.value = null;
}

async function saveEnvVar(name: string) {
  const site = envModalSite.value;
  const value = envDrafts.value[name];
  if (!site || !value) return;
  envSaving.value = name;
  envSaveError.value = null;
  try {
    await client.sites.envSet(site, { [name]: value });
    // Clear the draft immediately — never hold a submitted value in state.
    delete envDrafts.value[name];
    envEntries.value = await client.sites.envList(site);
  } catch (e) {
    envSaveError.value = e instanceof Error ? e.message : `Failed to set ${name}.`;
  } finally {
    envSaving.value = null;
  }
}

function envSetDisplay(entry: SiteEnvEntry): string {
  if (!entry.setAt) return 'Not set';
  const d = new Date(entry.setAt);
  if (isNaN(d.getTime())) return 'Set';
  return `Set ${relativeTime(entry.setAt)}`;
}

// ── delete confirm ────────────────────────────────────────────────────────
const deletingConfirmSite = ref<string | null>(null);
const deletingBusy = ref(false);
const deleteError = ref<string | null>(null);

function promptDelete(site: string) {
  deletingConfirmSite.value = site;
  deleteError.value = null;
}

function cancelDelete() {
  deletingConfirmSite.value = null;
}

async function commitDelete() {
  const site = deletingConfirmSite.value;
  if (!site) return;
  deletingBusy.value = true;
  deleteError.value = null;
  try {
    await client.sites.delete(site);
    deletingConfirmSite.value = null;
    await refresh();
  } catch (e) {
    deleteError.value = e instanceof Error ? e.message : `Failed to delete ${site}.`;
  } finally {
    deletingBusy.value = false;
  }
}

// ── helpers ───────────────────────────────────────────────────────────────
function statusClass(status: string): string {
  if (status === 'live') return 'text-signal-ok';
  if (status === 'failed' || status === 'error') return 'text-signal-danger';
  return 'text-ink-muted';
}

function relativeTime(iso: string | undefined): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (isNaN(d.getTime())) return '—';
  const diff = Date.now() - d.getTime();
  const mins = Math.floor(diff / 60_000);
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}

onMounted(() => {
  void refresh();
});

onBeforeUnmount(() => {
  unsubscribe();
});
</script>

<template>
  <SettingsShell
    number="11"
    section="SITES"
    title="Hosted sites"
    subtitle="Static and dynamic sites deployed via fleet. Use the fleet-sites MCP server to create and manage sites from an agent session."
  >
    <!-- loading -->
    <div v-if="loading" class="px-6 py-4 font-ui text-sm text-ink-muted">
      Loading sites…
    </div>

    <!-- list error -->
    <div
      v-else-if="error"
      class="px-6 py-4 font-ui text-sm text-signal-danger"
      role="alert"
      data-testid="sites-error"
    >
      {{ error }}
    </div>

    <!-- empty state -->
    <div
      v-else-if="sites.length === 0"
      class="px-6 py-6 font-ui text-sm text-ink-muted"
      data-testid="sites-empty"
    >
      <div class="text-ink">No sites deployed</div>
      <p class="mt-2 max-w-prose text-ink-muted">
        Sites are created and managed by Claude via the fleet-sites MCP server.
        Add it to your agent session to get started:
      </p>
      <pre
        class="mt-3 px-3 py-2 rounded bg-surface-1 font-mono text-[11px] text-ink select-all"
        data-testid="sites-mcp-oneliner"
      >claude mcp add fleet-sites</pre>
      <p class="mt-3 text-ink-muted">
        You can also deploy a local folder directly using the button below.
      </p>
    </div>

    <!-- sites table -->
    <table
      v-else
      class="w-full font-ui text-[12px] text-ink"
      data-testid="sites-table"
    >
      <thead class="bg-surface-1 text-ink-muted">
        <tr>
          <th class="text-left px-4 py-2 font-medium">Name</th>
          <th class="text-left px-4 py-2 font-medium">Type</th>
          <th class="text-left px-4 py-2 font-medium">URL</th>
          <th class="text-left px-4 py-2 font-medium">Status</th>
          <th class="text-left px-4 py-2 font-medium">Deployed</th>
          <th class="text-right px-4 py-2 font-medium"></th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="s in sites"
          :key="s.name"
          class="border-t border-border-muted hover:bg-surface-1"
          :data-testid="`site-row-${s.name}`"
        >
          <td class="px-4 py-2 font-mono">{{ s.name }}</td>
          <td class="px-4 py-2">
            <span
              class="text-[11px] uppercase tracking-[0.12em]"
              :class="s.kind === 'static' ? 'text-accent' : 'text-ink-muted'"
              :data-testid="`site-kind-${s.name}`"
            >{{ s.kind }}</span>
          </td>
          <td class="px-4 py-2 truncate max-w-[28ch]">
            <button
              v-if="s.url"
              type="button"
              class="text-accent hover:text-accent-muted text-left truncate max-w-full"
              :title="s.url"
              :data-testid="`site-url-${s.name}`"
              @click="client.openExternalURL(s.url)"
            >
              {{ s.url }}
            </button>
            <span v-else class="text-ink-subtle">—</span>
          </td>
          <td class="px-4 py-2">
            <span
              class="text-[11px] uppercase tracking-[0.12em]"
              :class="statusClass(s.status)"
              :data-testid="`site-status-${s.name}`"
            >{{ s.status }}</span>
          </td>
          <td class="px-4 py-2 text-ink-muted">
            {{ relativeTime(s.deployedAt) }}
          </td>
          <td class="px-4 py-2 text-right space-x-3">
            <button
              v-if="s.kind === 'dynamic'"
              type="button"
              class="text-[11px] text-ink-muted hover:text-ink"
              :data-testid="`site-logs-${s.name}`"
              @click="openLogs(s.name)"
            >
              Logs
            </button>
            <button
              type="button"
              class="text-[11px] text-ink-muted hover:text-ink"
              :data-testid="`site-env-${s.name}`"
              @click="openEnvModal(s.name)"
            >
              Env vars
            </button>
            <button
              type="button"
              class="text-[11px] text-signal-danger hover:text-ink"
              :data-testid="`site-delete-${s.name}`"
              @click="promptDelete(s.name)"
            >
              Delete
            </button>
          </td>
        </tr>
      </tbody>
    </table>

    <!-- deploy affordance + progress -->
    <div class="mt-6 px-6 py-4 border-t border-border-muted font-ui text-[12px]">
      <div class="text-ink mb-2">Deploy from folder</div>
      <p class="text-ink-muted mb-3 max-w-prose">
        Pick a local folder that contains a
        <code class="font-mono">kameas-site.json</code>
        manifest. Static sites bundle all files; dynamic sites upload an entry-point.
      </p>

      <div class="flex items-center gap-2">
        <button
          type="button"
          class="px-3 py-1 text-[11px] bg-accent text-ink hover:bg-accent-muted disabled:opacity-50 rounded"
          :disabled="deployBusy"
          data-testid="sites-deploy-btn"
          @click="pickAndDeploy"
        >
          {{ deployBusy ? 'Deploying…' : 'Deploy from folder…' }}
        </button>
        <button
          v-if="deployDone || deployError"
          type="button"
          class="text-[11px] text-ink-muted hover:text-ink"
          data-testid="sites-deploy-reset"
          @click="resetDeploy"
        >
          Clear
        </button>
      </div>

      <!-- deploy error -->
      <div
        v-if="deployError"
        class="mt-2 text-[11px] text-signal-danger"
        role="alert"
        data-testid="sites-deploy-error"
      >
        {{ deployError }}
      </div>

      <!-- deploy progress stages -->
      <ul
        v-if="deployStages.length > 0"
        class="mt-3 space-y-1 font-mono text-[11px]"
        data-testid="sites-deploy-progress"
      >
        <li
          v-for="(ev, i) in deployStages"
          :key="i"
          class="flex items-baseline gap-2"
          :class="ev.stage === 'error' ? 'text-signal-danger' : ev.stage === 'done' ? 'text-signal-ok' : 'text-ink-muted'"
          :data-testid="`deploy-stage-${ev.stage}`"
        >
          <span class="w-[7rem] shrink-0 uppercase tracking-[0.1em] text-[10px]">{{ ev.stage }}</span>
          <span class="flex-1">{{ ev.message }}</span>
          <a
            v-if="ev.url"
            :href="ev.url"
            class="text-accent shrink-0"
            target="_blank"
            rel="noopener"
          >open</a>
        </li>
      </ul>
    </div>

    <!-- logs modal -->
    <div
      v-if="logsModalSite !== null"
      class="fixed inset-0 z-50 flex items-center justify-center"
      role="dialog"
      aria-modal="true"
      data-testid="logs-modal"
    >
      <div class="absolute inset-0 bg-modal-overlay" @click="closeLogs" />
      <div
        class="relative z-10 w-[640px] max-w-[90vw] rounded-md border border-border-muted bg-surface-0 shadow-lg p-5"
      >
        <div class="flex items-center justify-between mb-3">
          <h2 class="font-ui text-base font-semibold text-ink">
            Logs: {{ logsModalSite }}
          </h2>
          <button
            type="button"
            class="font-ui text-xs text-ink-dim hover:text-ink"
            data-testid="logs-modal-close"
            @click="closeLogs"
          >
            Close
          </button>
        </div>
        <div v-if="logsBusy" class="text-sm text-ink-muted font-ui">
          Fetching logs…
        </div>
        <div
          v-else-if="logsError"
          class="text-sm text-signal-danger font-ui"
          role="alert"
        >
          {{ logsError }}
        </div>
        <pre
          v-else
          class="overflow-auto max-h-[50vh] font-mono text-[11px] text-ink bg-surface-1 rounded p-3 whitespace-pre-wrap"
          data-testid="logs-content"
        >{{ logsContent || '(no log output)' }}</pre>
      </div>
    </div>

    <!-- env vars modal -->
    <div
      v-if="envModalSite !== null"
      class="fixed inset-0 z-50 flex items-center justify-center"
      role="dialog"
      aria-modal="true"
      data-testid="env-modal"
    >
      <div class="absolute inset-0 bg-modal-overlay" @click="closeEnvModal" />
      <div
        class="relative z-10 w-[520px] max-w-[90vw] rounded-md border border-border-muted bg-surface-0 shadow-lg p-5"
      >
        <div class="flex items-center justify-between mb-3">
          <h2 class="font-ui text-base font-semibold text-ink">
            Env vars: {{ envModalSite }}
          </h2>
          <button
            type="button"
            class="font-ui text-xs text-ink-dim hover:text-ink"
            data-testid="env-modal-close"
            @click="closeEnvModal"
          >
            Close
          </button>
        </div>
        <p class="text-[11px] text-ink-muted mb-3 max-w-prose">
          Values are write-only — once set, they are never shown here again.
          This is the only way secrets reach a site; they never ride in a
          deploy manifest.
        </p>
        <div v-if="envLoading" class="text-sm text-ink-muted font-ui">
          Loading env vars…
        </div>
        <div
          v-else-if="envListError"
          class="text-sm text-signal-danger font-ui"
          role="alert"
          data-testid="env-list-error"
        >
          {{ envListError }}
        </div>
        <div
          v-else-if="envEntries.length === 0"
          class="text-sm text-ink-muted font-ui"
          data-testid="env-empty"
        >
          This site's manifest declares no environment variables.
        </div>
        <ul v-else class="space-y-3" data-testid="env-entries">
          <li
            v-for="entry in envEntries"
            :key="entry.name"
            class="space-y-1"
            :data-testid="`env-entry-${entry.name}`"
          >
            <div class="flex items-baseline justify-between">
              <span class="font-mono text-xs text-ink">{{ entry.name }}</span>
              <span
                class="text-[10px] uppercase tracking-[0.1em]"
                :class="entry.setAt ? 'text-signal-ok' : 'text-ink-subtle'"
                :data-testid="`env-status-${entry.name}`"
              >{{ envSetDisplay(entry) }}</span>
            </div>
            <p v-if="entry.description" class="text-[11px] text-ink-muted">
              {{ entry.description }}
            </p>
            <div class="flex gap-2">
              <input
                v-model="envDrafts[entry.name]"
                type="password"
                autocomplete="off"
                class="flex-1 px-2 py-1 text-xs font-mono rounded border border-border-muted bg-surface-1 text-ink"
                :placeholder="entry.setAt ? 'Replace value…' : 'Set value…'"
                :data-testid="`env-input-${entry.name}`"
              />
              <button
                type="button"
                class="px-2 py-1 text-[11px] bg-accent text-ink hover:bg-accent-muted disabled:opacity-50 rounded"
                :disabled="!envDrafts[entry.name] || envSaving === entry.name"
                :data-testid="`env-save-${entry.name}`"
                @click="saveEnvVar(entry.name)"
              >
                {{ envSaving === entry.name ? 'Saving…' : 'Save' }}
              </button>
            </div>
          </li>
        </ul>
        <div
          v-if="envSaveError"
          class="mt-3 text-xs text-signal-danger font-ui"
          role="alert"
          data-testid="env-save-error"
        >
          {{ envSaveError }}
        </div>
      </div>
    </div>

    <!-- delete confirm modal -->
    <div
      v-if="deletingConfirmSite !== null"
      class="fixed inset-0 z-50 flex items-center justify-center"
      role="dialog"
      aria-modal="true"
      data-testid="delete-confirm-modal"
    >
      <div class="absolute inset-0 bg-modal-overlay" @click="cancelDelete" />
      <div
        class="relative z-10 w-[420px] max-w-[90vw] rounded-md border border-border-muted bg-surface-0 shadow-lg p-5"
      >
        <h2 class="font-ui text-base font-semibold text-ink">
          Delete "{{ deletingConfirmSite }}"?
        </h2>
        <p class="mt-2 font-ui text-xs text-ink-muted">
          This will remove the site and all its deployments from fleet. The
          action cannot be undone.
        </p>
        <div
          v-if="deleteError"
          class="mt-2 text-xs text-signal-danger font-ui"
          role="alert"
          data-testid="delete-error"
        >
          {{ deleteError }}
        </div>
        <div class="mt-4 flex justify-end gap-2">
          <button
            type="button"
            class="font-ui text-xs px-3 py-1.5 text-ink-dim hover:text-ink"
            data-testid="delete-cancel"
            @click="cancelDelete"
          >
            Cancel
          </button>
          <button
            type="button"
            class="font-ui text-xs px-3 py-1.5 rounded-sm border border-signal-danger text-signal-danger hover:bg-surface-2 disabled:opacity-50"
            :disabled="deletingBusy"
            data-testid="delete-confirm"
            @click="commitDelete"
          >
            {{ deletingBusy ? 'Deleting…' : 'Delete' }}
          </button>
        </div>
      </div>
    </div>
  </SettingsShell>
</template>
