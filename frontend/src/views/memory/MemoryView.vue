<script setup lang="ts">
/**
 * MemoryView — long-term memory management surface (NN/SECTION pattern).
 *
 * Lists every persisted chunk, lets the user prune what they no longer
 * want the harness to remember, and surfaces the empty state when the
 * store is fresh. Privacy: chunks live on disk under
 * <DataDir>/memory.gob; this view is the user's prune-knob for that
 * file.
 */
import { onMounted, ref } from 'vue';
import CanvasHead from '@/shell/CanvasHead.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { MemoryChunk } from '@/lib/types';

const client = useHarnessClient();

const chunks = ref<readonly MemoryChunk[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);

async function refresh() {
  loading.value = true;
  error.value = null;
  try {
    chunks.value = await client.memory.listChunks();
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
    chunks.value = [];
  } finally {
    loading.value = false;
  }
}

async function forget(id: string) {
  try {
    await client.memory.forget(id);
    await refresh();
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
}

function preview(content: string): string {
  if (content.length <= 200) return content;
  return content.slice(0, 200) + '…';
}

function formatTimestamp(iso: string): string {
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
      number="07"
      section="MEMORY"
      title="Long-term memory"
      subtitle="Every snippet you have asked the harness to remember. These are pulled into future conversations across all sessions."
    />

    <div class="px-6 py-4 max-w-4xl">
      <div
        v-if="error"
        class="mb-3 rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
      >
        {{ error }}
      </div>

      <div
        v-if="loading"
        class="font-ui text-[12px] text-ink-muted"
        role="status"
      >
        Loading memories…
      </div>

      <div
        v-else-if="chunks.length === 0"
        class="rounded-md border border-border-muted bg-surface-1 px-4 py-6 text-center"
        data-testid="memory-empty-state"
      >
        <p class="font-ui text-sm text-ink">
          No memories saved yet. Hit the 📌 button on a message to start.
        </p>
      </div>

      <ul v-else class="space-y-2" data-testid="memory-chunk-list">
        <li
          v-for="chunk in chunks"
          :key="chunk.id"
          class="rounded-md border border-border-muted bg-surface-1 px-4 py-3"
          :data-testid="`memory-chunk-${chunk.id}`"
        >
          <div class="flex items-baseline gap-3">
            <span class="font-mono text-[10px] uppercase tracking-[0.18em] text-ink-subtle">
              {{ formatTimestamp(chunk.createdAt) }}
            </span>
            <span
              v-if="chunk.sessionId"
              class="font-mono text-[10px] text-ink-dim"
            >
              session {{ chunk.sessionId }}
            </span>
            <span
              v-if="chunk.sourceTurn"
              class="font-mono text-[10px] text-ink-dim"
            >
              · {{ chunk.sourceTurn }}
            </span>
            <button
              type="button"
              class="ml-auto px-2 py-1 rounded-sm border border-border-muted text-[10px] uppercase tracking-[0.18em] text-ink-dim hover:text-signal-danger hover:bg-surface-2"
              :data-testid="`memory-forget-${chunk.id}`"
              @click="forget(chunk.id)"
            >
              Forget
            </button>
          </div>
          <p class="mt-2 font-ui text-sm text-ink whitespace-pre-wrap">
            {{ preview(chunk.content) }}
          </p>
        </li>
      </ul>
    </div>
  </div>
</template>
