<script setup lang="ts">
/**
 * RecommendationsPanel — Settings → Recommendations.
 *
 * The local ML engine's install / status / uninstall / update surface
 * (laya-advisors-01LAYA001 WP13, spec §2c). One honest install action with
 * size + location disclosed BEFORE the download; one status line that names
 * every state the engine can actually be in (never "not installed" for an
 * installed-but-broken engine, never "unhealthy" for an engine that has
 * merely stopped itself when idle); update is prompted, never silent.
 *
 * All truth comes from core/mlsidecar.Manager through client.sidecar — this
 * component derives nothing it cannot read from the StatusView.
 */
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { isServedMode } from '@/lib/useServedMode';
import type { SidecarStatusView } from '@/lib/harnessClient';

const client = useHarnessClient();
const served = isServedMode();

const view = ref<SidecarStatusView | null>(null);
const busy = ref(false);
const errorMsg = ref('');
const confirmingUninstall = ref(false);

let pollTimer: ReturnType<typeof setInterval> | null = null;

async function refresh(): Promise<void> {
  try {
    view.value = await client.sidecar.status();
  } catch (err) {
    errorMsg.value = String(err);
  }
}

function startPolling(): void {
  if (pollTimer !== null) return;
  pollTimer = setInterval(() => void refresh(), 1500);
}
function stopPolling(): void {
  if (pollTimer !== null) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
}

onMounted(async () => {
  if (served) return;
  await refresh();
});
onUnmounted(stopPolling);

/** Run one of the blocking actions; poll status meanwhile so the install phase is visible. */
async function run(action: () => Promise<SidecarStatusView>): Promise<void> {
  busy.value = true;
  errorMsg.value = '';
  confirmingUninstall.value = false;
  startPolling();
  try {
    view.value = await action();
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : String(err);
    await refresh();
  } finally {
    stopPolling();
    busy.value = false;
  }
}

const enable = () => run(() => client.sidecar.enable());
const update = () => run(() => client.sidecar.update());
const repair = () => run(() => client.sidecar.repair());
const uninstall = () => run(() => client.sidecar.uninstall());

const state = computed(() => view.value?.state ?? 'not_installed');
const installing = computed(() => busy.value || state.value === 'installing');

/** One line per state — every mlsidecar.State is named distinctly. */
const statusLine = computed<{ headline: string; explain: string; tone: 'ok' | 'warn' | 'danger' | 'neutral' }>(() => {
  const v = view.value;
  const reason = v?.reason ?? '';
  switch (state.value) {
    case 'healthy':
      return {
        headline: v?.engineVersion ? `Running — engine ${v.engineVersion}` : 'Running',
        explain: 'Recommendations are served by the local ML engine on this device.',
        tone: 'ok',
      };
    case 'installed_idle':
      return {
        headline: 'Installed — starts when needed',
        explain:
          'The engine stops itself when idle and starts again the next time a recommendation needs it. Until then recommendations use the built-in heuristics.',
        tone: 'neutral',
      };
    case 'installing':
      return {
        headline: 'Installing…',
        explain: v?.detail || 'Working…',
        tone: 'neutral',
      };
    case 'installed_unhealthy':
      return {
        headline: 'Installed, but not working',
        explain: unhealthyExplain(reason, v?.detail ?? ''),
        tone: 'danger',
      };
    case 'unverified':
      return {
        headline: 'The installed engine could not be verified',
        explain:
          'The engine on disk does not match its install record, so it is not used and will not be started. If another process is answering on the engine port, it was left running untouched. Re-download to restore a verified copy.',
        tone: 'warn',
      };
    case 'contract_unsupported':
      return {
        headline: 'The running engine is not compatible with this version of Kenaz Harness',
        explain:
          'Recommendations use the built-in heuristics until Kenaz Harness and the shared engine are on compatible versions — update Kenaz Harness, or update the engine if it is the older one. The engine is left running for other apps that share it.',
        tone: 'warn',
      };
    case 'legacy_unverified':
      return {
        headline: 'An older ML engine is running',
        explain:
          'Update Kenaz to share the ML engine. The older engine was not installed by this app, so it is not used and is left running untouched.',
        tone: 'warn',
      };
    case 'not_installed':
    default:
      return {
        headline: 'Not installed',
        explain: 'Recommendations use the built-in heuristics. Enable local recommendations to add the ML engine.',
        tone: 'neutral',
      };
  }
});

