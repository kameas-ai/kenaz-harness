<script setup lang="ts">
/**
 * ImageLightbox — full-screen overlay rendering a multimodal image
 * block at native size (or fit-to-screen if larger).
 *
 *   - Esc closes.
 *   - Click outside the image closes; click ON the image is a no-op.
 *   - Header: filename + close button.
 *   - No allow-scripts; pure `<img>` rendering of a base64 / uri source.
 *
 * Mirrors the home-grown modal pattern from RecipeKeyPromptModal.vue:
 * fixed-position overlay + centred panel, no radix-vue, no teleport.
 *
 * Privacy CI invariant #4: no raw color literals — every colour flows
 * through the design tokens defined in tokens.css.
 */

import { onMounted, onBeforeUnmount, watch } from 'vue';
import { X } from 'lucide-vue-next';

const props = defineProps<{
  open: boolean;
  /** data:<media_type>;base64,<data>  OR an http(s)/file URI. */
  src: string;
  /** Display label in the header. Falls back to "image" when empty. */
  filename?: string;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
}>();

function close() {
  emit('close');
}

function onKeydown(ev: KeyboardEvent) {
  if (ev.key === 'Escape') {
    ev.preventDefault();
    close();
  }
}

function onOverlayClick(ev: MouseEvent) {
  // Only the overlay closes; clicks on the image itself are absorbed
  // by the inner element's @click.stop.
  if (ev.target === ev.currentTarget) close();
}

function attachKeydown() {
  window.addEventListener('keydown', onKeydown);
}

function detachKeydown() {
  window.removeEventListener('keydown', onKeydown);
}

onMounted(() => {
  if (props.open) attachKeydown();
});
onBeforeUnmount(() => {
  detachKeydown();
});

watch(
  () => props.open,
  (next) => {
    if (next) attachKeydown();
    else detachKeydown();
  },
);
</script>

<template>
  <div
    v-if="open"
    class="fixed inset-0 z-50 flex items-center justify-center bg-modal-overlay"
    role="dialog"
    aria-modal="true"
    :aria-label="filename ? `Image preview: ${filename}` : 'Image preview'"
    data-testid="image-lightbox"
    @click="onOverlayClick"
  >
    <div
      class="relative max-w-[92vw] max-h-[92vh] flex flex-col"
      @click.stop
    >
      <header
        class="flex items-center justify-between px-3 py-2 border-b border-border-muted bg-surface-1 rounded-t-md"
      >
        <span
          class="font-ui text-[12px] text-ink truncate max-w-[60ch]"
          data-testid="image-lightbox-filename"
        >
          {{ filename || 'image' }}
        </span>
        <button
          type="button"
          class="text-ink-dim hover:text-ink"
          aria-label="Close image preview"
          data-testid="image-lightbox-close"
          @click="close"
        >
          <X class="w-4 h-4" />
        </button>
      </header>
      <div class="bg-surface-0 rounded-b-md overflow-hidden">
        <img
          :src="src"
          :alt="filename || 'image'"
          class="block max-w-[92vw] max-h-[80vh] object-contain"
          data-testid="image-lightbox-img"
        />
      </div>
    </div>
  </div>
</template>
