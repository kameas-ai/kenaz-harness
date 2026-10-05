<script setup lang="ts">
/**
 * FolderShareDialog — folder-level Share… / Promote… (knowledge-home-01DOGF0E
 * FR-7; owner ruling on D4, 2026-10-05: build the batch dialog).
 *
 * Lists every file under the selected folder (recursive, paths relative to
 * the folder) with a checkbox per entry, defaulting to all eligible ones.
 * Confirm runs a client-side batch over the existing per-entry bindings —
 * `contexts.get` + `contexts.publish` for share, `contexts.promote` for
 * promote — sequentially, with per-entry progress and per-entry failure.
 * One failure never aborts the rest; the summary names every failure.
 *
 * Capability gating is whole-dialog and NOT recomputed here: the parent
 * passes ContextsView's FleetSession-derived `sharingDisabledReason`. When
 * it is non-null every control is disabled and that same sentence shows.
 *
 * Cancel while running is cancel-safe: the in-flight entry finishes, the
 * rest are marked "not started". The dialog cannot be dismissed mid-entry.
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { ContextNode } from '@/lib/types';
import {
  buildBatchEntries,
  contextEntryTitle,
  contextNodeID,
  runBatch,
  staged,
  summarize,
  type BatchEntry,
  type FolderBatchMode,
} from './folderBatch';

const props = defineProps<{
  folder: ContextNode;
  mode: FolderBatchMode;
  /** FleetSession-derived reason from ContextsView; non-null disables everything. */
  disabledReason: string | null;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  /** Fired once a batch has run (whatever its outcome) so the parent can refresh. */
  (e: 'finished'): void;
}>();

const client = useHarnessClient();

/** select → running → finished. */
const phase = ref<'select' | 'running' | 'finished'>('select');
// The entry list is fixed at open time: files added later are deliberately
// out of this batch; files deleted since fail honestly at their read step.
const entries = ref<BatchEntry[]>(buildBatchEntries(props.folder, props.mode));

const stopRequested = ref(false);
const layer = ref<'team' | 'org'>('team');

const folderName = computed(() => props.folder.path.split('/').pop() || 'library root');
const verb = computed(() => (props.mode === 'share' ? 'Share' : 'Promote'));
const doneWord = computed(() => (props.mode === 'share' ? 'shared' : 'promoted'));
const disabled = computed(() => props.disabledReason !== null);
const selectedCount = computed(
  () => entries.value.filter((e) => e.checked && e.ineligible === null).length,
);
const summary = computed(() => summarize(entries.value));
const failures = computed(() => entries.value.filter((e) => e.status === 'failed'));

function toggleAll(on: boolean) {
  for (const e of entries.value) if (e.ineligible === null) e.checked = on;
}

async function shareOne(e: BatchEntry): Promise<string | { skipped: string }> {
  const body = await staged('read failed', () => client.contexts.get(e.path));
  if (!body) return { skipped: 'Empty file — nothing to share.' };
  const res = await staged('publish rejected', () =>
    client.contexts.publish({
      node_id: contextNodeID(e.path),
      layer: layer.value,
      kind: 'guidance',
      title: contextEntryTitle(e.path),
      body,
      version: 1,
    }),
  );
  // effective_layer is the only truth about where it landed (finding #97).
  if (layer.value === 'team' && res.effective_layer === 'org') {
    return 'Published org-wide — team sync is not available yet, so this went to your whole organisation.';
  }
  const where = res.effective_layer === 'org' ? 'your organisation' : 'your team';
  const conflicts = res.conflicts?.length ?? 0;
  return conflicts > 0
    ? `Published to ${where} · ${conflicts} version conflict${conflicts === 1 ? '' : 's'}`
    : `Published to ${where}`;
}

async function promoteOne(e: BatchEntry): Promise<string> {
  const res = await staged('promote failed', () => client.contexts.promote(contextNodeID(e.path)));
  return `Promoted to ${res.new_classification}`;
}

