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
      data-testid="image-block-thumbnail"
      @click="openLightbox"
    >
      <img
        :src="dataURL"
        :alt="filename || 'image'"
        class="block max-w-[240px] max-h-[240px] object-contain bg-surface-2"
      />
    </button>
    <ImageLightbox
      :open="lightboxOpen"
      :src="dataURL"
      :filename="filename"
      @close="closeLightbox"
    />
  </span>
</template>
