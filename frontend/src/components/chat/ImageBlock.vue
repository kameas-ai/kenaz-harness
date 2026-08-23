<script setup lang="ts">
/**
 * ImageBlock — renders a single image content block as a thumbnail.
 *
 * The thumbnail clicks open ImageLightbox at native (or fit-to-screen)
 * size. Pure `<img>` rendering — no v-html, no allow-scripts (privacy
 * CI invariant). The base64 / uri payload comes straight from the
 * MediaSource on the wire; we never inspect or transform it.
 */

import { computed, ref } from 'vue';
import type { MediaSource } from '@/lib/types';
import ImageLightbox from './ImageLightbox.vue';

const props = defineProps<{
  source: MediaSource;
}>();

const lightboxOpen = ref(false);

const dataURL = computed(() => {
  if (props.source.kind === 'uri' && props.source.uri) {
    return props.source.uri;
  }
  const mt = props.source.media_type || 'image/png';
  const data = props.source.data ?? '';
  return `data:${mt};base64,${data}`;
});

const filename = computed(() => props.source.original_name ?? '');

/**
 * Tooltip showing pixel dimensions, e.g. "1920×1080 px".
 *
 * NARROW (controls-and-readouts-that-tell-the-truth-01PMZ808 WP20 /
 * UNIT-15, spec §1.15): `image_dimensions` is never populated on the
 * production path -- ChatInput.vue builds every image MediaSource for a
 * freshly-staged attachment without it -- so this degrades gracefully to
 * an empty (not-shown) tooltip rather than lying, but it also never
 * renders anything today. Populating it needs no core/llm change (the
 * field already exists on the wire type); it needs the frontend staging
 * path to measure the bitmap before send. Owner: alec. Date: 2026-08-23.
 */
const dimensionTooltip = computed(() => {
  const dims = props.source.image_dimensions;
  if (!dims || !dims.width || !dims.height) return '';
  return `${dims.width}×${dims.height} px`;
});

function openLightbox() {
  lightboxOpen.value = true;
}
function closeLightbox() {
  lightboxOpen.value = false;
}
</script>

<template>
  <span class="inline-block mr-2 mb-2 align-top">
    <button
      type="button"
      class="block rounded-md border border-border-muted overflow-hidden hover:border-accent transition-fast"
      :aria-label="filename ? `Open image preview: ${filename}` : 'Open image preview'"
      :title="dimensionTooltip || undefined"
      data-testid="image-block-thumbnail"
      @click="openLightbox"
    >
      <img
        :src="dataURL"
        :alt="filename || 'image'"
        class="block max-w-[240px] max-h-[240px] object-contain bg-surface-2"
      />
    </button>
    <span
      v-if="dimensionTooltip"
      class="block text-[10px] text-ink-dim text-center mt-0.5"
      data-testid="image-block-dims"
    >{{ dimensionTooltip }}</span>
    <ImageLightbox
      :open="lightboxOpen"
      :src="dataURL"
      :filename="filename"
      @close="closeLightbox"
    />
  </span>
</template>