async function confirm() {
  if (disabled.value || phase.value !== 'select' || selectedCount.value === 0) return;
  phase.value = 'running';
  stopRequested.value = false;
  for (const e of entries.value) {
    e.status = 'pending';
    e.message = '';
  }
  await runBatch(entries.value, props.mode === 'share' ? shareOne : promoteOne, () => stopRequested.value);
  phase.value = 'finished';
  emit('finished');
}

/** ESC dismisses only while choosing — never mid-batch (no dismiss mid-entry). */
function onKeydown(ev: KeyboardEvent) {
  if (ev.key === 'Escape' && phase.value === 'select') emit('close');
}
onMounted(() => window.addEventListener('keydown', onKeydown));
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown);
  // Safety net: if the parent unmounts us mid-batch, halt after the
  // in-flight entry rather than leave an invisible batch running.
  if (phase.value === 'running') stopRequested.value = true;
});

function onCancel() {
  if (phase.value === 'running') {
    stopRequested.value = true;
    return;
  }
  emit('close');
}

const statusLabel: Record<BatchEntry['status'], string> = {
  pending: '',
  running: 'Working…',
  done: 'Done',
  failed: 'Failed',
  skipped: 'Skipped',
  not_started: 'Not started',
};
const statusClass: Record<BatchEntry['status'], string> = {
  pending: 'text-ink-subtle',
  running: 'text-accent',
  done: 'text-signal-success',
  failed: 'text-signal-danger',
  skipped: 'text-signal-warning',
  not_started: 'text-ink-subtle',
};
</script>

