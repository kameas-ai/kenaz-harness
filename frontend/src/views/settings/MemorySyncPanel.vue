<script setup lang="ts">
/**
 * MemorySyncPanel — Settings → Sync → "Learned memory across devices"
 * (memory-sync-01MEMSY01 WP08, Fleet contract H9).
 *
 * Opt-in, user-private sync of learned memory (global + long-term scopes)
 * between the user's own devices. The whole section is hidden unless the
 * memory_sync capability is present; it is off until the user accepts the
 * disclosure. Disabling keeps Fleet's copy unless the user also asks to
 * delete it, which needs the literal confirmation "forget-all".
 */
import { computed, onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { MemorySyncStatus } from '@/lib/types';

const client = useHarnessClient();

const status = ref<MemorySyncStatus | null>(null);
const busy = ref(false);
const errorMsg = ref('');
const consentChecked = ref(false);
const scopeLongTerm = ref(true);
const scopeGlobal = ref(true);
const confirmingDisable = ref(false);
const deleteFromFleet = ref(false);
const confirmText = ref('');

async function refresh() {
  try {
    status.value = await client.fleet.memorySyncStatus();
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : String(err);
  }
}

onMounted(refresh);

const visible = computed(() => !!status.value?.wired && !!status.value?.entitled);
const chosenScopes = computed(() => {
  const out: string[] = [];
  if (scopeLongTerm.value) out.push('long_term');
  if (scopeGlobal.value) out.push('global');
  return out;
});
const canEnable = computed(
  () => consentChecked.value && chosenScopes.value.length > 0 && !busy.value,
);
const canConfirmDisable = computed(
  () => !busy.value && (!deleteFromFleet.value || confirmText.value === 'forget-all'),
);

function scopeLabel(s: string): string {
  return s === 'long_term' ? 'Long-term' : s === 'global' ? 'Global' : s;
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MiB`;
}

const laneLine = computed<string>(() => {
  const l = status.value?.lane;
  if (!l) return '';
  switch (l.status) {
    case 'ok':
      return 'Syncing normally.';
    case 'degraded':
      if (l.reason === 'clock_in_future')
        return "Paused — this computer's clock is ahead of Fleet. Fix the system clock to resume.";
      if (l.reason === 'rate_limited') return 'Fleet asked us to slow down; retrying shortly.';
      return `Not syncing right now (${l.reason ?? 'error'}); retrying automatically.`;
    case 'off':
      if (l.reason === 'signed_out') return 'Sign in to Fleet to sync.';
      if (l.reason === 'disabled_on_fleet') return 'Turned off from another device.';
      return '';
    default:
      return 'Waiting for the first sync.';
  }
});

async function enable() {
  if (!canEnable.value || !status.value) return;
  busy.value = true;
  errorMsg.value = '';
  try {
    status.value = await client.fleet.memorySyncEnable(
      chosenScopes.value,
      status.value.currentConsentVersion,
    );
    consentChecked.value = false;
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : String(err);
  } finally {
    busy.value = false;
  }
}

function startDisable() {
  confirmingDisable.value = true;
  deleteFromFleet.value = false;
  confirmText.value = '';
}

async function confirmDisable() {
  if (!canConfirmDisable.value) return;
  busy.value = true;
  errorMsg.value = '';
  try {
    status.value = await client.fleet.memorySyncDisable(
      deleteFromFleet.value,
      deleteFromFleet.value ? confirmText.value : '',
    );
    confirmingDisable.value = false;
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : String(err);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <section v-if="visible && status" class="space-y-3" data-testid="memory-sync-panel">
    <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
      Learned memory across devices
    </h2>

    <!-- Off: disclosure + scope choice + consent. -->
    <div v-if="!status.enabled" class="space-y-3" data-testid="memory-sync-off">
      <div class="text-[12px] text-ink-muted space-y-1.5" data-testid="memory-sync-disclosure">
        <p>
          When on, the memories this harness has learned in the scopes you pick are copied to
          Fleet and to your other signed-in devices, so they follow you.
        </p>
        <ul class="list-disc pl-5 space-y-0.5">
          <li>Private to you: no admin, teammate or Kenaz staff view exists.</li>
          <li>Session memory never leaves this device. Project memory does not sync yet.</li>
          <li>
            Memory text, titles, pins and file paths are sent; embeddings are not (each device
            builds its own). Anything that looks like a credential is refused and stays local.
          </li>
          <li>
            Turning this off keeps Fleet's copy; you can also delete everything from Fleet at any
            time.
          </li>
        </ul>
      </div>
      <fieldset class="flex gap-4 text-[12px]" data-testid="memory-sync-scopes">
        <legend class="sr-only">Scopes to sync</legend>
        <label class="flex items-center gap-1.5">
          <input v-model="scopeLongTerm" type="checkbox" data-testid="memory-sync-scope-long_term" />
          Long-term
        </label>
        <label class="flex items-center gap-1.5">
          <input v-model="scopeGlobal" type="checkbox" data-testid="memory-sync-scope-global" />
          Global
        </label>
      </fieldset>
      <label class="flex items-start gap-2 text-[12px]">
        <input v-model="consentChecked" type="checkbox" data-testid="memory-sync-consent" />
        <span>I understand what syncs and want to turn this on.</span>
      </label>
      <button
        type="button"
        class="px-3 py-1.5 rounded-sm border border-border-muted text-[11px] uppercase tracking-[0.18em] hover:bg-surface-2 disabled:opacity-50 disabled:cursor-not-allowed"
        :disabled="!canEnable"
        data-testid="memory-sync-enable"
        @click="enable"
      >
        Turn on memory sync
      </button>
    </div>

    <!-- On: readout + disable. -->
    <div v-else class="space-y-2 text-[12px]" data-testid="memory-sync-on">
      <p data-testid="memory-sync-scopes-on">
        Syncing: {{ status.scopes.map(scopeLabel).join(', ') || '—' }}
      </p>
      <p class="text-ink-muted" data-testid="memory-sync-usage">
        On Fleet: {{ status.liveRecords }} of {{ status.maxRecords }} memories,
        {{ formatBytes(status.liveBytes) }} of {{ formatBytes(status.maxBytes) }}.
        <span v-if="status.pendingCount > 0">{{ status.pendingCount }} waiting to sync.</span>
      </p>
      <p
        v-if="status.blockedCount > 0"
        class="text-signal-warn"
        data-testid="memory-sync-blocked"
      >
        {{ status.blockedCount }} {{ status.blockedCount === 1 ? 'memory stays' : 'memories stay' }}
        on this device only — Fleet refused {{ status.blockedCount === 1 ? 'it' : 'them' }}
        (for example, content that looks like a credential). They are marked in Learned.
      </p>
      <p v-if="laneLine" class="text-ink-muted" data-testid="memory-sync-lane">{{ laneLine }}</p>
      <p v-if="status.fleetError" class="text-ink-muted" data-testid="memory-sync-fleet-error">
        Could not read Fleet settings: {{ status.fleetError }}
      </p>

      <button
        v-if="!confirmingDisable"
        type="button"
        class="px-3 py-1.5 rounded-sm border border-border-muted text-[11px] uppercase tracking-[0.18em] hover:bg-surface-2"
        data-testid="memory-sync-disable"
        @click="startDisable"
      >
        Turn off memory sync
      </button>
      <div v-else class="space-y-2 rounded-sm border border-border-muted p-3" data-testid="memory-sync-disable-confirm">
        <p>
          Turning this off stops syncing on all your devices. Fleet keeps its copy unless you also
          delete it.
        </p>
        <label class="flex items-center gap-2">
          <input v-model="deleteFromFleet" type="checkbox" data-testid="memory-sync-delete-fleet" />
          Also delete all my memory from Fleet (other devices delete their synced copies too)
        </label>
        <div v-if="deleteFromFleet" class="space-y-1">
          <label for="memory-sync-confirm" class="block text-ink-muted">
            Type <code>forget-all</code> to confirm.
          </label>
          <input
            id="memory-sync-confirm"
            v-model="confirmText"
            type="text"
            autocomplete="off"
            class="w-48 rounded-sm border border-border-muted bg-surface-1 px-2 py-1 font-mono"
            data-testid="memory-sync-confirm-text"
          />
        </div>
        <div class="flex gap-2">
          <button
            type="button"
            class="px-3 py-1.5 rounded-sm border border-border-muted text-[11px] uppercase tracking-[0.18em] hover:bg-surface-2 disabled:opacity-50 disabled:cursor-not-allowed"
            :disabled="!canConfirmDisable"
            data-testid="memory-sync-disable-confirm-button"
            @click="confirmDisable"
          >
            {{ deleteFromFleet ? 'Turn off and delete from Fleet' : 'Turn off' }}
          </button>
          <button
            type="button"
            class="px-3 py-1.5 text-[11px] uppercase tracking-[0.18em] text-ink-dim hover:text-accent"
            data-testid="memory-sync-disable-cancel"
            @click="confirmingDisable = false"
          >
            Cancel
          </button>
        </div>
      </div>
    </div>

    <p v-if="errorMsg" class="text-[12px] text-signal-danger" data-testid="memory-sync-error">{{ errorMsg }}</p>
  </section>
</template>
