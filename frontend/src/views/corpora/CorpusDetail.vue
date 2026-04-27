<script setup lang="ts">
/**
 * CorpusDetail — per-corpus file list + chunk preview pane.
 *
 * Two-column layout: file list on the left, selected file's chunks on
 * the right. The chunk preview is a read-only panel; ingestion is
 * driven from CorporaView via IngestModal.
 */
import { computed, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import CanvasHead from '@/shell/CanvasHead.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { Corpus, CorpusChunk, CorpusFile } from '@/lib/types';

const client = useHarnessClient();
const route = useRoute();
const router = useRouter();

const corpus = ref<Corpus | null>(null);
const files = ref<readonly CorpusFile[]>([]);
const selectedFileId = ref<string | null>(null);
const chunks = ref<readonly CorpusChunk[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);

const corpusId = computed(() => {
  const v = route.params.id;
  return Array.isArray(v) ? v[0] : v;
});

async function loadCorpus() {
  if (!corpusId.value) return;
  try {
    corpus.value = await client.corpus.getCorpus(corpusId.value);
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
    corpus.value = null;
  }
}

async function loadFiles() {
  if (!corpusId.value) return;
  loading.value = true;
  error.value = null;
  try {
    files.value = await client.corpus.listFiles(corpusId.value);
    if (files.value.length > 0 && !selectedFileId.value) {
      selectedFileId.value = files.value[0].id;
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
    files.value = [];
  } finally {
    loading.value = false;
  }
}

async function loadChunks(fileId: string) {
  if (!corpusId.value || !fileId) {
    chunks.value = [];
    return;
  }
  try {
    chunks.value = await client.corpus.listChunks(corpusId.value, fileId);
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
    chunks.value = [];
  }
}

function selectFile(id: string) {
  selectedFileId.value = id;
}

function back() {
  void router.push({ name: 'corpora' });
}

function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
  return `${(n / 1024 / 1024).toFixed(1)} MiB`;
}

function previewText(t: string): string {
  if (t.length <= 800) return t;
  return t.slice(0, 800) + '…';
}

watch(selectedFileId, async (id) => {
  if (id) {
    await loadChunks(id);
  } else {
    chunks.value = [];
  }
});

onMounted(async () => {
  await loadCorpus();
  await loadFiles();
});

defineExpose({ loadFiles, loadChunks });
</script>

<template>
  <div>
    <CanvasHead
      number="11"
      section="KNOWLEDGE"
      :title="corpus ? corpus.name : 'Corpus'"
      :subtitle="
        corpus
          ? `${corpus.scope} scope • updated ${new Date(corpus.updatedAt).toLocaleString()}`
          : ''
      "
    >
      <template #trailing>
        <button
          type="button"
          class="rounded-sm border border-border-muted px-2 py-1 font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2"
          @click="back"
        >
          ← All corpora
        </button>
      </template>
    </CanvasHead>

    <div class="px-6 py-4 max-w-6xl">
      <div
        v-if="error"
        class="mb-3 rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
      >
        {{ error }}
      </div>

      <div v-if="loading" class="font-ui text-[12px] text-ink-muted">
        Loading files…
      </div>

      <div
        v-else-if="files.length === 0"
        class="rounded-md border border-border-muted bg-surface-1 px-4 py-6 font-ui text-[13px] text-ink-muted"
        data-testid="corpus-detail-empty"
      >
        No files ingested yet for this corpus.
      </div>

      <div v-else class="grid grid-cols-12 gap-4">
        <ul
          class="col-span-5 max-h-[60vh] overflow-y-auto rounded-md border border-border-muted bg-surface-1"
          data-testid="corpus-files-list"
        >
          <li
            v-for="f in files"
            :key="f.id"
            class="cursor-pointer border-b border-border-muted px-3 py-2 last:border-b-0"
            :class="
              selectedFileId === f.id ? 'bg-surface-2 text-ink' : 'text-ink-muted'
            "
            :data-testid="`corpus-file-${f.id}`"
            @click="selectFile(f.id)"
          >
            <div class="font-ui text-[13px]">{{ f.path }}</div>
            <div class="font-ui text-[11px] text-ink-dim">
              {{ fmtBytes(f.fileSize) }} • {{ f.lineCount }} lines
            </div>
          </li>
        </ul>

        <div class="col-span-7" data-testid="corpus-chunk-pane">
          <div
            v-if="!selectedFileId"
            class="rounded-md border border-border-muted bg-surface-1 px-4 py-6 font-ui text-[13px] text-ink-muted"
          >
            Select a file to preview its chunks.
          </div>
          <div
            v-else-if="chunks.length === 0"
            class="rounded-md border border-border-muted bg-surface-1 px-4 py-6 font-ui text-[13px] text-ink-muted"
          >
            No chunks for this file (empty file or parser produced none).
          </div>
          <div v-else class="space-y-2">
            <div
              v-for="c in chunks"
              :key="c.id"
              class="rounded-md border border-border-muted bg-surface-1 px-3 py-2"
              :data-testid="`corpus-chunk-${c.id}`"
            >
              <div
                class="mb-1 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim"
              >
                {{ c.provenance.filePath }}: lines
                {{ c.provenance.lineStart }}–{{ c.provenance.lineEnd }}
              </div>
              <pre
                class="whitespace-pre-wrap break-words font-mono text-[12px] text-ink"
              >{{ previewText(c.text) }}</pre>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
