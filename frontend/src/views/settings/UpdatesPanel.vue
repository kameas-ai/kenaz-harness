<script setup lang="ts">
/**
 * UpdatesPanel — Settings tab for the auto-update mission (v0.4.0 WP05).
 *
 * Surfaces three blocks per the mission brief:
 *   1. Status block — current version, auto-check indicator, last
 *      checked stamp, "Check now" trigger, plus an inline "v0.3.4
 *      available" mini-panel that mirrors UpdateMenu's primary action.
 *   2. Preferences — auto-check toggle, channel radio (Stable /
 *      Prerelease), and check-interval select (1h / 6h / 24h).
 *   3. Skipped versions — collapsible list of "Skip this version" entries
 *      with per-row "Unskip" links.
 *
 * Backend wiring lives in three surfaces:
 *   - Settings.AutoCheckUpdates / UpdateChannel / UpdateCheckInterval
 *     reach through SettingsClient.set with the matching field on the
 *     full Settings record (debounced 250ms). This panel writes the
 *     fields directly because the SettingsClient surface stays narrow
 *     until WP03 lands a dedicated setter pair.
 *   - The skip-list reads / writes go through `lib/updateClient.ts`
 *     (the WP04 shim, extended in WP05). Mocked via the fake-bindings
 *     pattern until WP03 wires the real updater.
 *
 * Style: matches BashPermissionsPanel / CredentialPermissionsPanel —
 * an outer <section> with a SETTINGS-style ALL-CAPS header, the same
 * font tokens, and the `surface-1` / `border-border` palette.
 */

import { computed, onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import { createUpdateClient, type UpdateStatus } from '@/lib/updateClient';
import { useEventStream } from '@/lib/useEventStream';
import {
  applyDownloadProgressEvent,
  applyDownloadCompleteEvent,
  applyDownloadFailedEvent,
  type UpdateDownloadProgressPayload,
  type UpdateStagedPayload,
  type UpdateDownloadFailedPayload,
} from '@/lib/updateDownloadEvents';
import type { Settings } from '@/lib/types';

const client = useHarnessClient();

/**
 * The update RPC surface lives outside the typed HarnessClient (the
 * updater is a process-level concern, not a view-scoped accessor).
 * Instantiate once at mount; tests can override via the
 * `updateClientOverride` prop so they don't have to monkey-patch the
 * factory.
 */
const props = defineProps<{
  /** Test seam — when set, used in place of createUpdateClient(). */
  updateClientOverride?: ReturnType<typeof createUpdateClient>;
}>();

const updater = props.updateClientOverride ?? createUpdateClient();

/* ── State ─────────────────────────────────────────────────────────── */

const status = ref<UpdateStatus | null>(null);
const settings = ref<Settings | null>(null);
const skippedVersions = ref<string[]>([]);
const skippedOpen = ref(false);
const checking = ref(false);
const installing = ref(false);
const errorMessage = ref<string | null>(null);

/* ── Loaders ───────────────────────────────────────────────────────── */

async function loadStatus() {
  try {
    status.value = await updater.status();
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err);
  }
}

async function loadSettings() {
  try {
    settings.value = await client.settings.get();
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err);
  }
}

async function loadSkipped() {
  try {
    skippedVersions.value = await updater.listSkippedVersions();
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err);
  }
}

async function refreshAll() {
  errorMessage.value = null;
  await Promise.all([loadStatus(), loadSettings(), loadSkipped()]);
}

onMounted(() => {
  void refreshAll();
});

/* ── Download-progress accelerator (WP03, DC-1/DC-8) ──────────────────
 * installLatest (updateClient.ts) is the ONLY waiter — it decides
 * completion by polling Update_Status, never by these events. This
 * subscription exists purely so the panel repaints faster than the next
 * poll tick; if every one of these three useEventStream calls were
 * deleted, installLatest would still resolve/reject correctly (DC-1) —
 * the panel would just look a little steppier while downloading. */
