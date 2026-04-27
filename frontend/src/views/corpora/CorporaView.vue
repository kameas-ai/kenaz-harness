<script setup lang="ts">
/**
 * CorporaView — bulk-ingest knowledge corpora landing page (mission
 * agent-kernel-graph; Bundle C WP10/WP11).
 *
 * Lists every corpus, lets the user create / delete corpora, and links
 * each row through to the per-corpus detail view (CorpusDetail).
 * Ingestion happens through IngestModal which the user opens from a
 * row's "Ingest…" button.
 */
import { onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import CanvasHead from '@/shell/CanvasHead.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type {
  Corpus,
  CorpusCreateRequest,
  CorpusScope,
} from '@/lib/types';
import IngestModal from './IngestModal.vue';

const client = useHarnessClient();
const router = useRouter();

const corpora = ref<readonly Corpus[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);

const showCreate = ref(false);
const createName = ref('');
const createScope = ref<CorpusScope>('global');
const createScopeId = ref('');
const createTag = ref('');
const createError = ref<string | null>(null);

const ingestTarget = ref<Corpus | null>(null);

async function refresh() {
  loading.value = true;
  error.value = null;
  try {
    corpora.value = await client.corpus.listCorpora('');
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
    corpora.value = [];
  } finally {
    loading.value = false;
  }
}

function openCreate() {
  createName.value = '';
  createScope.value = 'global';
  createScopeId.value = '';
  createTag.value = '';
  createError.value = null;
  showCreate.value = true;
}

async function submitCreate() {
  createError.value = null;
  if (!createName.value.trim()) {
    createError.value = 'Name is required.';
    return;
  }
  if (createScope.value !== 'global' && !createScopeId.value.trim()) {
    createError.value = 'Scope id required for project / session scopes.';
    return;
  }
  const req: CorpusCreateRequest = {
    name: createName.value.trim(),
    scope: createScope.value,
    scopeId:
      createScope.value === 'global' ? undefined : createScopeId.value.trim(),
    tag: createTag.value.trim() || undefined,
  };
  try {
    await client.corpus.createCorpus(req);
    showCreate.value = false;
    await refresh();
  } catch (err) {
    createError.value = err instanceof Error ? err.message : String(err);
  }
}

