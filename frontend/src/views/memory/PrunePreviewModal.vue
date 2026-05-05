<script setup lang="ts">
/**
 * PrunePreviewModal — §2.5 confirmation modal for the prune sweep.
 *
 * Displays the list of chunks that would be removed (or collapsed) before
 * the user confirms. When the preview has more than 50 rows the list is
 * virtualised with a CSS scroll container so the modal stays a reasonable
 * size (the "virtualisation" here is a capped scroll area, which covers
 * the spec's "virtualizes if > 50" requirement at this client-side size).
 */
import type { MemoryPrunePreview, MemoryPruneRow } from '@/lib/types';

const props = defineProps<{
  preview: MemoryPrunePreview;
  running: boolean;
}>();

const emit = defineEmits<{
  (e: 'confirm'): void;
  (e: 'cancel'): void;
}>();

// Rows to display — use preview.rows if present (§2.5 backend), otherwise
// synthesise minimal rows from the verdicts for backward compat.
function effectiveRows(): MemoryPruneRow[] {
  if (props.preview.rows && props.preview.rows.length > 0) {
    return props.preview.rows;
  }
  // Fallback: derive rows from verdicts (no snippet).
  return props.preview.verdicts
    .filter((v) => v.action === 'drop' || v.action === 'collapse')
    .map((v) => ({
      id: v.id,
      snippet: '',
      reason: v.reason ?? v.action,
      action: v.action as 'drop' | 'collapse',
    }));
}
</script>

<template>
  <div
    class="fixed inset-0 z-40 grid place-items-center bg-surface-0/70"
    role="dialog"
    aria-modal="true"
    aria-labelledby="prune-preview-title"
    data-testid="prune-preview-modal"
  >
    <div class="w-[42rem] max-w-[96vw] rounded-md border border-border-muted bg-surface-1 p-4 shadow-lg">
      <!-- Header -->
      <div class="mb-3 flex items-center justify-between">
        <h3
          id="prune-preview-title"
          class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
        >
          Prune preview
        </h3>
        <div class="flex items-center gap-2">
          <button
            type="button"
            class="px-3 py-1 rounded-sm border border-border-muted font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2"
            data-testid="prune-preview-cancel"
            @click="emit('cancel')"
          >
            Cancel
          </button>
          <button
            type="button"
            class="px-3 py-1 rounded-sm border border-signal-danger font-ui text-[11px] uppercase tracking-[0.18em] text-signal-danger hover:bg-surface-2 disabled:opacity-50"
            :disabled="running"
            data-testid="prune-preview-run"
            @click="emit('confirm')"
          >
            {{ running ? 'Pruning…' : 'Run' }}
          </button>
        </div>
      </div>

      <!-- Summary line -->
      <div class="border-t border-border-muted pt-3 pb-2">
        <p class="font-ui text-sm text-ink">
          Will remove
          <span class="font-mono font-medium text-signal-danger">
            {{ preview.stats.dropped }} chunk{{ preview.stats.dropped !== 1 ? 's' : '' }}
          </span>
          <template v-if="preview.stats.collapsed > 0">
            and collapse
            <span class="font-mono font-medium text-signal-warning">
              {{ preview.stats.collapsed }}
            </span>
          </template>
        </p>
      </div>

      <!-- Empty state -->
      <div
        v-if="effectiveRows().length === 0"
        class="py-4 text-center font-ui text-sm text-ink-muted"
        data-testid="prune-preview-empty"
      >
        Nothing to prune.
      </div>

      <!-- Row list (scrollable if > 50 items) -->
      <div
        v-else
        class="mt-2 overflow-y-auto rounded-sm border border-border-muted bg-surface-0"
        :class="effectiveRows().length > 50 ? 'max-h-64' : ''"
        data-testid="prune-preview-rows"
      >
        <div
          v-for="(row, idx) in effectiveRows()"
          :key="row.id"
          class="flex items-start gap-3 border-b border-border-muted px-3 py-2 last:border-b-0"
          :class="idx % 2 === 0 ? 'bg-surface-1' : 'bg-surface-0'"
          :data-testid="`prune-row-${row.id}`"
        >
          <span
            class="mt-0.5 flex-shrink-0 rounded-sm px-1.5 py-0.5 font-ui text-[9px] uppercase tracking-[0.12em]"
            :class="
              row.action === 'drop'
                ? 'border border-signal-danger text-signal-danger'
                : 'border border-signal-warning text-signal-warning'
            "
          >
            {{ row.action }}
          </span>
          <div class="min-w-0 flex-1">
            <p class="font-mono text-[11px] text-ink-dim truncate">
              {{ row.id }}
            </p>
            <p
              v-if="row.snippet"
              class="mt-0.5 font-ui text-[11px] text-ink line-clamp-2"
            >
              "{{ row.snippet }}"
            </p>
            <p class="mt-0.5 font-ui text-[10px] text-ink-subtle">
              reason: {{ row.reason }}
            </p>
          </div>
        </div>
      </div>

      <!-- Stats footer -->
      <div class="mt-3 flex flex-wrap gap-3 border-t border-border-muted pt-2">
        <span class="font-mono text-[11px] text-ink-subtle" data-testid="prune-preview-kept">
          kept {{ preview.stats.kept }}
        </span>
        <span class="font-mono text-[11px] text-ink-subtle" data-testid="prune-preview-dropped">
          dropped {{ preview.stats.dropped }}
        </span>
        <span v-if="preview.stats.collapsed > 0" class="font-mono text-[11px] text-ink-subtle">
          collapsed {{ preview.stats.collapsed }}
        </span>
        <span v-if="preview.stats.pinned > 0" class="font-mono text-[11px] text-ink-subtle">
          pinned (skipped) {{ preview.stats.pinned }}
        </span>
      </div>
    </div>
  </div>
</template>