useEventStream<UpdateDownloadProgressPayload>(
  'update:download-progress',
  (payload) => {
    status.value = applyDownloadProgressEvent(status.value, payload);
  },
);
useEventStream<UpdateStagedPayload>('update:download-complete', (payload) => {
  status.value = applyDownloadCompleteEvent(status.value, payload);
});
useEventStream<UpdateDownloadFailedPayload>(
  'update:download-failed',
  (payload) => {
    status.value = applyDownloadFailedEvent(status.value, payload);
  },
);

/* ── Status block ──────────────────────────────────────────────────── */

/** Friendly relative-time renderer for "Last checked" — matches the
 *  mission brief's "14 minutes ago" / "Never" copy. Avoids a date-fns
 *  dep so the panel stays light. Accepts a Unix-seconds number (the
 *  backend's UpdateStatus.lastCheckedAt shape) or null/undefined. */
function relativeTime(unixSec: number | null | undefined): string {
  if (unixSec == null || unixSec <= 0) return 'Never';
  const tMs = unixSec * 1000;
  const diffSec = Math.max(0, (Date.now() - tMs) / 1000);
  if (diffSec < 60) return 'just now';
  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 60)
    return `${diffMin} minute${diffMin === 1 ? '' : 's'} ago`;
  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return `${diffHr} hour${diffHr === 1 ? '' : 's'} ago`;
  const diffDay = Math.floor(diffHr / 24);
  return `${diffDay} day${diffDay === 1 ? '' : 's'} ago`;
}

const lastCheckedLabel = computed(() =>
  status.value ? relativeTime(status.value.lastCheckedAt) : '—',
);

// auto-check is owned by the Settings record (autoCheckUpdatesDisabled
// inverted), not by the UpdateStatus payload. Read it from settings so
// the indicator matches the toggle below.
const autoCheckLabel = computed(() => {
  if (!settings.value) return '—';
  return settings.value.autoCheckUpdatesDisabled ? 'off' : 'on';
});

async function onCheckNow() {
  if (checking.value) return;
  checking.value = true;
  errorMessage.value = null;
  try {
    await updater.startCheck();
    // Re-pull status after the backend updates lastCheckedAt / available.
    await loadStatus();
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err);
  } finally {
    checking.value = false;
  }
}

async function onInstallAvailable() {
  const v = status.value?.availableVersion;
  if (!v || installing.value) return;
  installing.value = true;
  errorMessage.value = null;
  try {
    // The poll loop inside installLatest hands us every Update_Status
    // snapshot it reads (DC-8: still ONE waiter — we do not poll here,
    // we just render what that waiter already read). This is what makes
    // the WP03 broker subscriptions a true accelerator: with every
    // subscription detached the bar still advances, at 2/s instead of
    // ~10/s, and still lands on its terminal 'failed' render.
    await updater.installLatest(v, (s) => {
      status.value = s;
    });
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err);
  } finally {
    installing.value = false;
  }
}

/* ── Download progress surface (WP04 — the A8 user sentence) ─────────
 * Pure function of `status` — the last Update_Status snapshot (from the
 * initial poll, a re-check, or the WP03 accelerator event merge). No
 * second poll loop here (DC-8): installLatest is the only waiter. This
 * is why AC-5 (reload mid-download rehydrates from Status alone, no
 * events) falls out for free — these are the same computed reads the
 * first onMounted status load already populates. */

/** 0-100 integer percent (DC-2 — never a 0..1 fraction). */
const downloadPercent = computed(() => status.value?.downloadProgress ?? 0);
const isDownloading = computed(
  () => status.value?.downloadState === 'downloading',
);
/** No Content-Length from the server (impl.go percent() returns 0 for
 *  total<=0) renders an indeterminate bar rather than a fake "0%". */
const isIndeterminateDownload = computed(
  () => isDownloading.value && downloadPercent.value <= 0,
);
const isStaged = computed(() => status.value?.downloadState === 'staged');
const isFailed = computed(() => status.value?.downloadState === 'failed');
/** FR-004: readable from Status alone, not only from a thrown
 *  installLatest rejection — so a fresh mount/reload after a failure
 *  (with no click in this session) still shows the reason. */
