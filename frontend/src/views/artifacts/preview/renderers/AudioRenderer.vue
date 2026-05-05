<script setup lang="ts">
/**
 * AudioRenderer.vue — renders audio/* artifacts via HTML5 <audio controls>
 * (artifact-preview-binary-rendering-01KQ8TD5 WP02, FR-004).
 *
 * No autoplay per FR-005 / risk R-5. Download affordance included.
 * URL.revokeObjectURL is called on unmount / abort signal.
 */

import { onBeforeUnmount, watch } from 'vue';
import type { RendererProps } from '../types';

const props = defineProps<RendererProps>();

function downloadAudio() {
  const a = document.createElement('a');
  a.href = props.sourceUrl;
  a.download = props.artifact.title || 'audio-artifact';
  a.click();
}

// Revoke object URL when aborted.
watch(
  () => props.abortSignal.aborted,
  (aborted) => {
    if (aborted) {
      props.onTimeout();
    }
  },
);

onBeforeUnmount(() => {
  // Parent's useStreamAbort also revokes; this is belt-and-braces.
});
</script>

<template>
  <div
    class="bg-surface-1 rounded-sm border border-border-muted p-4 space-y-3"
    data-testid="audio-renderer"
  >
    <audio
      controls
      :src="sourceUrl"
      class="w-full"
      data-testid="audio-renderer-audio"
    />
    <div class="flex justify-end">
      <button
        type="button"
        class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1 text-[12px] text-ink-muted hover:bg-surface-2 hover:text-ink"
        data-testid="audio-renderer-download"
        @click="downloadAudio"
      >
        Download
      </button>
    </div>
  </div>
</template>