function unhealthyExplain(reason: string, detail: string): string {
  switch (reason) {
    case 'crash':
      return `The engine failed to start. ${detail}`.trim();
    case 'port_conflict':
      return "Another program is using the engine's port. This app will not stop it or use it.";
    case 'digest_mismatch':
      return "The engine's files do not match their signature, so they will not be run. Re-download a fresh copy.";
    case 'update_pending':
      return `An update did not complete; the previous version is still installed. ${detail}`.trim();
    default:
      return detail || 'The engine is installed but is not responding.';
  }
}

const toneClass = computed(() => {
  switch (statusLine.value.tone) {
    case 'ok':
      return 'text-signal-ok';
    case 'danger':
      return 'text-signal-danger';
    case 'warn':
      return 'text-signal-warn';
    default:
      return 'text-ink';
  }
});

/** Show the install button only when nothing usable is installed and it can work. */
const showEnable = computed(() => {
  const v = view.value;
  if (!v || !v.available) return false;
  if (!v.installed) return true;
  // Installed but the files are untrusted (tampered tree, or a record
  // that no longer matches the disk): offer a fresh download.
  if (v.state === 'unverified') return true;
  return v.state === 'installed_unhealthy' && v.reason === 'digest_mismatch';
});
const showRepair = computed(
  () => !!view.value?.installed && state.value === 'installed_unhealthy' && view.value?.reason !== 'digest_mismatch',
);
const showUninstall = computed(() => !!view.value?.installed);
</script>