const downloadFailureReason = computed(() =>
  isFailed.value
    ? (status.value?.downloadError ?? 'Update download failed.')
    : null,
);
/** The top alert banner shows whichever failure reason is known: an
 *  exception thrown by this session's own action, or (FR-004/FR-005) a
 *  failure reason read back from Status on mount/reload. */
const bannerMessage = computed(
  () => errorMessage.value ?? downloadFailureReason.value,
);

/* ── Preferences (auto-check toggle, channel, interval) ────────────── */

const autoCheckEnabled = computed({
  get: () => !(settings.value?.autoCheckUpdatesDisabled ?? false),
  set: (next: boolean) => {
    if (!settings.value) return;
    settings.value = { ...settings.value, autoCheckUpdatesDisabled: !next };
  },
});

async function onToggleAutoCheck() {
  if (!settings.value) return;
  const next = !autoCheckEnabled.value;
  const updated: Settings = {
    ...settings.value,
    autoCheckUpdatesDisabled: !next,
  };
  settings.value = updated;
  errorMessage.value = null;
  try {
    await client.settings.set(updated);
    // Status payload's autoCheckEnabled mirrors this flag — re-pull so
    // the status block reflects the change without waiting for the next
    // backend tick.
    await loadStatus();
  } catch (err) {
    // Revert visually if the write failed.
    settings.value = {
      ...settings.value,
      autoCheckUpdatesDisabled: !updated.autoCheckUpdatesDisabled,
    };
    errorMessage.value = err instanceof Error ? err.message : String(err);
  }
}

const CHANNEL_OPTIONS: ReadonlyArray<{
  value: 'stable' | 'prerelease';
  label: string;
}> = [
  { value: 'stable', label: 'Stable' },
  { value: 'prerelease', label: 'Prerelease' },
];

const currentChannel = computed<'stable' | 'prerelease'>(() => {
  const v = settings.value?.updateChannel;
  return v === 'prerelease' ? 'prerelease' : 'stable';
});

async function setChannel(channel: 'stable' | 'prerelease') {
  if (!settings.value || currentChannel.value === channel) return;
  const updated: Settings = { ...settings.value, updateChannel: channel };
  settings.value = updated;
  errorMessage.value = null;
  try {
    await client.settings.set(updated);
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err);
  }
}

const INTERVAL_OPTIONS: ReadonlyArray<{ value: number; label: string }> = [
  { value: 3600, label: '1 hour' },
  { value: 21600, label: '6 hours' },
  { value: 86400, label: '24 hours' },
];

const currentIntervalSec = computed<number>(() => {
  const v = settings.value?.updateCheckIntervalSec ?? 0;
  return v > 0 ? v : 21600;
});

async function onIntervalChange(evt: Event) {
  if (!settings.value) return;
  const v = Number.parseInt((evt.target as HTMLSelectElement).value, 10);
  if (Number.isNaN(v)) return;
  const updated: Settings = { ...settings.value, updateCheckIntervalSec: v };
  settings.value = updated;
  errorMessage.value = null;
  try {
    await client.settings.set(updated);
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err);
  }
}

/* ── Skipped versions ──────────────────────────────────────────────── */

async function onUnskip(version: string) {
  errorMessage.value = null;
  try {
    await updater.unskipVersion(version);
    await loadSkipped();
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err);
  }
}
</script>