<template>
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
    data-testid="folder-share-overlay"
  >
    <div
      class="bg-surface-1 rounded-xl shadow-xl border border-border-muted p-6 max-w-lg w-full mx-4 flex flex-col max-h-[80vh]"
      role="dialog"
      aria-modal="true"
      :aria-label="`${verb} folder ${folderName}`"
      :data-mode="mode"
      :data-disabled="disabled ? 'true' : 'false'"
      data-testid="folder-share-dialog"
    >
      <h2 class="font-ui font-semibold text-sm text-ink mb-2">
        {{ verb }} the files in “{{ folderName }}”?
      </h2>

      <p
        v-if="disabledReason"
        class="font-ui text-[12px] text-ink-muted leading-relaxed mb-3"
        data-testid="folder-share-disabled-reason"
      >
        {{ disabledReason }}
        <a
          href="#/settings?tab=account"
          class="text-accent hover:text-accent-muted underline"
        >Settings › Account</a>
      </p>
      <template v-else>
        <p
          v-if="mode === 'share'"
          class="font-ui text-[12px] text-ink-muted leading-relaxed mb-3"
        >
          Each checked file is published as its own entry. Do not share credentials, private
          keys, or sensitive personal information in shared layers.
        </p>
        <p
          v-else
          class="font-ui text-[12px] text-ink-muted leading-relaxed mb-3"
        >
          Promote moves entries already shared with your team to org-wide. A file that was never
          shared fails with the server's reason; the other files still promote.
        </p>
      </template>

      <fieldset
        v-if="mode === 'share'"
        class="flex flex-col gap-1 mb-3"
        :disabled="disabled || phase !== 'select'"
        data-testid="folder-share-layer-choice"
      >
        <legend class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle mb-1">
          Visibility
        </legend>
        <label class="flex items-center gap-2 font-ui text-[12px] text-ink cursor-pointer">
          <input v-model="layer" type="radio" value="team" data-testid="folder-share-layer-team" />
          <span>Share to team</span>
        </label>
        <label class="flex items-center gap-2 font-ui text-[12px] text-ink cursor-pointer">
          <input v-model="layer" type="radio" value="org" data-testid="folder-share-layer-org" />
          <span>Publish org-wide (visible to everyone in your organisation)</span>
        </label>
      </fieldset>

      <div class="flex items-center gap-3 mb-1 font-ui text-[11px] text-ink-muted">
        <span class="flex-1">{{ entries.length }} file{{ entries.length === 1 ? '' : 's' }} · {{ selectedCount }} selected</span>
        <button
          type="button"
          class="hover:text-accent disabled:opacity-50"
          :disabled="disabled || phase !== 'select'"
          data-testid="folder-share-select-all"
          @click="toggleAll(true)"
        >All</button>
        <button
          type="button"
          class="hover:text-accent disabled:opacity-50"
          :disabled="disabled || phase !== 'select'"
          data-testid="folder-share-select-none"
          @click="toggleAll(false)"
        >None</button>
      </div>

      <p
        v-if="entries.length === 0"
        class="font-ui text-[12px] text-ink-muted py-2"
        data-testid="folder-share-empty"
      >
        This folder has no files to {{ mode }}.
      </p>
      <ul
        v-else
        class="flex-1 min-h-0 overflow-y-auto border border-border-muted rounded-md divide-y divide-border-muted"
        data-testid="folder-share-entries"
      >
        <li
          v-for="e in entries"
          :key="e.path"
          class="px-2 py-1.5 font-ui text-[12px]"
          :data-status="e.status"
          :data-testid="`folder-share-entry-${e.path}`"
        >
          <label class="flex items-center gap-2">
            <input
              v-model="e.checked"
              type="checkbox"
              class="accent-accent"
              :disabled="disabled || phase !== 'select' || e.ineligible !== null"
              :data-testid="`folder-share-check-${e.path}`"
            />
            <span class="font-mono text-ink truncate flex-1">{{ e.relPath }}</span>
            <span
              v-if="statusLabel[e.status]"
              :class="statusClass[e.status]"
              class="text-[11px] shrink-0"
            >{{ statusLabel[e.status] }}</span>
          </label>
          <p
            v-if="e.ineligible"
            class="pl-6 text-[11px] text-ink-subtle"
            :data-testid="`folder-share-ineligible-${e.path}`"
          >
            {{ e.ineligible }}
          </p>
          <p
            v-else-if="e.message"
            class="pl-6 text-[11px]"
            :class="statusClass[e.status]"
            :data-testid="`folder-share-message-${e.path}`"
          >
            {{ e.message }}
          </p>
        </li>
      </ul>

      <div
        v-if="phase === 'finished'"
        class="mt-3 font-ui text-[12px]"
        role="status"
        data-testid="folder-share-summary"
      >
        <p :class="summary.failed > 0 ? 'text-signal-warning' : 'text-signal-success'">
          {{ summary.done }} {{ doneWord }}, {{ summary.failed }} failed<template v-if="summary.skipped > 0">, {{ summary.skipped }} skipped</template><template v-if="summary.notStarted > 0">, {{ summary.notStarted }} not started (cancelled)</template>.
        </p>
        <ul v-if="failures.length > 0" class="mt-1 text-signal-danger text-[11px]">
          <li v-for="f in failures" :key="f.path">
            <span class="font-mono">{{ f.relPath }}</span> — {{ f.message }}
          </li>
        </ul>
      </div>

      <div class="flex justify-end gap-3 mt-4">
        <button
          type="button"
          class="font-ui text-[12px] text-ink-dim hover:text-ink px-3 py-1.5 rounded-md border border-border-muted disabled:opacity-50"
          :disabled="phase === 'running' && stopRequested"
          data-testid="folder-share-cancel"
          @click="onCancel"
        >
          <template v-if="phase === 'running'">{{ stopRequested ? 'Stopping after this file…' : 'Stop' }}</template>
          <template v-else-if="phase === 'finished'">Close</template>
          <template v-else>Cancel</template>
        </button>
        <button
          v-if="phase !== 'finished'"
          type="button"
          class="font-ui text-[12px] text-accent hover:text-accent-muted px-3 py-1.5 rounded-md bg-accent/10 hover:bg-accent/20 disabled:opacity-50"
          :disabled="disabled || phase !== 'select' || selectedCount === 0"
          data-testid="folder-share-confirm"
          @click="confirm"
        >
          <template v-if="phase === 'running'">{{ verb === 'Share' ? 'Sharing' : 'Promoting' }}…</template>
          <template v-else>{{ verb }} {{ selectedCount }} file{{ selectedCount === 1 ? '' : 's' }}</template>
        </button>
      </div>
    </div>
  </div>
</template>