<template>
  <section class="space-y-6 p-4" data-testid="sidecar-panel">
    <div>
      <h2 class="text-sm font-semibold text-ink mb-1">Local recommendations</h2>
      <p class="text-xs text-ink-muted">
        Suggestions for when to branch a conversation, compact its context, or switch to a stronger model.
        Without the ML engine these come from built-in heuristics. With it, they come from the Kameas ML
        engine, which runs entirely on this device — nothing is sent anywhere.
      </p>
    </div>

    <!-- Served mode: the engine is a desktop install. -->
    <p v-if="served" class="text-xs text-ink-muted" data-testid="sidecar-served-note">
      The local ML engine is installed and runs on the desktop app. It is not available in a browser session.
    </p>

    <template v-else>
      <!-- Status line: one per state, always rendered once loaded. -->
      <div v-if="view" class="rounded border border-border-muted p-3 space-y-1" data-testid="sidecar-status-card">
        <p class="text-xs font-medium uppercase tracking-wide text-ink-muted">Status</p>
        <p class="text-sm font-medium" :class="toneClass" :data-state="state" data-testid="sidecar-status">
          {{ statusLine.headline }}
        </p>
        <p class="text-xs text-ink-muted" data-testid="sidecar-status-explain">{{ statusLine.explain }}</p>
        <p v-if="view.installed" class="text-xs text-ink-muted" data-testid="sidecar-installed-version">
          Installed engine: {{ view.installedVersion }}
        </p>
        <!-- Paused label lanes (design A4): honest, per-kind, never only a log line. -->
        <div v-if="view.labelLanes?.length" class="space-y-0.5" data-testid="sidecar-label-lanes">
          <p
            v-for="lane in view.labelLanes"
            :key="lane.kind"
            class="text-xs text-amber-700 dark:text-amber-400"
            :data-lane-kind="lane.kind"
          >
            Label sync for “{{ lane.kind }}” is paused ({{ lane.code || lane.reason }}); nothing is lost — it retries
            automatically.
          </p>
        </div>
      </div>
      <p v-else class="text-xs text-ink-muted" data-testid="sidecar-loading">Checking the ML engine…</p>

      <!-- Update: prompted, never silent. -->
      <div
        v-if="view?.updateAvailable"
        class="rounded border border-border-muted p-3 space-y-2"
        data-testid="sidecar-update-card"
      >
        <p class="text-xs text-ink">
          A newer ML engine ({{ view.release.version }}) is available. Updating changes how recommendations
          behave, so it only happens when you choose to.
        </p>
        <button
          type="button"
          class="text-xs px-3 py-1.5 rounded border border-border-muted text-ink hover:bg-surface-2"
          data-testid="sidecar-update"
          :disabled="installing"
          @click="update"
        >
          Update to {{ view.release.version }}
        </button>
      </div>

      <!-- Why the install action is not offered (unsupported platform / nothing published yet). -->
      <p
        v-if="view && !view.available && !view.installed"
        class="text-xs text-ink-muted"
        data-testid="sidecar-unavailable"
      >
        {{ view.unavailableReason }}
      </p>

      <!-- The one install action, with size + location disclosed before the click. -->
      <div v-if="showEnable && view" class="space-y-2" data-testid="sidecar-enable-card">
        <button
          type="button"
          class="text-sm px-3 py-2 rounded border border-border-muted text-ink hover:bg-surface-2 text-left"
          data-testid="sidecar-enable"
          :disabled="installing"
          @click="enable"
        >
          Enable local recommendations — downloads the Kameas ML engine
          ({{ view.release.sizeMB > 0 ? `${view.release.sizeMB} MB` : 'size unknown' }}, runs on this device)
        </button>
        <p class="text-xs text-ink-muted" data-testid="sidecar-disclosure">
          Installs to <code class="break-all">{{ view.installLocation }}</code>. The download is checked against
          Kameas's signature before it is ever run. You can remove it at any time from this page.
        </p>
      </div>

      <div v-if="showRepair || showUninstall" class="flex flex-wrap items-center gap-2">
        <button
          v-if="showRepair"
          type="button"
          class="text-xs px-3 py-1.5 rounded border border-border-muted text-ink hover:bg-surface-2"
          data-testid="sidecar-repair"
          :disabled="installing"
          @click="repair"
        >
          Try again
        </button>
        <button
          v-if="showUninstall && !confirmingUninstall"
          type="button"
          class="text-xs px-3 py-1.5 rounded border border-border-muted text-ink-muted hover:text-ink"
          data-testid="sidecar-uninstall"
          :disabled="installing"
          @click="confirmingUninstall = true"
        >
          Uninstall
        </button>
      </div>

      <!-- Inline two-step confirm (no window.confirm — the viewer blocks dialogs). -->
      <div
        v-if="confirmingUninstall && view"
        class="rounded border border-border-muted p-3 space-y-2"
        data-testid="sidecar-uninstall-confirm"
      >
        <p class="text-xs text-ink">
          Remove the ML engine? This stops it and deletes the engine, its downloaded models and its
          configuration from <code class="break-all">{{ view.installLocation }}</code>. Recommendations go back
          to the built-in heuristics. The engine is shared with other Kenaz apps: if one is using it right
          now, it stays installed until that app quits.
        </p>
        <div class="flex gap-2">
          <button
            type="button"
            class="text-xs px-3 py-1.5 rounded border border-border-muted text-signal-danger hover:bg-surface-2"
            data-testid="sidecar-uninstall-confirm-yes"
            :disabled="installing"
            @click="uninstall"
          >
            Uninstall
          </button>
          <button
            type="button"
            class="text-xs px-3 py-1.5 rounded border border-border-muted text-ink-muted hover:text-ink"
            data-testid="sidecar-uninstall-confirm-no"
            @click="confirmingUninstall = false"
          >
            Cancel
          </button>
        </div>
      </div>

      <p v-if="errorMsg" class="text-xs text-signal-danger" data-testid="sidecar-error">{{ errorMsg }}</p>
    </template>
  </section>
</template>