<template>
  <section data-testid="updates-panel">
    <h2
      class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
    >
      Updates
    </h2>
    <p class="mt-1 font-ui text-[11px] text-ink-dim">
      Auto-update preferences for new Kenaz releases. Checks query the
      GitHub release feed; downloads are signed and verified before
      install.
    </p>

    <div
      v-if="bannerMessage"
      class="mt-2 rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
      role="alert"
      data-testid="updates-error"
    >
      {{ bannerMessage }}
    </div>

    <!-- ── Status block ────────────────────────────────────────────── -->

    <div
      class="mt-4 rounded-sm border border-border bg-surface-1 p-3"
      data-testid="updates-status-block"
    >
      <div class="flex items-start justify-between gap-3">
        <div class="grid gap-1 font-ui text-[12px]">
          <p class="text-ink" data-testid="updates-current-version">
            You're running Kenaz
            <span class="font-mono text-ink"
              >{{ status?.currentVersion ?? '—' }}</span
            >
          </p>
          <p class="text-ink-muted">
            Auto-update is
            <span
              class="font-mono"
              :class="
                autoCheckLabel === 'on'
                  ? 'text-signal-success'
                  : 'text-ink-muted'
              "
              data-testid="updates-auto-check-label"
              >{{ autoCheckLabel }}</span
            >
          </p>
          <p class="text-ink-muted">
            Last checked:
            <span class="font-mono text-ink" data-testid="updates-last-checked">{{
              lastCheckedLabel
            }}</span>
          </p>
        </div>
        <button
          type="button"
          class="rounded-sm border border-accent-hairline bg-surface-1 px-3 py-1.5 font-ui text-[11px] uppercase tracking-[0.14em] text-accent hover:bg-accent-glow disabled:opacity-50 transition-colors"
          :disabled="checking"
          data-testid="updates-check-now"
          @click="onCheckNow"
        >
          {{ checking ? 'Checking…' : 'Check for updates now' }}
        </button>
      </div>

      <!-- Inline "vX.Y.Z available" mini-panel — mirrors UpdateMenu. -->
      <div
        v-if="status?.available && status.availableVersion"
        class="mt-3 flex items-center justify-between gap-3 rounded-sm border border-accent-hairline bg-surface-2 px-3 py-2"
        data-testid="updates-available-panel"
      >
        <div class="font-ui text-[12px] text-ink">
          <span class="font-mono">{{ status.availableVersion }}</span>
          available
          <a
            v-if="status.releaseUrl"
            :href="status.releaseUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="ml-2 text-accent hover:underline"
            data-testid="updates-release-notes-link"
          >Release notes</a>
        </div>
        <button
          type="button"
          class="rounded-sm border border-accent bg-accent px-3 py-1 font-ui text-[11px] uppercase tracking-[0.14em] text-surface-0 hover:bg-accent-strong disabled:opacity-50 transition-colors"
          :disabled="installing"
          data-testid="updates-install"
          @click="onInstallAvailable"
        >
          {{ installing ? 'Installing…' : 'Install' }}
        </button>
      </div>

      <!-- Download progress (WP04). Renders purely from `status` — no
           second poll loop (DC-8). Independent of the available-panel
           `v-if` above so a rehydrated 'downloading'/'staged' status
           always renders even if `available` itself is stale. -->
      <div
        v-if="isDownloading || isStaged"
        class="mt-3"
        data-testid="updates-progress-region"
      >
        <div
          class="h-1.5 w-full overflow-hidden rounded-full bg-surface-2"
        >
          <div
            v-if="isDownloading && !isIndeterminateDownload"
            class="h-full rounded-full bg-accent transition-[width]"
            role="progressbar"
            data-testid="updates-progress"
            :aria-valuenow="downloadPercent"
            aria-valuemin="0"
            aria-valuemax="100"
            :style="{ width: downloadPercent + '%' }"
          ></div>
          <div
            v-else-if="isDownloading"
            class="h-full w-1/3 animate-pulse rounded-full bg-accent"
            role="progressbar"
            data-testid="updates-progress"
            data-indeterminate="true"
            aria-valuemin="0"
            aria-valuemax="100"
          ></div>
          <div
            v-else
            class="h-full w-full rounded-full bg-accent"
            role="progressbar"
            data-testid="updates-progress"
            aria-valuenow="100"
            aria-valuemin="0"
            aria-valuemax="100"
          ></div>
        </div>
        <p
          class="mt-1 font-ui text-[11px] text-ink-muted"
          data-testid="updates-progress-label"
        >
          <template v-if="isDownloading && !isIndeterminateDownload"
            >Downloading… {{ downloadPercent }}%</template
          >
          <template v-else-if="isDownloading">Downloading…</template>
          <!-- 'installing' is true iff THIS session's installLatest
               waiter is running. A reload mid-download destroys that
               waiter while the Go-side download keeps going, so the
               panel can land on 'staged' with nobody about to apply it:
               claiming "installing…" there would name an activity that
               is not happening. Say what is actually true and point at
               the control that finishes the job. -->
          <template v-else-if="installing">Staged — installing…</template>
          <template v-else
            >Update downloaded — click Install to finish, or use the Help
            menu's “Install &amp; Restart”.</template
          >
        </p>
      </div>
    </div>

    <!-- ── Preferences ─────────────────────────────────────────────── -->

    <div class="mt-6 grid gap-4" data-testid="updates-preferences">
      <div>
        <label class="flex items-center gap-3 font-ui text-[12px] text-ink">
          <input
            type="checkbox"
            class="accent-accent"
            :checked="autoCheckEnabled"
            data-testid="updates-auto-check-toggle"
            @change="onToggleAutoCheck"
          />
          Automatically check for updates
        </label>
        <p class="mt-1 font-ui text-[11px] text-ink-muted">
          When on, the harness polls the release feed at the configured
          interval and notifies you of available updates.
        </p>
      </div>

      <div>
        <p
          class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle"
        >
          Update channel
        </p>
        <div
          class="mt-2 inline-flex rounded-sm border border-border"
          role="radiogroup"
          aria-label="Update channel"
        >
          <button
            v-for="opt in CHANNEL_OPTIONS"
            :key="opt.value"
            type="button"
            role="radio"
            :aria-checked="currentChannel === opt.value"
            class="px-3 py-1.5 font-ui text-[12px] border-r border-border last:border-r-0 transition-colors"
            :class="
              currentChannel === opt.value
                ? 'bg-surface-3 text-ink'
                : 'bg-surface-1 text-ink-muted hover:text-ink'
            "
            :data-testid="`updates-channel-${opt.value}`"
            @click="setChannel(opt.value)"
          >
            {{ opt.label }}
          </button>
        </div>
        <p class="mt-1 font-ui text-[11px] text-ink-muted">
          Prerelease builds get features sooner but may have rough edges.
        </p>
      </div>

      <div>
        <label
          for="updates-interval-select"
          class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle"
        >
          Check interval
        </label>
        <div class="mt-2">
          <select
            id="updates-interval-select"
            class="rounded-sm border border-border bg-surface-1 px-2 py-1 font-ui text-[12px] text-ink"
            :value="currentIntervalSec"
            data-testid="updates-interval-select"
            @change="onIntervalChange"
          >
            <option
              v-for="opt in INTERVAL_OPTIONS"
              :key="opt.value"
              :value="opt.value"
            >
              {{ opt.label }}
            </option>
          </select>
        </div>
      </div>
    </div>

    <!-- ── Skipped versions ────────────────────────────────────────── -->

    <div class="mt-6" data-testid="updates-skipped-section">
      <button
        type="button"
        class="flex items-center gap-2 font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle hover:text-ink"
        :aria-expanded="skippedOpen"
        data-testid="updates-skipped-toggle"
        @click="skippedOpen = !skippedOpen"
      >
        <span>{{ skippedOpen ? '▾' : '▸' }}</span>
        <span
          >Skipped versions
          <span class="ml-1 text-ink-dim"
            >({{ skippedVersions.length }})</span
          ></span
        >
      </button>
      <div v-if="skippedOpen" class="mt-2">
        <ul
          v-if="skippedVersions.length > 0"
          class="rounded-sm border border-border bg-surface-1 divide-y divide-border-muted"
          data-testid="updates-skipped-list"
        >
          <li
            v-for="v in skippedVersions"
            :key="v"
            class="flex items-center justify-between px-3 py-2"
          >
            <span class="font-mono text-[12px] text-ink">{{ v }}</span>
            <button
              type="button"
              class="font-ui text-[11px] text-accent hover:underline"
              :data-testid="`updates-unskip-${v}`"
              @click="onUnskip(v)"
            >
              Unskip
            </button>
          </li>
        </ul>
        <p
          v-else
          class="font-ui text-[12px] text-ink-muted"
          data-testid="updates-skipped-empty"
        >
          No skipped versions
        </p>
      </div>
    </div>
  </section>
</template>
