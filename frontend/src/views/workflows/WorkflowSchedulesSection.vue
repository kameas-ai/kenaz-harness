<script setup lang="ts">
/**
 * WorkflowSchedulesSection — the cron-schedule editor for workflows.
 *
 * nav-ia-sweep-01DOGF0F WP04 (FR-3). Lifted out of
 * `WorkflowsSettingsPanel.vue` (workflow-extensions-01KW2D3Y WP02), which was
 * the ONLY UI that called `scheduleSet` / `scheduleClear` — WorkflowsView had
 * none. Settings › Workflows was therefore not a duplicate to delete but a
 * capability to move: it now lives here, under Workflows › Schedules, beside
 * scheduled chats ("things that run on a timer").
 *
 * One row per installed workflow: current cron (if any), Schedule /
 * Reschedule (modal: 5-field cron + IANA timezone), Unschedule.
 */
import { onMounted, ref } from 'vue';
import type {
  WorkflowsClient,
  WorkflowsScheduleEntry,
  WorkflowsSummary,
} from '@/lib/workflowsClient';

const props = defineProps<{ client: WorkflowsClient }>();

const catalog = ref<WorkflowsSummary[]>([]);
const schedules = ref<WorkflowsScheduleEntry[]>([]);
const loading = ref(false);
const loadError = ref<string | null>(null);

async function loadSchedules() {
  try {
    schedules.value = await props.client.scheduleList();
  } catch {
    schedules.value = [];
  }
}

