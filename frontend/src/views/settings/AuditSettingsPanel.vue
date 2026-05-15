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

const showWindow = computed(() => strategy.value !== 'keep_forever');

onMounted(async () => {
  try {
    const s = await client.settings.getAuditSettings();
    strategy.value = s.strategy;
    windowDays.value = s.windowDays;
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
      windowDays: windowDays.value,
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

const strategyDescriptions: Record<AuditSettings['strategy'], string> = {
  keep_forever: 'Audit events are never deleted from the database.',
  delete_after_window: 'Events older than the retention window are permanently deleted during the nightly sweep.',
  archive_after_window: 'Events older than the window are written to a JSONL archive file then removed from the database.',
};
</script>

<template>
  <section data-testid="audit-settings-panel">
    <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
      Audit Log Retention
    </h2>
    <p class="mt-1 font-ui text-[11px] text-ink-dim">
      Configure how long audit events are kept in the database.
      The retention sweep runs nightly.
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
