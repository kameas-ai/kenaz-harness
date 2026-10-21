<script setup lang="ts">
/**
 * ScheduledChatFormModal — create / edit modal for a scheduled chat run.
 *
 * Mission: scheduled-chat-runs-01KX5R8B (WP05).
 *
 * Emits "saved" with the new/updated ChatRunEntry on success.
 * Emits "cancel" when the user dismisses without saving.
 *
 * Tool set (tool-context-budget-01TCBUD01 WP06, FR-K3): a schedule has no
 * project, so its runs resolve the user's default tiers; the form shows
 * that set's per-request cost (Tools_SchemaCosts with no session and no
 * project) next to the model. A schedule contained to a tool allowlist
 * runs a narrower catalog than that, so its cost is not shown. "Custom
 * servers" is rendered disabled with its reason: the schedule record has
 * no tool-set field.
 * TODO(dated 2026-10-09, owner alec — follow-up mission "schedule tool
 * set"): wire "Custom servers" once the schedule record carries a tool set.
 *
 * Conversation memory (scheduled-chat-context-budget, sessions/0349): every
 * fire continues one conversation, so the form sets how much of it a run
 * carries — the context budget (4000 tokens up to the model's window),
 * how many recent runs stay verbatim (0–10), and what the summary of older
 * runs must remember. The server validates the same bounds.
 */
import { ref, computed, watch, onMounted } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { formatTokens, sendableTokens } from '@/lib/toolExposure';
import {
  CONTEXT_BUDGET_DEFAULT,
  CONTEXT_BUDGET_MAX,
  CONTEXT_BUDGET_MIN,
  KEEP_RECENT_RUNS_DEFAULT,
  KEEP_RECENT_RUNS_MAX,
  MEMORY_FOCUS_MAX_CHARS,
  type ScheduledChatClient,
  type ScheduledChatEntry,
} from '@/lib/scheduledChatClient';

const props = defineProps<{
  client: ScheduledChatClient;
  /** When non-null, the modal is in edit mode for the given entry. */
  editing: ScheduledChatEntry | null;
}>();

const emit = defineEmits<{
  saved: [entry: ScheduledChatEntry];
  cancel: [];
}>();

// ── form state ────────────────────────────────────────────────────────────

const name = ref('');
const promptTemplate = ref('');
const cron = ref('0 9 * * *');
const timezone = ref('');
const model = ref('');
const outputSink = ref('banner');
const filePath = ref('');
const enabled = ref(true);
// One-shot schedules (model-scheduled-jobs-01PMSJ01 WP08, FR-006).
// triggerKind "cron" is the pre-existing, only-ever-had behaviour;
// "once" arms runAt (a <input type="datetime-local"> value, local time)
// instead of requiring a fabricated cron expression.
const triggerKind = ref<'cron' | 'once'>('cron');
const runAt = ref('');
// Conversation memory (sessions/0349). Number inputs bind as numbers; an
// emptied field reads as '' and fails validation.
const contextBudget = ref<number | ''>(CONTEXT_BUDGET_DEFAULT);
const keepRecentRuns = ref<number | ''>(KEEP_RECENT_RUNS_DEFAULT);
const memoryFocus = ref('');

// What "active default" resolves to — the same resolution the dispatcher
// applies at fire time — shown under the field while it is blank. null
// means it could not be read; '' means no default is configured.
const defaultModel = ref<string | null>(null);
// The default model's context window (the budget's ceiling); 0 = unknown.
const defaultWindow = ref(0);
onMounted(async () => {
  try {
    const dm = await props.client.defaultModel();
    defaultModel.value = dm.model || '';
    defaultWindow.value = dm.maxContextTokens ?? 0;
  } catch {
    defaultModel.value = null;
  }
});

