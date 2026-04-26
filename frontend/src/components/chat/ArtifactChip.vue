<script setup lang="ts">
/**
 * ArtifactChip — compact pill rendering one Artifact below a chat
 * message (per-message chip in MessageBubble) or as a row item in the
 * Artifacts tab / global view.
 *
 * Behaviour:
 *   - Source-glyph mapping: code_block → Code, tool_output → Wrench,
 *     user_pin → Pin.
 *   - Title + size (e.g. "tictactoe.html · 3.2 KB").
 *   - Click → emits `open` so the parent can mount an ArtifactPreview
 *     modal (the chip stays presentational; the modal lifecycle is
 *     owned by the parent so it can also wire promote / delete).
 *
 * Privacy CI invariant #4: no raw color literals — every colour
 * resolves through tokens.css.
 */

import { computed } from 'vue';
import { Code, Pin, Wrench } from '@/shell/icons';
import type { Artifact } from '@/lib/types';

const props = defineProps<{
  artifact: Artifact;
}>();

const emit = defineEmits<{
  (e: 'open', artifact: Artifact): void;
}>();

const sourceIcon = computed(() => {
  switch (props.artifact.source) {
    case 'code_block':
      return Code;
    case 'tool_output':
      return Wrench;
    case 'user_pin':
    default:
      return Pin;
  }
});

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const kb = bytes / 1024;
  if (kb < 1024) {
    return kb < 10 ? `${kb.toFixed(1)} KB` : `${Math.round(kb)} KB`;
  }
  const mb = kb / 1024;
  return mb < 10 ? `${mb.toFixed(1)} MB` : `${Math.round(mb)} MB`;
}

const sizeLabel = computed(() => formatSize(props.artifact.byteSize));
const titleLabel = computed(() => props.artifact.title || 'untitled');

function onClick() {
  emit('open', props.artifact);
}
</script>

<template>
  <button
    type="button"
    class="inline-flex items-center gap-1.5 rounded-sm border border-border-muted bg-surface-1 px-2 py-1 font-ui text-[11px] text-ink-muted hover:bg-surface-2 hover:text-ink transition-fast"
    :data-testid="`artifact-chip-${artifact.id}`"
    :aria-label="`Open artifact ${titleLabel}`"
    @click="onClick"
  >
    <component :is="sourceIcon" :size="12" class="text-accent" aria-hidden="true" />
    <span class="font-mono truncate max-w-[28ch]">{{ titleLabel }}</span>
    <span class="text-ink-dim">·</span>
    <span class="font-mono text-ink-dim">{{ sizeLabel }}</span>
  </button>
</template>
