<script setup lang="ts">
/**
 * ProvenanceDrawer — full audit chain for one memory chunk.
 *
 * Renders the §2.6 provenance fields returned by Memory_GetChunkProvenance:
 * source turn, hook boundary, kind, scope path, retrieval/citation counts,
 * promotion score, embedder metadata, creation timestamp.
 *
 * Emits:
 *   - close: when the user dismisses the drawer (Escape or backdrop click).
 *
 * memory-inspection-ui-01KX5R8E WP08.
 */
import { onMounted, onUnmounted } from 'vue';
import type { ChunkProvenance } from '@/lib/types';

defineProps<{
  provenance: ChunkProvenance;
  loading?: boolean;
  error?: string | null;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
}>();

function handleKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    emit('close');
  }
}

onMounted(() => {
  document.addEventListener('keydown', handleKeydown);
});

onUnmounted(() => {
  document.removeEventListener('keydown', handleKeydown);
});

function formatTimestamp(iso: string): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

function formatScore(v: number): string {
  return v.toFixed(1);
}
</script>

<template>
  <!-- Backdrop -->
  <div
    class="fixed inset-0 z-40 bg-surface-0/60"
    aria-hidden="true"
    data-testid="provenance-drawer-backdrop"
    @click="emit('close')"
  />

  <!-- Drawer panel -->
  <div
    class="fixed right-0 top-0 bottom-0 z-50 w-[28rem] max-w-[92vw] flex flex-col border-l border-border-muted bg-surface-1 shadow-xl"
    role="dialog"
    aria-modal="true"
    aria-labelledby="provenance-drawer-title"
    data-testid="provenance-drawer"
  >
    <!-- Header -->
    <div class="flex items-center justify-between border-b border-border-muted px-4 py-3">
      <h2
        id="provenance-drawer-title"
        class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        Chunk provenance
      </h2>
      <button
        type="button"
        class="rounded-sm p-1 text-ink-dim hover:text-ink hover:bg-surface-2"
        aria-label="Close provenance drawer"
        data-testid="provenance-drawer-close"
        @click="emit('close')"
      >
        ✕
      </button>
    </div>

    <!-- Loading state -->
    <div
      v-if="loading"
      class="flex-1 flex items-center justify-center font-ui text-[12px] text-ink-muted"
      role="status"
      data-testid="provenance-drawer-loading"
    >
      Loading provenance…
    </div>

    <!-- Error state -->
    <div
      v-else-if="error"
      class="m-4 rounded-sm border border-signal-danger px-3 py-2 font-ui text-[12px] text-signal-danger"
      role="alert"
      data-testid="provenance-drawer-error"
    >
      {{ error }}
    </div>

    <!-- Content -->
    <div
      v-else
      class="flex-1 overflow-y-auto px-4 py-3 space-y-4"
      data-testid="provenance-drawer-content"
    >
      <!-- Chunk ID -->
      <div data-testid="provenance-chunk-id">
        <p class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim mb-0.5">
          Chunk ID
        </p>
        <p class="font-mono text-[11px] text-ink break-all">
          {{ provenance.chunkId }}
        </p>
      </div>

      <!-- Source turn -->
      <div v-if="provenance.sourceTurn" data-testid="provenance-source-turn">
        <p class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim mb-0.5">
          Source turn
        </p>
        <p class="font-mono text-[11px] text-ink">
          {{ provenance.sourceTurn }}
        </p>
      </div>

      <!-- Hook boundary -->
      <div v-if="provenance.hookBoundary" data-testid="provenance-hook-boundary">
        <p class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim mb-0.5">
          Captured by hook
        </p>
        <p class="font-ui text-[12px] text-ink">
          {{ provenance.hookBoundary }}
        </p>
      </div>

      <!-- Kind -->
      <div v-if="provenance.kind" data-testid="provenance-kind">
        <p class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim mb-0.5">
          Kind
        </p>
        <p class="font-ui text-[12px] text-ink">
          {{ provenance.kind }}
        </p>
      </div>

      <!-- Scope path -->
      <div data-testid="provenance-scope-path">
        <p class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim mb-0.5">
          Scope path
        </p>
        <div class="flex flex-wrap items-center gap-1">
          <template
            v-for="(step, i) in provenance.scopePath.split(' → ')"
            :key="step"
          >
            <span class="rounded-sm border border-border-muted px-2 py-0.5 font-ui text-[10px] uppercase tracking-[0.12em] text-ink">
              {{ step }}
            </span>
            <span
              v-if="i < provenance.scopePath.split(' → ').length - 1"
              class="text-ink-dim text-[11px]"
              aria-hidden="true"
            >→</span>
          </template>
        </div>
      </div>

      <!-- Stats row: retrieval / citation / promotion -->
      <div class="grid grid-cols-3 gap-2">
        <div
          class="rounded-sm border border-border-muted bg-surface-0 px-3 py-2 text-center"
          data-testid="provenance-retrieval-count"
        >
          <p class="font-mono text-[16px] font-bold text-ink">
            {{ provenance.retrievalCount }}
          </p>
          <p class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim mt-0.5">
            Retrievals
          </p>
        </div>
        <div
          class="rounded-sm border border-border-muted bg-surface-0 px-3 py-2 text-center"
          data-testid="provenance-citation-count"
        >
          <p class="font-mono text-[16px] font-bold text-ink">
            {{ provenance.citationCount }}
          </p>
          <p class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim mt-0.5">
            Citations
          </p>
        </div>
        <div
          class="rounded-sm border border-border-muted bg-surface-0 px-3 py-2 text-center"
          data-testid="provenance-promotion-score"
        >
          <p class="font-mono text-[16px] font-bold text-ink">
            {{ formatScore(provenance.promotionScore) }}
          </p>
          <p class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim mt-0.5">
            Score
          </p>
        </div>
      </div>

      <!-- Pinned -->
      <div data-testid="provenance-pinned">
        <p class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim mb-0.5">
          Pinned
        </p>
        <p class="font-ui text-[12px]" :class="provenance.pinned ? 'text-accent' : 'text-ink-dim'">
          {{ provenance.pinned ? '★ Yes — immune to prune sweep' : 'No' }}
        </p>
      </div>

      <!-- Embedder info -->
      <div v-if="provenance.embedderKind" data-testid="provenance-embedder">
        <p class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim mb-0.5">
          Embedding
        </p>
        <p class="font-ui text-[12px] text-ink">
          {{ provenance.embedderKind }}
          <template v-if="provenance.embedDimensions">
            · {{ provenance.embedDimensions }}-dim
          </template>
        </p>
      </div>

      <!-- Created at -->
      <div data-testid="provenance-created-at">
        <p class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim mb-0.5">
          Created
        </p>
        <p class="font-mono text-[11px] text-ink">
          {{ formatTimestamp(provenance.createdAt) }}
        </p>
      </div>
    </div>
  </div>
</template>