// The default tool set's per-request cost. null = not known (the read
// failed): the line is not rendered.
const harness = useHarnessClient();
const toolSetTokens = ref<number | null>(null);
const toolSetFull = ref(0);
const contained = computed(() => (props.editing?.toolAllowlist?.length ?? 0) > 0);
onMounted(async () => {
  try {
    const costs = await harness.tools.schemaCosts('', '');
    toolSetTokens.value = sendableTokens(costs);
    toolSetFull.value = costs.reduce(
      (n, c) => n + (c.tools ?? []).filter((t) => t.sendable).length,
      0,
    );
  } catch {
    toolSetTokens.value = null;
  }
});
// Populate form when editing entry changes.
watch(
  () => props.editing,
  (entry) => {
    if (entry) {
      name.value = entry.name;
      promptTemplate.value = entry.promptTemplate;
      cron.value = entry.cron;
      timezone.value = entry.timezone ?? '';
      model.value = entry.model ?? '';
      const sink = entry.outputSink ?? 'banner';
      if (sink.startsWith('file:')) {
        outputSink.value = 'file';
        filePath.value = sink.slice('file:'.length);
      } else {
        outputSink.value = sink;
        filePath.value = '';
      }
      enabled.value = entry.enabled;
      triggerKind.value = entry.triggerKind === 'once' ? 'once' : 'cron';
      // entry.runAt is ISO 8601 UTC; datetime-local inputs want
      // "YYYY-MM-DDTHH:mm" in LOCAL time.
      runAt.value = entry.runAt ? toDatetimeLocal(entry.runAt) : '';
      contextBudget.value = entry.contextBudgetTokens ?? CONTEXT_BUDGET_DEFAULT;
      keepRecentRuns.value = entry.keepRecentRuns ?? KEEP_RECENT_RUNS_DEFAULT;
      memoryFocus.value = entry.memoryFocus ?? '';
    } else {
      name.value = '';
      promptTemplate.value = '';
      cron.value = '0 9 * * *';
      timezone.value = '';
      model.value = '';
      outputSink.value = 'banner';
      filePath.value = '';
      enabled.value = true;
      triggerKind.value = 'cron';
      runAt.value = '';
      contextBudget.value = CONTEXT_BUDGET_DEFAULT;
      keepRecentRuns.value = KEEP_RECENT_RUNS_DEFAULT;
      memoryFocus.value = '';
    }
  },
  { immediate: true },
);

