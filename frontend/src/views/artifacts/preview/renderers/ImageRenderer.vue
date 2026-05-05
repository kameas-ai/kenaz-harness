<script setup lang="ts">
/**
 * ImageRenderer.vue — renders image/* artifacts via <img> with an 80vh
 * height cap and click-to-zoom (artifact-preview-binary-rendering-01KQ8TD5
 * WP02, FR-002).
 *
 * On onerror the renderer calls onSizeExceeded() so the parent can swap in
 * the UnknownBinaryRenderer placeholder (FR-009).
 */

import { ref } from 'vue';
import type { RendererProps } from '../types';

const props = defineProps<RendererProps>();

const zoomed = ref(false);

function onError() {
  props.onSizeExceeded();
}

function toggleZoom() {
  zoomed.value = !zoomed.value;
}
</script>

<template>
  <div
    class="bg-surface-1 rounded-sm border border-border-muted overflow-auto flex items-center justify-center"
    data-testid="image-renderer"
  >
    <img
      :src="sourceUrl"
      :alt="artifact.title || 'image artifact'"
      :class="[
        'block mx-auto cursor-zoom-in transition-all',
        zoomed
          ? 'max-w-none max-h-none'
          : 'max-w-full max-h-[80vh]',
      ]"
      data-testid="image-renderer-img"
      @error="onError"
      @click="toggleZoom"
    />
  </div>
</template>
