<script setup lang="ts">
/**
 * AuditSettingsPanel — Settings panel for audit-log retention.
 *
 * Exposes:
 *   - Retention strategy selector: keep_forever | delete_after_window |
 *     archive_after_window (§4.3 of spec).
 *   - Window days spinner (visible when strategy != keep_forever).
 *
 * Writes are committed on form submit so that partial edits do not
 * produce intermediate saves. A success banner confirms persistence.
 *
 * Fact-driven copy (audit-that-tells-the-truth-01PMZA10 UNIT-4, spec
 * D-8 / §5.4 item 7 / E-001 resolution). `retentionEnforced` comes from
 * the server (AuditSettings.retention_enforced) — never a literal here.
 * It is false until UNIT-8 lands a real sweep; when it flips, this file
 * needs NO further edit (that is the whole point of D-8).
 *
 * `keep_forever`'s description is unconditionally true regardless of
 * `retentionEnforced`: choosing keep_forever means nothing sweeps, by
 * definition, whether or not a sweep subsystem exists elsewhere. The
 * OTHER two strategies' descriptions — and the header's second
 * sentence — describe an ACTIVE sweep (delete, or archive-then-delete)
 * that literally cannot happen without one, so both are gated on the
 * fact. This is a deliberate, small correction to the previous draft's
 * "`:66` and `:68` both become unconditionally true" framing: `:68`
 * (archive_after_window) asserts the same active-deletion behaviour
 * `:67` does and cannot be unconditionally true for the same reason —
 * see the mission's own D-6 ("no manufactured success, anywhere...
 * where a path cannot do the work, it says so").
 */
import { onMounted, ref, computed } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { AuditSettings } from '@/lib/types';

const client = useHarnessClient();

const loading = ref(true);
const saving = ref(false);
const saved = ref(false);
const error = ref<string | null>(null);

const strategy = ref<AuditSettings['strategy']>('keep_forever');
const windowDays = ref(90);
const retentionEnforced = ref(false);

const showWindow = computed(() => strategy.value !== 'keep_forever');

onMounted(async () => {
  try {
    const s = await client.settings.getAuditSettings();
    strategy.value = s.strategy;
    windowDays.value = s.window_days;
    retentionEnforced.value = s.retention_enforced;
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
});

async function save() {
  saving.value = true;
  saved.value = false;
  error.value = null;
  try {
    await client.settings.setAuditSettings({
      strategy: strategy.value,
      window_days: windowDays.value,
      retention_enforced: retentionEnforced.value,
    });
    saved.value = true;
    setTimeout(() => { saved.value = false; }, 3000);
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    saving.value = false;
  }
}

const strategyLabels: Record<AuditSettings['strategy'], string> = {
  keep_forever: 'Keep forever',
  delete_after_window: 'Delete after window',
  archive_after_window: 'Archive then delete after window',
};

// strategyDescriptions is a computed, not a literal Record: two of its
// three entries depend on retentionEnforced. keep_forever's entry never
// changes — see the script-block comment above for why that one alone
// is safe to state unconditionally.
const strategyDescriptions = computed<Record<AuditSettings['strategy'], string>>(() => ({
  keep_forever: 'Audit events are never deleted from the database.',
  delete_after_window: retentionEnforced.value
    ? 'Events older than the retention window are permanently deleted during the retention sweep.'
    : 'Retention enforcement is not yet available in this build — no events are deleted today, regardless of this setting. Once enabled, events older than the window will be deleted automatically.',
  archive_after_window: retentionEnforced.value
    ? 'Events older than the window are written to a JSONL archive file then removed from the database.'
    : 'Retention enforcement is not yet available in this build — no events are archived or deleted today, regardless of this setting. Once enabled, events older than the window will be archived to a JSONL file, then removed from the database.',
}));

const headerSubtitle = computed(() =>
  retentionEnforced.value
    ? 'Configure how long audit events are kept in the database. The retention sweep runs regularly.'
    : 'Configure how long audit events are kept in the database. Retention enforcement is not yet active in this build — audit events are kept regardless of the strategy selected below.'
);
</script>

<template>
  <section data-testid="audit-settings-panel">
    <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
      Audit Log Retention
    </h2>
    <p class="mt-1 font-ui text-[11px] text-ink-dim" data-testid="audit-settings-subtitle">
      {{ headerSubtitle }}
    </p>

    <div v-if="loading" class="mt-4 font-ui text-[12px] text-ink-muted" data-testid="audit-settings-loading">
      Loading…
    </div>

    <form v-else class="mt-4 grid gap-4 max-w-md" data-testid="audit-settings-form" @submit.prevent="save">
      <!-- Strategy selector -->
      <div class="grid gap-2">
        <label
          v-for="(label, val) in strategyLabels"
          :key="val"
          class="flex items-start gap-2 cursor-pointer"
          :data-testid="`audit-strategy-${val}`"
        >
          <input
            type="radio"
            name="audit-strategy"
            :value="val"
            v-model="strategy"
            class="mt-0.5 shrink-0 accent-accent"
          />
          <span class="grid gap-0.5">
            <span class="font-ui text-[12px] text-ink">{{ label }}</span>
            <span class="font-ui text-[11px] text-ink-dim">{{ strategyDescriptions[val as AuditSettings['strategy']] }}</span>
          </span>
        </label>
      </div>

      <!-- Window days (hidden for keep_forever) -->
      <div v-if="showWindow" class="grid gap-1">
        <label for="audit-window-days" class="font-ui text-[11px] uppercase tracking-[0.14em] text-ink-subtle">
          Retention window (days)
        </label>
        <input
          id="audit-window-days"
          v-model.number="windowDays"
          type="number"
          min="1"
          max="3650"
          class="w-32 rounded-sm border border-border bg-surface-1 px-2 py-1 font-ui text-[12px] text-ink focus:outline-none focus:ring-1 focus:ring-accent"
          data-testid="audit-window-days"
        />
        <p class="font-ui text-[11px] text-ink-dim">
          Events older than {{ windowDays }} day{{ windowDays === 1 ? '' : 's' }} will be swept.
        </p>
      </div>

      <!-- Error banner -->
      <div
        v-if="error"
        role="alert"
        class="rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        data-testid="audit-settings-error"
      >
        {{ error }}
      </div>

      <!-- Success banner -->
      <div
        v-if="saved"
        role="status"
        class="rounded-sm border border-signal-success bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-success"
        data-testid="audit-settings-saved"
      >
        Retention settings saved.
      </div>

      <!-- Submit -->
      <div>
        <button
          type="submit"
          :disabled="saving"
          class="rounded-sm border border-border bg-surface-2 px-3 py-1.5 font-ui text-[12px] text-ink hover:bg-surface-3 disabled:opacity-50 transition-colors"
          data-testid="audit-settings-save"
        >
          {{ saving ? 'Saving…' : 'Save' }}
        </button>
      </div>
    </form>
  </section>
</template>
