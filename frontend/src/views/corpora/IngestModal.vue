<script setup lang="ts">
/**
 * IngestModal — picks a path + tunables and kicks off a background
 * ingest, then polls the job until completion. Emits `complete` on
 * success and `close` on cancel.
 *
 * Path entry is a free-text input — the Wails 2.x runtime doesn't
 * expose a directory picker (DirectoryPicker.vue under tools/ uses
 * the webkit fallback, but desktop users typically paste an absolute
 * path); we accept either a single file or a directory.
 */
import { onBeforeUnmount, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { Corpus, CorpusIngestStatus } from '@/lib/types';

const props = defineProps<{ corpus: Corpus }>();
const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'complete'): void;
}>();

const client = useHarnessClient();

const path = ref('');
const recursive = ref(true);
const chunkLines = ref<number>(50);
const submitting = ref(false);
const status = ref<CorpusIngestStatus | null>(null);
const error = ref<string | null>(null);

let pollHandle: ReturnType<typeof setTimeout> | null = null;

function clearPoll() {
  if (pollHandle !== null) {
    clearTimeout(pollHandle);
    pollHandle = null;
  }
}

async function pollOnce(jobId: string) {
  try {
    const cur = await client.corpus.jobStatus(jobId);
    status.value = cur;
    if (cur.state === 'completed') {
      emit('complete');
      return;
    }
    if (cur.state === 'failed' || cur.state === 'canceled') {
      error.value = cur.error || `ingest ${cur.state}`;
      submitting.value = false;
      return;
    }
    pollHandle = setTimeout(() => pollOnce(jobId), 250);
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
    submitting.value = false;
  }
}

async function submit() {
  error.value = null;
  if (!path.value.trim()) {
    error.value = 'Path is required.';
    return;
  }
  submitting.value = true;
  status.value = null;
  try {
    const initial = await client.corpus.ingestPath(props.corpus.id, path.value.trim(), {
      recursive: recursive.value,
      chunkLines: chunkLines.value,
    });
    status.value = initial;
    void pollOnce(initial.jobId);
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
    submitting.value = false;
  }
}

function close() {
  clearPoll();
  emit('close');
}

onBeforeUnmount(() => {
  clearPoll();
});

defineExpose({ submit, close });
</script>

<template>
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
    data-testid="corpus-ingest-modal"
    @click.self="close"
  >
    <div
      class="w-[480px] rounded-md border border-border-muted bg-surface-1 p-4"
    >
      <h2 class="mb-3 font-ui text-[14px] font-semibold text-ink">
        Ingest into "{{ corpus.name }}"
      </h2>

      <label class="mb-2 block">
        <span
          class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim"
          >Path (file or directory)</span
        >
        <input
          v-model="path"
          type="text"
          data-testid="corpus-ingest-path"
          placeholder="/absolute/path/to/source"
          class="mt-1 w-full rounded-sm border border-border-muted bg-surface-0 px-2 py-1 font-mono text-[12px] text-ink"
        />
      </label>

      <label class="mb-2 flex items-center gap-2">
        <input
          v-model="recursive"
          type="checkbox"
          data-testid="corpus-ingest-recursive"
        />
        <span class="font-ui text-[12px] text-ink-muted">Walk recursively</span>
      </label>

      <label class="mb-2 block">
        <span
          class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim"
          >Chunk lines</span
        >
        <input
          v-model.number="chunkLines"
          type="number"
          min="1"
          max="500"
          data-testid="corpus-ingest-chunk-lines"
          class="mt-1 w-32 rounded-sm border border-border-muted bg-surface-0 px-2 py-1 font-ui text-[13px] text-ink"
        />
      </label>

      <div
        v-if="status"
        class="mt-3 rounded-md border border-border-muted bg-surface-0 px-3 py-2 font-ui text-[12px] text-ink-muted"
        data-testid="corpus-ingest-status"
      >
        <div>State: <strong>{{ status.state }}</strong></div>
        <div>
          Files: {{ status.filesDone }} / {{ status.filesTotal }} ({{
            status.filesSkip
          }} skipped)
        </div>
        <div>Chunks: {{ status.chunksTotal }}</div>
      </div>

      <div
        v-if="error"
        class="mt-2 font-ui text-[12px] text-signal-danger"
        role="alert"
        data-testid="corpus-ingest-error"
      >
        {{ error }}
      </div>

      <div class="mt-4 flex justify-end gap-2">
        <button
          type="button"
          class="rounded-sm border border-border-muted px-3 py-1 font-ui text-[12px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2"
          @click="close"
        >
          Close
        </button>
        <button
          type="button"
          class="rounded-sm border border-accent bg-surface-2 px-3 py-1 font-ui text-[12px] uppercase tracking-[0.18em] text-accent disabled:opacity-50"
          data-testid="corpus-ingest-submit"
          :disabled="submitting"
          @click="submit"
        >
          {{ submitting ? 'Ingesting…' : 'Start ingest' }}
        </button>
      </div>
    </div>
  </div>
</template>
