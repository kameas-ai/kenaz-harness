<script setup lang="ts">
/**
 * MigrationDriftPanel — Settings → Health — migration drift detector.
 *
 * Surfaces discrepancies between the harness_migrations ledger and the
 * registered migration set (v0.5.1 migration-doctor). Motivation: the
 * May 2026 incident (PR #71) where version 322 was renumbered between
 * dev builds, leaving users with a silently broken DB until manually
 * patched.
 *
 * Drift kinds:
 *   id_mismatch  — "error"   — automatable fix (Apply button)
 *   ledger_only  — "warning" — needs manual inspection
 *   code_only    — "info"    — pending migration; will apply on next boot
 */
import { onMounted, ref, computed } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { DriftEntry, DriftReport } from '@/lib/types';

const client = useHarnessClient();

// ── state ──────────────────────────────────────────────────────────────

const loading = ref(true);
const loadError = ref<string | null>(null);
const report = ref<DriftReport | null>(null);
/** Per-entry fix state keyed by version. */
const fixBusy = ref<Record<number, boolean>>({});
const fixError = ref<Record<number, string | null>>({});
const fixDone = ref<Record<number, boolean>>({});

// ── derived ────────────────────────────────────────────────────────────

const actionableDrifts = computed<DriftEntry[]>(() => {
  if (!report.value) return [];
  // Surface id_mismatch + ledger_only; suppress code_only (info) by default.
  return report.value.drifts.filter(
    (d) => d.kind === 'id_mismatch' || d.kind === 'ledger_only',
  );
});

const pendingCount = computed<number>(() => {
  if (!report.value) return 0;
  return report.value.drifts.filter((d) => d.kind === 'code_only').length;
});

const hasErrors = computed<boolean>(() =>
  actionableDrifts.value.some((d) => d.severity === 'error'),
);

/**
 * FR-005: Status label for the summary pill.
 *
 * "drift" = actionable mismatches (id_mismatch / ledger_only) that need
 * human attention.  "pending" = code_only entries that will self-apply on
 * the next boot — not drift in the corrective sense.
 *
 * Showing "No drift detected" while the pending-note says "8 pending" was
 * contradictory.  Instead:
 *   - actionable drift present → "N drift(s) detected" (unchanged)
 *   - no actionable drift, but pending migrations → "N pending (no drift)"
 *   - fully clean → "No drift · 0 pending"
 */
const statusLabel = computed<string>(() => {
  if (loading.value) return 'Checking…';
  if (loadError.value) return 'Error loading report';
  if (!report.value) return '—';
  const n = actionableDrifts.value.length;
  if (n > 0) return `${n} drift${n === 1 ? '' : 's'} detected`;
  const p = pendingCount.value;
  if (p > 0) return `${p} pending (no drift)`;
  return 'No drift · 0 pending';
});

// ── lifecycle ──────────────────────────────────────────────────────────

async function load() {
  loading.value = true;
  loadError.value = null;
  try {
    report.value = await client.storage.getMigrationDriftReport();
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : 'Failed to load drift report.';
  } finally {
    loading.value = false;
  }
}

async function applyFix(entry: DriftEntry) {
  if (entry.kind !== 'id_mismatch') return;
  if (fixBusy.value[entry.version]) return;

  fixBusy.value = { ...fixBusy.value, [entry.version]: true };
  fixError.value = { ...fixError.value, [entry.version]: null };

  try {
    await client.storage.applyDriftFix(entry.version);
    fixDone.value = { ...fixDone.value, [entry.version]: true };
    // Refresh the report after a successful fix.
    await load();
  } catch (e) {
    fixError.value = {
      ...fixError.value,
      [entry.version]: e instanceof Error ? e.message : 'Fix failed.',
    };
  } finally {
    fixBusy.value = { ...fixBusy.value, [entry.version]: false };
  }
}

onMounted(() => {
  void load();
});

// ── severity styles ────────────────────────────────────────────────────