async function load() {
  loading.value = true;
  loadError.value = null;
  try {
    catalog.value = await props.client.list();
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
  await loadSchedules();
}

onMounted(load);

function scheduleFor(workflowId: string): WorkflowsScheduleEntry | undefined {
  return schedules.value.find((s) => s.workflowId === workflowId);
}

// ── schedule modal ──────────────────────────────────────────────────────

const scheduleModalId = ref<string | null>(null);
const scheduleCron = ref('0 7 * * *');
const scheduleTimezone = ref('UTC');
const scheduleSaving = ref(false);
const scheduleError = ref<string | null>(null);

function openScheduleModal(id: string) {
  scheduleModalId.value = id;
  const existing = scheduleFor(id);
  scheduleCron.value = existing?.cron ?? '0 7 * * *';
  scheduleTimezone.value = existing?.timezone ?? 'UTC';
  scheduleError.value = null;
}

function closeScheduleModal() {
  scheduleModalId.value = null;
}

async function saveSchedule() {
  if (!scheduleModalId.value) return;
  scheduleSaving.value = true;
  scheduleError.value = null;
  try {
    await props.client.scheduleSet({
      workflowId: scheduleModalId.value,
      cron: scheduleCron.value,
      timezone: scheduleTimezone.value,
    });
    closeScheduleModal();
    await loadSchedules();
  } catch (err) {
    scheduleError.value = err instanceof Error ? err.message : String(err);
  } finally {
    scheduleSaving.value = false;
  }
}

async function clearSchedule(id: string) {
  try {
    await props.client.scheduleClear(id);
    await loadSchedules();
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : String(err);
  }
}

</script>

<template>
  <section class="space-y-3" data-testid="workflow-schedules-section">
    <div>
      <h2 class="font-ui text-sm font-medium text-ink">Workflow schedules</h2>
      <p class="font-ui text-xs text-ink-muted">
        Run a saved workflow on a cron schedule. Past and upcoming runs are
        on the Runs tab.
      </p>
    </div>

    <div
      v-if="loadError"
      class="rounded-sm border border-signal-danger bg-signal-danger-soft p-3 font-ui text-sm text-signal-danger"
      data-testid="wf-sched-load-error"
    >
      {{ loadError }}
    </div>

    <div v-if="loading" class="font-ui text-sm text-ink-muted">Loading workflows…</div>

    <div
      v-else-if="catalog.length === 0"
      class="rounded-sm border border-border-muted bg-surface-1 p-4 font-ui text-sm text-ink-muted"
      data-testid="wf-sched-empty"
    >
      No workflows installed yet — create one on the Library tab, then
      schedule it here.
    </div>

    <div
      v-else
      class="rounded-sm border border-border-muted overflow-hidden"
      data-testid="wf-sched-table"
    >
      <table class="w-full font-ui text-sm">
        <thead>
          <tr class="border-b border-border-muted bg-surface-1">
            <th class="text-left px-3 py-2 text-xs uppercase tracking-wide text-ink-muted">Workflow</th>
            <th class="text-left px-3 py-2 text-xs uppercase tracking-wide text-ink-muted">Schedule</th>
            <th class="px-3 py-2 text-xs uppercase tracking-wide text-ink-muted text-right">Actions</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="wf in catalog"
            :key="wf.id"
            class="border-b border-border-muted last:border-0 hover:bg-surface-1"
            :data-testid="`wf-sched-row-${wf.id}`"
          >
            <td class="px-3 py-2 text-ink font-medium">{{ wf.name }}</td>
            <td class="px-3 py-2">
              <span
                v-if="scheduleFor(wf.id)"
                class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-mono text-xs bg-signal-ok-soft text-signal-ok"
                :data-testid="`wf-sched-badge-${wf.id}`"
              >
                {{ scheduleFor(wf.id)!.cron }}
                <span v-if="scheduleFor(wf.id)!.timezone" class="text-ink-muted">
                  {{ scheduleFor(wf.id)!.timezone }}
                </span>
              </span>
              <span v-else class="font-ui text-xs text-ink-muted">—</span>
            </td>
            <td class="px-3 py-2">
              <!-- skill-library-01SKLIB01: an org-required workflow's schedule
                   is the org's; the backend refuses Schedule/Unschedule. -->
              <div
                v-if="wf.orgManaged"
                class="text-right font-ui text-xs text-ink-muted"
                :data-testid="`wf-sched-org-managed-${wf.id}`"
              >
                Required by your org
              </div>
              <div v-else class="flex items-center justify-end gap-1">
                <button
                  type="button"
                  class="rounded-sm px-2 py-1 font-ui text-xs text-ink-muted hover:text-ink hover:bg-surface-2"
                  :data-testid="`wf-sched-set-${wf.id}`"
                  @click="openScheduleModal(wf.id)"
                >
                  {{ scheduleFor(wf.id) ? 'Reschedule' : 'Schedule' }}
                </button>
                <button
                  v-if="scheduleFor(wf.id)"
                  type="button"
                  class="rounded-sm px-2 py-1 font-ui text-xs text-ink-muted hover:text-signal-danger"
                  :data-testid="`wf-sched-clear-${wf.id}`"
                  @click="clearSchedule(wf.id)"
                >
                  Unschedule
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <dialog
      v-if="scheduleModalId !== null"
      open
      class="fixed inset-0 z-50 m-auto w-full max-w-sm rounded-sm border border-border-muted bg-surface-1 p-5 shadow-xl"
      data-testid="wf-sched-modal"
    >
      <h3 class="font-ui text-base text-ink mb-3">Set schedule</h3>
      <div class="space-y-3 mb-4">
        <div>
          <label class="font-ui text-xs text-ink-muted block mb-1" for="wf-sched-cron">
            Cron expression (5-field)
          </label>
          <input
            id="wf-sched-cron"
            v-model="scheduleCron"
            type="text"
            class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-sm text-ink"
            data-testid="wf-sched-cron-input"
            placeholder="0 7 * * *"
          />
        </div>
        <div>
          <label class="font-ui text-xs text-ink-muted block mb-1" for="wf-sched-tz">
            Timezone (IANA)
          </label>
          <input
            id="wf-sched-tz"
            v-model="scheduleTimezone"
            type="text"
            class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-sm text-ink"
            data-testid="wf-sched-tz-input"
            placeholder="UTC"
          />
        </div>
      </div>
      <div
        v-if="scheduleError"
        class="mb-3 font-ui text-sm text-signal-danger"
        data-testid="wf-sched-error"
      >
        {{ scheduleError }}
      </div>
      <div class="flex gap-2 justify-end">
        <button
          type="button"
          class="rounded-sm border border-border-muted px-3 py-1.5 font-ui text-sm text-ink hover:bg-surface-2"
          data-testid="wf-sched-cancel"
          @click="closeScheduleModal"
        >
          Cancel
        </button>
        <button
          type="button"
          class="rounded-sm border border-accent bg-accent px-3 py-1.5 font-ui text-sm text-bg hover:opacity-90 disabled:opacity-50"
          :disabled="scheduleSaving"
          data-testid="wf-sched-save"
          @click="saveSchedule"
        >
          {{ scheduleSaving ? 'Saving…' : 'Save schedule' }}
        </button>
      </div>
    </dialog>
  </section>
</template>
