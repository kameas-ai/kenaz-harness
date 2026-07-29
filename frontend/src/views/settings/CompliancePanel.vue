<script setup lang="ts">
/**
 * CompliancePanel — Security → Compliance settings section.
 *
 * Shows fleet audit archival status: last archived timestamp, pending-event
 * count, retention window selector (30 / 60 / 90 / 365 days), and an
 * "Archive now" trigger. When CapAuditLogImmuDB is inactive (enabled=false),
 * a gating notice replaces the controls. A red banner appears when a hash-
 * chain continuity break has been detected (archival halted until resolved).
 *
 * (fleet-audit-archival-01NDFSEX13 WP06)
 */
import { ref, computed, onMounted } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { ComplianceStatus } from '@/lib/types';

const client = useHarnessClient();

// ── State ──────────────────────────────────────────────────────────────────

const status = ref<ComplianceStatus | null>(null);
const loadError = ref('');
const archiving = ref(false);
const archiveError = ref('');
const retentionSaving = ref(false);
const retentionError = ref('');

// ── Load ────────────────────────────────────────────────────────────────────

async function loadStatus() {
  loadError.value = '';
  try {
    status.value = await client.compliance.status();
  } catch (err) {
    loadError.value = String(err);
  }
}

onMounted(() => {
  void loadStatus();
});

// ── Computed ────────────────────────────────────────────────────────────────

const enabled = computed(() => status.value?.enabled ?? false);

const chainBreak = computed(() => status.value?.chainBreak ?? false);

const lastArchivedDisplay = computed<string>(() => {
  const ts = status.value?.lastArchivedAt;
  if (!ts) return 'Never';
  try {
    const d = new Date(ts);
    if (isNaN(d.getTime())) return ts;
    return d.toLocaleString();
  } catch {
    return ts;
  }
});

const pendingCount = computed(() => status.value?.pendingCount ?? 0);

const currentRetention = computed<number>(() => status.value?.retentionDays ?? 90);

const retentionOptions: ReadonlyArray<{ label: string; value: number }> = [
  { label: '30 days', value: 30 },
  { label: '60 days', value: 60 },
  { label: '90 days (default)', value: 90 },
  { label: '365 days', value: 365 },
];

// ── Actions ─────────────────────────────────────────────────────────────────

async function archiveNow() {
  archiving.value = true;
  archiveError.value = '';
  try {
    await client.compliance.archiveNow();
    await loadStatus();
  } catch (err) {
    archiveError.value = String(err);
  } finally {
    archiving.value = false;
  }
}

async function setRetention(days: number) {
  retentionSaving.value = true;
  retentionError.value = '';
  try {
    await client.compliance.setRetention(days);
    // Optimistically reflect the new value while we reload.
    if (status.value) {
      status.value = { ...status.value, retentionDays: days };
    }
    await loadStatus();
  } catch (err) {
    retentionError.value = String(err);
  } finally {
    retentionSaving.value = false;
  }
}
</script>

<template>
  <section class="space-y-6 p-4" data-testid="compliance-panel">
    <!-- Load error -->
    <p v-if="loadError" class="text-xs text-signal-danger" data-testid="compliance-load-error">
      Failed to load compliance status: {{ loadError }}
    </p>

    <!-- Header -->
    <div>
      <h2 class="text-sm font-semibold text-ink mb-1">Audit Archival</h2>
      <p class="text-xs text-ink-muted">
        Audit events are signed with your device key and archived to an
        immutable immudb ledger. Requires the
        <span class="font-medium">Team+</span> subscription tier.
        No conversation content, API keys, or credentials are included in
        audit records.
      </p>
    </div>

    <!-- Not-enabled gate -->
    <div
      v-if="status !== null && !enabled"
      class="rounded border border-border-muted p-4 text-center space-y-1"
      data-testid="compliance-disabled-notice"
    >
      <p class="text-sm font-medium text-ink">Not available on your current plan</p>
      <p class="text-xs text-ink-muted">
        Upgrade to Team+ to enable immutable audit archival.
      </p>
    </div>

    <!-- Chain-break banner (shown only when enabled) -->
    <div
      v-if="enabled && chainBreak"
      class="rounded border border-signal-danger bg-signal-danger-soft p-4 space-y-1"
      data-testid="compliance-chain-break-banner"
    >
      <p class="text-sm font-semibold text-signal-danger">
        Hash-chain continuity break detected
      </p>
      <p class="text-xs text-signal-danger">
        Archival has been halted. A gap in the audit hash chain was detected
        since the last successful flush. Review
        <code>fleet.audit_chain_break</code> events in the audit log, then
        contact your administrator to advance the cursor past the break point.
      </p>
    </div>

    <!-- Status grid (shown when enabled and status loaded) -->
    <dl
      v-if="enabled && status !== null"
      class="grid grid-cols-2 gap-x-4 gap-y-2 text-xs"
      data-testid="compliance-status-grid"
    >
      <div>
        <dt class="text-ink-muted">Last archived</dt>
        <dd class="font-medium text-ink" data-testid="compliance-last-archived">
          {{ lastArchivedDisplay }}
        </dd>
      </div>
      <div>
        <dt class="text-ink-muted">Pending events</dt>
        <dd class="font-medium text-ink" data-testid="compliance-pending-count">
          {{ pendingCount }}
        </dd>
      </div>
      <div>
        <dt class="text-ink-muted">Archiver</dt>
        <dd class="font-medium text-ink" data-testid="compliance-archiver-running">
          {{ status.archiverRunning ? 'Running' : 'Stopped' }}
        </dd>
      </div>
    </dl>

    <!-- Retention picker (shown when enabled) -->
    <fieldset
      v-if="enabled"
      class="space-y-2"
      :disabled="retentionSaving"
    >
      <legend class="text-xs font-medium text-ink-muted uppercase tracking-wide">
        Local retention window
      </legend>
      <div class="space-y-1">
        <label
          v-for="opt in retentionOptions"
          :key="opt.value"
          class="flex items-center gap-2 cursor-pointer"
        >
          <input
            type="radio"
            name="compliance-retention"
            :value="opt.value"
            :checked="currentRetention === opt.value"
            class="mt-0.5"
            @change="setRetention(opt.value)"
          />
          <span class="text-sm text-ink">{{ opt.label }}</span>
        </label>
      </div>
      <p v-if="retentionError" class="text-xs text-signal-danger" data-testid="compliance-retention-error">
        {{ retentionError }}
      </p>
      <p v-if="retentionSaving" class="text-xs text-ink-muted">Saving…</p>
    </fieldset>

    <!-- Archive now -->
    <div v-if="enabled" class="flex items-center gap-3">
      <button
        type="button"
        class="px-3 py-1.5 rounded text-xs font-medium bg-surface-2 border border-border-muted text-ink hover:bg-surface-3 disabled:opacity-50 disabled:cursor-not-allowed"
        :disabled="archiving || chainBreak"
        data-testid="compliance-archive-now-btn"
        @click="archiveNow"
      >
        {{ archiving ? 'Archiving…' : 'Archive now' }}
      </button>
      <p v-if="chainBreak" class="text-xs text-signal-danger">
        Resolve chain break before archiving.
      </p>
      <p v-if="archiveError" class="text-xs text-signal-danger" data-testid="compliance-archive-error">
        {{ archiveError }}
      </p>
    </div>

    <!-- Refresh -->
    <div v-if="status !== null">
      <button
        type="button"
        class="text-xs text-ink-muted hover:text-ink underline"
        data-testid="compliance-refresh-btn"
        @click="loadStatus"
      >
        Refresh status
      </button>
    </div>
  </section>
</template>