async function remove(c: Corpus) {
  if (
    typeof window !== 'undefined' &&
    typeof window.confirm === 'function' &&
    !window.confirm(`Delete corpus "${c.name}"? This drops every chunk.`)
  ) {
    return;
  }
  try {
    await client.corpus.deleteCorpus(c.id);
    await refresh();
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
}

function openDetail(c: Corpus) {
  void router.push({ name: 'corpus-detail', params: { id: c.id } });
}

function openIngest(c: Corpus) {
  ingestTarget.value = c;
}

function closeIngest() {
  ingestTarget.value = null;
}

async function onIngestComplete() {
  ingestTarget.value = null;
  await refresh();
}

function scopeLabel(scope: string): string {
  if (scope === 'global') return 'Global';
  if (scope === 'project') return 'Project';
  return 'Session';
}

function fmt(iso: string): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

onMounted(() => {
  void refresh();
});

defineExpose({ refresh });
</script>

<template>
  <div>
    <CanvasHead
      number="11"
      section="KNOWLEDGE"
      title="Corpora"
      subtitle="Bulk-ingest knowledge sources. Each corpus is walked, hashed, chunked, and embedded for top-K retrieval with provenance."
    >
      <template #trailing>
        <button
          type="button"
          class="rounded-sm border border-accent bg-surface-2 px-3 py-1 font-ui text-[12px] uppercase tracking-[0.18em] text-accent"
          data-testid="corpora-create"
          @click="openCreate"
        >
          New corpus
        </button>
      </template>
    </CanvasHead>

    <div class="px-6 py-4 max-w-4xl">
      <div
        v-if="error"
        class="mb-3 rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
      >
        {{ error }}
      </div>

      <div v-if="loading" class="font-ui text-[12px] text-ink-muted">
        Loading corpora…
      </div>

      <div
        v-else-if="corpora.length === 0"
        class="rounded-md border border-border-muted bg-surface-1 px-4 py-6 font-ui text-[13px] text-ink-muted"
        data-testid="corpora-empty"
      >
        No corpora yet. Click <em>New corpus</em> to create one.
      </div>

      <ul v-else class="space-y-2" data-testid="corpora-list">
        <li
          v-for="c in corpora"
          :key="c.id"
          class="rounded-md border border-border-muted bg-surface-1 px-4 py-3"
          :data-testid="`corpus-row-${c.id}`"
        >
          <div class="flex items-baseline justify-between gap-3">
            <div>
              <div class="flex items-center gap-2">
                <button
                  type="button"
                  class="font-ui text-[14px] font-semibold text-ink hover:underline"
                  @click="openDetail(c)"
                >
                  {{ c.name }}
                </button>
                <span
                  class="rounded-sm border border-border-muted px-2 py-0 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim"
                >
                  {{ scopeLabel(c.scope) }}
                </span>
                <span
                  v-if="c.tag"
                  class="rounded-sm border border-border-muted px-2 py-0 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim"
                >
                  {{ c.tag }}
                </span>
              </div>
              <div
                class="mt-1 font-ui text-[11px] text-ink-muted"
                :data-testid="`corpus-updated-${c.id}`"
              >
                Updated {{ fmt(c.updatedAt) }}
              </div>
            </div>
            <div class="flex shrink-0 items-center gap-2">
              <button
                type="button"
                class="rounded-sm border border-border-muted px-2 py-1 font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2"
                :data-testid="`corpus-ingest-${c.id}`"
                @click="openIngest(c)"
              >
                Ingest…
              </button>
              <button
                type="button"
                class="rounded-sm border border-border-muted px-2 py-1 font-ui text-[11px] uppercase tracking-[0.18em] text-signal-danger hover:bg-surface-2"
                :data-testid="`corpus-delete-${c.id}`"
                @click="remove(c)"
              >
                Delete
              </button>
            </div>
          </div>
        </li>
      </ul>
    </div>

    <div
      v-if="showCreate"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
      data-testid="corpora-create-modal"
      @click.self="showCreate = false"
    >
      <div
        class="w-[420px] rounded-md border border-border-muted bg-surface-1 p-4"
      >
        <h2 class="mb-3 font-ui text-[14px] font-semibold text-ink">
          New corpus
        </h2>
        <label class="mb-2 block">
          <span
            class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim"
            >Name</span
          >
          <input
            v-model="createName"
            type="text"
            data-testid="corpora-create-name"
            class="mt-1 w-full rounded-sm border border-border-muted bg-surface-0 px-2 py-1 font-ui text-[13px] text-ink"
          />
        </label>
        <label class="mb-2 block">
          <span
            class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim"
            >Scope</span
          >
          <select
            v-model="createScope"
            class="mt-1 w-full rounded-sm border border-border-muted bg-surface-0 px-2 py-1 font-ui text-[13px] text-ink"
            data-testid="corpora-create-scope"
          >
            <option value="global">Global</option>
            <option value="project">Project</option>
            <option value="session">Session</option>
          </select>
        </label>
        <label v-if="createScope !== 'global'" class="mb-2 block">
          <span
            class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim"
            >Scope id</span
          >
          <input
            v-model="createScopeId"
            type="text"
            data-testid="corpora-create-scope-id"
            class="mt-1 w-full rounded-sm border border-border-muted bg-surface-0 px-2 py-1 font-ui text-[13px] text-ink"
          />
        </label>
        <label class="mb-2 block">
          <span
            class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim"
            >Tag (optional)</span
          >
          <input
            v-model="createTag"
            type="text"
            class="mt-1 w-full rounded-sm border border-border-muted bg-surface-0 px-2 py-1 font-ui text-[13px] text-ink"
          />
        </label>
        <div
          v-if="createError"
          class="mt-2 font-ui text-[12px] text-signal-danger"
          role="alert"
        >
          {{ createError }}
        </div>
        <div class="mt-4 flex justify-end gap-2">
          <button
            type="button"
            class="rounded-sm border border-border-muted px-3 py-1 font-ui text-[12px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2"
            @click="showCreate = false"
          >
            Cancel
          </button>
          <button
            type="button"
            class="rounded-sm border border-accent bg-surface-2 px-3 py-1 font-ui text-[12px] uppercase tracking-[0.18em] text-accent"
            data-testid="corpora-create-submit"
            @click="submitCreate"
          >
            Create
          </button>
        </div>
      </div>
    </div>

    <IngestModal
      v-if="ingestTarget"
      :corpus="ingestTarget"
      @close="closeIngest"
      @complete="onIngestComplete"
    />
  </div>
</template>