function severityBadge(severity: DriftEntry['severity']): string {
  switch (severity) {
    case 'error':
      return 'bg-signal-danger/10 text-signal-danger border border-signal-danger/20';
    case 'warning':
      return 'bg-signal-warn/10 text-signal-warn border border-signal-warn/20';
    default:
      return 'bg-surface-2 text-ink-muted border border-border-muted';
  }
}
</script>

<template>
  <section
    class="px-6 py-4"
    data-testid="migration-drift-panel"
  >
    <!-- Panel header -->
    <h2
      class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle mb-3"
    >
      Migration health
    </h2>

    <!-- Status summary card -->
    <div
      class="rounded-sm border border-border-muted bg-surface-1 mb-4"
      data-testid="drift-status-card"
    >
      <div class="px-4 py-3 flex items-center justify-between gap-3">
        <div>
          <div class="font-ui text-[13px] text-ink">
            Database migration drift
          </div>
          <p class="mt-0.5 text-[11px] text-ink-muted max-w-prose">
            Compares the live
            <code class="font-mono text-[11px]">harness_migrations</code>
            ledger against the registered migration set and surfaces ID
            mismatches, orphaned ledger rows, or pending migrations.
          </p>
        </div>

        <!-- Status pill -->
        <div class="shrink-0">
          <span
            v-if="loading"
            class="text-[11px] text-ink-muted"
            data-testid="drift-status-loading"
          >Checking…</span>

          <span
            v-else-if="loadError"
            class="inline-flex items-center gap-1 rounded px-2 py-0.5 text-[11px]
                   bg-signal-danger/10 text-signal-danger border border-signal-danger/20"
            data-testid="drift-status-error"
          >Error</span>

          <!-- No actionable drift + pending migrations: amber "N pending" pill
               (FR-005 — avoid contradiction with "8 pending" note below) -->
          <span
            v-else-if="actionableDrifts.length === 0 && pendingCount > 0"
            class="inline-flex items-center gap-1 rounded px-2 py-0.5 text-[11px]
                   bg-signal-warn/10 text-signal-warn border border-signal-warn/20"
            data-testid="drift-status-pending"
          >{{ statusLabel }}</span>

          <!-- Fully clean: no actionable drift, no pending migrations -->
          <span
            v-else-if="actionableDrifts.length === 0"
            class="inline-flex items-center gap-1 rounded px-2 py-0.5 text-[11px]
                   bg-signal-success/10 text-signal-success border border-signal-success/20"
            data-testid="drift-status-ok"
          >{{ statusLabel }}</span>

          <span
            v-else
            :class="[
              'inline-flex items-center gap-1 rounded px-2 py-0.5 text-[11px]',
              hasErrors
                ? 'bg-signal-danger/10 text-signal-danger border border-signal-danger/20'
                : 'bg-signal-warn/10 text-signal-warn border border-signal-warn/20',
            ]"
            data-testid="drift-status-drift"
          >{{ statusLabel }}</span>
        </div>
      </div>

      <!-- Refresh button row -->
      <div class="px-4 pb-3 flex items-center gap-2">
        <button
          type="button"
          class="text-[11px] text-accent hover:underline disabled:opacity-50"
          :disabled="loading"
          data-testid="drift-refresh-btn"
          @click="load"
        >
          {{ loading ? 'Refreshing…' : 'Refresh' }}
        </button>
      </div>
    </div>

    <!-- Load error message -->
    <div
      v-if="loadError"
      class="mb-4 rounded-sm border border-signal-danger/30 bg-signal-danger/5 px-4 py-3"
      role="alert"
      data-testid="drift-load-error"
    >
      <p class="text-[12px] text-signal-danger">{{ loadError }}</p>
    </div>

    <!-- Drift entries -->
    <div
      v-if="!loading && !loadError && actionableDrifts.length > 0"
      class="rounded-sm border border-border-muted bg-surface-1 divide-y divide-border-muted"
      data-testid="drift-entries"
    >
      <div
        v-for="entry in actionableDrifts"
        :key="entry.version"
        class="px-4 py-3"
        :data-testid="`drift-entry-${entry.version}`"
      >
        <!-- Entry header row -->
        <div class="flex items-start gap-2 mb-1">
          <span
            :class="[
              'shrink-0 rounded px-1.5 py-0.5 text-[10px] font-mono',
              severityBadge(entry.severity),
            ]"
            :data-testid="`drift-severity-${entry.version}`"
          >{{ entry.severity }}</span>
          <span class="font-mono text-[12px] text-ink">v{{ entry.version }}</span>
          <span
            class="text-[11px] font-ui uppercase tracking-wider text-ink-muted ml-auto"
          >{{ entry.kind.replace('_', ' ') }}</span>
        </div>

        <!-- ID comparison (id_mismatch only) -->
        <div
          v-if="entry.kind === 'id_mismatch'"
          class="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-[11px]"
          :data-testid="`drift-ids-${entry.version}`"
        >
          <span class="text-ink-subtle">Ledger ID</span>
          <code
            class="font-mono text-signal-danger"
            :data-testid="`drift-ledger-id-${entry.version}`"
          >{{ entry.ledgerId }}</code>
          <span class="text-ink-subtle">Expected ID</span>
          <code
            class="font-mono text-signal-success"
            :data-testid="`drift-expected-id-${entry.version}`"
          >{{ entry.expectedId }}</code>
        </div>

        <!-- Suggestion text -->
        <p
          class="mt-2 text-[11px] text-ink-muted max-w-prose"
          :data-testid="`drift-suggestion-${entry.version}`"
        >{{ entry.suggestion }}</p>

        <!-- Fix success -->
        <p
          v-if="fixDone[entry.version]"
          class="mt-2 text-[11px] text-signal-success"
          :data-testid="`drift-fix-done-${entry.version}`"
        >Fix applied successfully. Ledger updated.</p>

        <!-- Fix error -->
        <p
          v-if="fixError[entry.version]"
          class="mt-2 text-[11px] text-signal-danger"
          role="alert"
          :data-testid="`drift-fix-error-${entry.version}`"
        >{{ fixError[entry.version] }}</p>

        <!-- Apply fix button (id_mismatch only) -->
        <div
          v-if="entry.kind === 'id_mismatch' && !fixDone[entry.version]"
          class="mt-3"
        >
          <button
            type="button"
            class="rounded px-3 py-1.5 text-[11px] font-ui bg-accent text-on-accent
                   hover:bg-accent-hover disabled:opacity-50 disabled:cursor-wait"
            :disabled="fixBusy[entry.version] ?? false"
            :data-testid="`drift-fix-btn-${entry.version}`"
            @click="applyFix(entry)"
          >
            {{ fixBusy[entry.version] ? 'Applying…' : 'Apply automatic fix' }}
          </button>
          <p class="mt-1 text-[10px] text-ink-muted">
            Creates a backup of <code class="font-mono">data.db</code> before modifying the ledger.
          </p>
        </div>
      </div>
    </div>

    <!-- Pending (code_only) count note -->
    <p
      v-if="!loading && !loadError && pendingCount > 0"
      class="mt-3 text-[11px] text-ink-muted"
      data-testid="drift-pending-note"
    >
      <span class="text-ink">{{ pendingCount }}</span>
      pending migration{{ pendingCount === 1 ? '' : 's' }} (not yet applied) — will run automatically on next boot.
    </p>

    <!-- Clean state (no actionable drifts, no pending) -->
    <p
      v-if="!loading && !loadError && actionableDrifts.length === 0 && pendingCount === 0"
      class="text-[11px] text-ink-muted"
      data-testid="drift-clean-note"
    >
      All registered migrations match the ledger. No pending migrations.
    </p>
  </section>
</template>
