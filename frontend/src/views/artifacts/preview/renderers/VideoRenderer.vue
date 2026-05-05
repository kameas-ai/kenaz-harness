<script setup lang="ts">
/**
 * VideoRenderer.vue — renders video/* artifacts via HTML5 <video controls>
 * (artifact-preview-binary-rendering-01KQ8TD5 WP02, FR-005).
 *
 * Uses preload="metadata" (risk R-4) to bound memory pressure. No autoplay.
 * Download affordance included.
 */

import { watch } from 'vue';
import type { RendererProps } from '../types';

const props = defineProps<RendererProps>();

function downloadVideo() {
  const a = document.createElement('a');
  a.href = props.sourceUrl;
  a.download = props.artifact.title || 'video-artifact';
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
</script>

<template>
  <div
    class="bg-surface-1 rounded-sm border border-border-muted p-4 space-y-3"
    data-testid="video-renderer"
  >
    <video
      controls
      preload="metadata"
      :src="sourceUrl"
      class="w-full max-h-[70vh] rounded-sm"
      data-testid="video-renderer-video"
    />
    <div class="flex justify-end">
      <button
        type="button"
        class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1 text-[12px] text-ink-muted hover:bg-surface-2 hover:text-ink"
        data-testid="video-renderer-download"
        @click="downloadVideo"
      >
        Download
      </button>
    </div>
  </div>
</template>
