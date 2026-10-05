<script setup lang="ts">
/**
 * ScheduledChatFormModal — create / edit modal for a scheduled chat run.
 *
 * Mission: scheduled-chat-runs-01KX5R8B (WP05).
 *
 * Emits "saved" with the new/updated ChatRunEntry on success.
 * Emits "cancel" when the user dismisses without saving.
 */
import { ref, computed, watch } from 'vue';
import type {
  ScheduledChatClient,
  ScheduledChatEntry,
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

const canSave = computed(
  () =>
    (isOnce.value ? runAtValid.value : cronValid.value) &&
    (!filePathRequired.value || filePath.value.trim() !== ''),
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
        </div>

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