/** ISO 8601 (UTC) -> "YYYY-MM-DDTHH:mm" in local time, for a datetime-local input. */
function toDatetimeLocal(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// ── validation ────────────────────────────────────────────────────────────

const isEditMode = computed(() => props.editing !== null);
const title = computed(() => (isEditMode.value ? 'Edit scheduled chat' : 'New scheduled chat'));

const isOnce = computed(() => triggerKind.value === 'once');

/** Basic 5-field cron validation (not a full parser). Only applies to triggerKind=cron. */
const cronValid = computed(() => {
  const parts = cron.value.trim().split(/\s+/);
  return parts.length === 5 && parts.every((p) => p !== '');
});

const runAtValid = computed(() => runAt.value.trim() !== '' && !Number.isNaN(new Date(runAt.value).getTime()));

const filePathRequired = computed(() => outputSink.value === 'file');
const sinkValue = computed(() =>
  outputSink.value === 'file' ? `file:${filePath.value}` : outputSink.value,
);

// The budget's ceiling: the default model's window when the schedule
// runs on it and the window is known, else the absolute maximum (the
// server checks a named model's own window).
const budgetCeiling = computed(() =>
  !model.value.trim() && defaultWindow.value > 0 ? defaultWindow.value : CONTEXT_BUDGET_MAX,
);
const budgetError = computed(() => {
  const v = contextBudget.value;
  if (v === '' || !Number.isInteger(v)) return 'Enter a whole number of tokens';
  if (v < CONTEXT_BUDGET_MIN) return `At least ${CONTEXT_BUDGET_MIN.toLocaleString()} tokens`;
  if (v > budgetCeiling.value) {
    return budgetCeiling.value === CONTEXT_BUDGET_MAX
      ? `At most ${CONTEXT_BUDGET_MAX.toLocaleString()} tokens`
      : `At most ${budgetCeiling.value.toLocaleString()} tokens (the model's context window)`;
  }
  return '';
});
const keepError = computed(() => {
  const v = keepRecentRuns.value;
  if (v === '' || !Number.isInteger(v) || v < 0 || v > KEEP_RECENT_RUNS_MAX) {
    return `Between 0 and ${KEEP_RECENT_RUNS_MAX}`;
  }
  return '';
});
const focusError = computed(() =>
  memoryFocus.value.trim().length > MEMORY_FOCUS_MAX_CHARS
    ? `At most ${MEMORY_FOCUS_MAX_CHARS} characters`
    : '',
);

const canSave = computed(
  () =>
    (isOnce.value ? runAtValid.value : cronValid.value) &&
    (!filePathRequired.value || filePath.value.trim() !== '') &&
    !budgetError.value &&
    !keepError.value &&
    !focusError.value,
);

// ── form submission ───────────────────────────────────────────────────────

const saving = ref(false);
const saveError = ref<string | null>(null);

async function handleSubmit() {
  if (!canSave.value) return;
  saving.value = true;
  saveError.value = null;
  try {
    // A "once" trigger sends runAt as UTC ISO 8601 and an empty cron
    // (FR-006: no fabricated cron expression); a "cron" trigger sends
    // the cron expression and no runAt, exactly the pre-WP08 payload
    // shape so existing callers/tests are unaffected.
    const triggerFields = isOnce.value
      ? { cron: '', triggerKind: 'once' as const, runAt: new Date(runAt.value).toISOString() }
      : { cron: cron.value.trim(), triggerKind: 'cron' as const, runAt: undefined };
    const memoryFields = {
      contextBudgetTokens: Number(contextBudget.value),
      keepRecentRuns: Number(keepRecentRuns.value),
      memoryFocus: memoryFocus.value.trim(),
    };
    let entry: ScheduledChatEntry;
    if (isEditMode.value && props.editing) {
      entry = await props.client.update({
        id: props.editing.id,
        name: name.value,
        promptTemplate: promptTemplate.value,
        ...triggerFields,
        timezone: timezone.value || undefined,
        model: model.value || undefined,
        outputSink: sinkValue.value,
        enabled: enabled.value,
        ...memoryFields,
      });
    } else {
      entry = await props.client.create({
        name: name.value,
        promptTemplate: promptTemplate.value,
        ...triggerFields,
        timezone: timezone.value || undefined,
        model: model.value || undefined,
        outputSink: sinkValue.value,
        enabled: enabled.value,
        ...memoryFields,
      });
    }
    emit('saved', entry);
  } catch (err) {
    saveError.value = err instanceof Error ? err.message : String(err);
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <!-- Overlay -->
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/60"
    data-testid="scheduled-chat-form-modal"
    @click.self="emit('cancel')"
  >
    <div class="w-full max-w-lg rounded-sm border border-border-muted bg-surface-0 shadow-xl">
      <!-- Header -->
      <div class="flex items-center justify-between border-b border-border-muted px-5 py-3">
        <h3 class="font-ui text-sm font-medium text-ink" data-testid="modal-title">
          {{ title }}
        </h3>
        <button
          type="button"
          class="font-ui text-xs text-ink-muted hover:text-ink"
          data-testid="modal-close"
          @click="emit('cancel')"
        >
          ✕
        </button>
      </div>

      <!-- Body -->
      <form class="px-5 py-4 space-y-4" @submit.prevent="handleSubmit">
        <!-- Name -->
        <div>
          <label class="block font-ui text-xs text-ink-muted mb-1" for="sc-name">Name</label>
          <input
            id="sc-name"
            v-model="name"
            type="text"
            class="w-full rounded-sm border border-border-muted bg-surface-1 px-3 py-1.5 font-ui text-sm text-ink focus:outline-none focus:ring-1 focus:ring-accent"
            placeholder="Daily EA briefing"
            data-testid="sc-name-input"
          />
        </div>

        <!-- Prompt template -->
        <div>
          <label class="block font-ui text-xs text-ink-muted mb-1" for="sc-prompt">
            Prompt template
          </label>
          <textarea
            id="sc-prompt"
            v-model="promptTemplate"
            rows="4"
            class="w-full rounded-sm border border-border-muted bg-surface-1 px-3 py-1.5 font-ui text-sm text-ink focus:outline-none focus:ring-1 focus:ring-accent resize-none"
            placeholder="Summarize my calendar for {{date}}. Time: {{time}}."
            data-testid="sc-prompt-input"
          ></textarea>
          <p class="mt-1 font-ui text-xs text-ink-muted">
            Variables: <code v-pre class="font-mono">{{date}}</code>,
            <code v-pre class="font-mono">{{time}}</code>,
            <code v-pre class="font-mono">{{cron_expr}}</code>
          </p>
        </div>

        <!-- Trigger kind (model-scheduled-jobs-01PMSJ01 WP08) -->
        <div>
          <label class="block font-ui text-xs text-ink-muted mb-1">When</label>
          <div class="flex items-center gap-4">
            <label class="flex items-center gap-1.5 font-ui text-sm text-ink cursor-pointer">
              <input
                v-model="triggerKind"
                type="radio"
                value="cron"
                data-testid="sc-trigger-cron"
              />
              Recurring (cron)
            </label>
            <label class="flex items-center gap-1.5 font-ui text-sm text-ink cursor-pointer">
              <input
                v-model="triggerKind"
                type="radio"
                value="once"
                data-testid="sc-trigger-once"
              />
              Once
            </label>
          </div>
        </div>

        <!-- Cron + timezone row (triggerKind=cron) -->
        <div v-if="!isOnce" class="grid grid-cols-2 gap-3">
          <div>
            <label class="block font-ui text-xs text-ink-muted mb-1" for="sc-cron">
              Cron expression
            </label>
            <input
              id="sc-cron"
              v-model="cron"
              type="text"
              class="w-full rounded-sm border px-3 py-1.5 font-mono text-sm text-ink focus:outline-none focus:ring-1 focus:ring-accent"
              :class="cronValid ? 'border-border-muted bg-surface-1' : 'border-signal-danger bg-signal-danger-soft'"
              placeholder="0 9 * * *"
              data-testid="sc-cron-input"
            />
            <p v-if="!cronValid" class="mt-1 font-ui text-xs text-signal-danger">
              Enter a valid 5-field cron expression
            </p>
          </div>
          <div>
            <label class="block font-ui text-xs text-ink-muted mb-1" for="sc-tz">
              Timezone (IANA)
            </label>
            <input
              id="sc-tz"
              v-model="timezone"
              type="text"
              class="w-full rounded-sm border border-border-muted bg-surface-1 px-3 py-1.5 font-ui text-sm text-ink focus:outline-none focus:ring-1 focus:ring-accent"
              placeholder="America/New_York"
              data-testid="sc-tz-input"
            />
          </div>
        </div>

        <!-- Run-at (triggerKind=once) -->
        <div v-else>
          <label class="block font-ui text-xs text-ink-muted mb-1" for="sc-run-at">
            Run at
          </label>
          <input
            id="sc-run-at"
            v-model="runAt"
            type="datetime-local"
            class="w-full rounded-sm border px-3 py-1.5 font-ui text-sm text-ink focus:outline-none focus:ring-1 focus:ring-accent"
            :class="runAtValid ? 'border-border-muted bg-surface-1' : 'border-signal-danger bg-signal-danger-soft'"
            data-testid="sc-run-at-input"
          />
          <p v-if="!runAtValid" class="mt-1 font-ui text-xs text-signal-danger">
            Choose a date and time
          </p>
        </div>

        <!-- Model -->
        <div>
          <label class="block font-ui text-xs text-ink-muted mb-1" for="sc-model">
            Target model <span class="text-ink-muted">(optional)</span>
          </label>
          <input
            id="sc-model"
            v-model="model"
            type="text"
            class="w-full rounded-sm border border-border-muted bg-surface-1 px-3 py-1.5 font-ui text-sm text-ink focus:outline-none focus:ring-1 focus:ring-accent"
            placeholder="Leave blank to use the active default"
            data-testid="sc-model-input"
          />
          <p
            v-if="!model && defaultModel !== null"
            class="mt-1 font-ui text-xs text-ink-muted"
            data-testid="sc-model-default-hint"
          >
            <template v-if="defaultModel">
              Active default resolves to <span class="font-mono text-ink">{{ defaultModel }}</span>.
            </template>
            <template v-else>
              No default model is configured — add a provider, or name a model here.
            </template>
          </p>
        </div>

        <!-- Tool set (tool-context-budget-01TCBUD01 WP06, FR-K3) -->
        <fieldset data-testid="sc-tool-set">
          <legend class="block font-ui text-xs text-ink-muted mb-1">Tool set</legend>
          <div class="flex items-center gap-4">
            <label class="flex items-center gap-1.5 font-ui text-sm text-ink cursor-pointer">
              <input type="radio" name="sc-tool-set" checked data-testid="sc-tool-set-default" />
              Your default tiers
            </label>
            <label class="flex items-center gap-1.5 font-ui text-sm text-ink-muted cursor-not-allowed">
              <input
                type="radio"
                name="sc-tool-set"
                disabled
                aria-describedby="sc-tool-set-custom-reason"
                data-testid="sc-tool-set-custom"
              />
              Custom servers
            </label>
          </div>
          <p
            v-if="contained"
            class="mt-1 font-ui text-xs text-ink-muted"
            data-testid="sc-tool-set-contained"
          >
            Contained run — limited to {{ editing?.toolAllowlist?.length }} allowed
            tool{{ editing?.toolAllowlist?.length === 1 ? '' : 's' }}; cost not shown.
          </p>
          <p
            v-else-if="toolSetTokens !== null"
            class="mt-1 font-ui text-xs text-ink-muted"
            data-testid="sc-tool-set-cost"
          >
            Each request sends {{ toolSetFull }} tool definition{{ toolSetFull === 1 ? '' : 's' }}
            ({{ formatTokens(toolSetTokens) }} tokens); the run can load more as it needs them.
          </p>
          <p id="sc-tool-set-custom-reason" class="mt-1 font-ui text-xs text-ink-subtle">
            Choosing servers per schedule is not available yet. Change the default tiers in
            Capabilities.
          </p>
        </fieldset>

        <!-- Conversation memory (scheduled-chat-context-budget) -->
        <fieldset data-testid="sc-memory">
          <legend class="block font-ui text-xs text-ink-muted mb-1">Conversation memory</legend>
          <p class="mb-2 font-ui text-xs text-ink-muted">
            Every run continues one conversation. When it grows past the budget, older runs
            are summarised before the next run starts; the newest runs stay word for word.
          </p>
          <div class="grid grid-cols-2 gap-3">
            <div>
              <label class="block font-ui text-xs text-ink-muted mb-1" for="sc-budget">
                Context budget (tokens)
              </label>
              <input
                id="sc-budget"
                v-model.number="contextBudget"
                type="number"
                :min="CONTEXT_BUDGET_MIN"
                :max="budgetCeiling"
                step="1000"
                class="w-full rounded-sm border px-3 py-1.5 font-mono text-sm text-ink focus:outline-none focus:ring-1 focus:ring-accent"
                :class="budgetError ? 'border-signal-danger bg-signal-danger-soft' : 'border-border-muted bg-surface-1'"
                data-testid="sc-budget-input"
              />
              <p v-if="budgetError" class="mt-1 font-ui text-xs text-signal-danger" data-testid="sc-budget-error">
                {{ budgetError }}
              </p>
            </div>
            <div>
              <label class="block font-ui text-xs text-ink-muted mb-1" for="sc-keep">
                Recent runs kept verbatim
              </label>
              <input
                id="sc-keep"
                v-model.number="keepRecentRuns"
                type="number"
                min="0"
                :max="KEEP_RECENT_RUNS_MAX"
                step="1"
                class="w-full rounded-sm border px-3 py-1.5 font-mono text-sm text-ink focus:outline-none focus:ring-1 focus:ring-accent"
                :class="keepError ? 'border-signal-danger bg-signal-danger-soft' : 'border-border-muted bg-surface-1'"
                data-testid="sc-keep-input"
              />
              <p v-if="keepError" class="mt-1 font-ui text-xs text-signal-danger" data-testid="sc-keep-error">
                {{ keepError }}
              </p>
            </div>
          </div>
          <div class="mt-2">
            <label class="block font-ui text-xs text-ink-muted mb-1" for="sc-memory-focus">
              What to remember <span class="text-ink-muted">(optional)</span>
            </label>
            <textarea
              id="sc-memory-focus"
              v-model="memoryFocus"
              rows="2"
              class="w-full rounded-sm border px-3 py-1.5 font-ui text-sm text-ink focus:outline-none focus:ring-1 focus:ring-accent resize-none"
              :class="focusError ? 'border-signal-danger bg-signal-danger-soft' : 'border-border-muted bg-surface-1'"
              placeholder="e.g. open action items and how each metric changed since the last run"
              data-testid="sc-memory-focus-input"
            ></textarea>
            <p v-if="focusError" class="mt-1 font-ui text-xs text-signal-danger" data-testid="sc-memory-focus-error">
              {{ focusError }}
            </p>
            <p v-else class="mt-1 font-ui text-xs text-ink-subtle">
              Given to the summary of older runs. Summarising costs a model call, shown on the run.
            </p>
          </div>
        </fieldset>

        <!-- Output sink -->
        <div>
          <label class="block font-ui text-xs text-ink-muted mb-1">Output sink</label>
          <div class="flex items-center gap-4">
            <label class="flex items-center gap-1.5 font-ui text-sm text-ink cursor-pointer">
              <input
                v-model="outputSink"
                type="radio"
                value="banner"
                data-testid="sc-sink-banner"
              />
              Banner
            </label>
            <label class="flex items-center gap-1.5 font-ui text-sm text-ink cursor-pointer">
              <input v-model="outputSink" type="radio" value="file" data-testid="sc-sink-file" />
              File
            </label>
            <label class="flex items-center gap-1.5 font-ui text-sm text-ink cursor-pointer">
              <input v-model="outputSink" type="radio" value="none" data-testid="sc-sink-none" />
              None (silent)
            </label>
          </div>

          <!-- File path (conditional) -->
          <div v-if="filePathRequired" class="mt-2">
            <input
              v-model="filePath"
              type="text"
              class="w-full rounded-sm border border-border-muted bg-surface-1 px-3 py-1.5 font-mono text-sm text-ink focus:outline-none focus:ring-1 focus:ring-accent"
              placeholder="/tmp/briefing.md"
              data-testid="sc-file-path-input"
            />
          </div>
        </div>

        <!-- Enabled toggle -->
        <label class="flex items-center gap-2 cursor-pointer">
          <input
            v-model="enabled"
            type="checkbox"
            class="h-4 w-4 rounded"
            data-testid="sc-enabled-checkbox"
          />
          <span class="font-ui text-sm text-ink">Enabled</span>
        </label>

        <!-- Save error -->
        <div
          v-if="saveError"
          class="rounded-sm border border-signal-danger bg-signal-danger-soft px-3 py-2 font-ui text-sm text-signal-danger"
          data-testid="sc-save-error"
        >
          {{ saveError }}
        </div>

        <!-- Footer buttons -->
        <div class="flex justify-end gap-2 pt-2">
          <button
            type="button"
            class="rounded-sm border border-border-muted bg-surface-2 px-4 py-1.5 font-ui text-sm text-ink hover:bg-surface-1"
            data-testid="modal-cancel"
            @click="emit('cancel')"
          >
            Cancel
          </button>
          <button
            type="submit"
            class="rounded-sm bg-accent px-4 py-1.5 font-ui text-sm text-white disabled:opacity-50"
            :disabled="!canSave || saving"
            data-testid="modal-save"
          >
            {{ saving ? 'Saving…' : isEditMode ? 'Save changes' : 'Create' }}
          </button>
        </div>
      </form>
    </div>
  </div>
</template>
