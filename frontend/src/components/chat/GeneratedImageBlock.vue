<script setup lang="ts">
/**
 * GeneratedImageBlock — renders a model-generated image artifact in the chat
 * bubble as a thumbnail that opens ImageLightbox on click.
 *
 * The component receives an `Artifact` (source = 'model_output') and fetches
 * its bytes lazily via `artifactBytesCache.getArtifactBytes`. The bytes are
 * base64-encoded by Wails (Go []byte → JSON base64 string) and are presented
 * as a `data:` URL directly in an `<img>` tag.
 *
 * Privacy CI invariant #4 (no raw color literals) is upheld — all colours
 * resolve through design tokens.
 *
 * No v-html, no allow-scripts — pure `<img>` rendering consistent with
 * ImageBlock.vue.
 *
 * (multimodal-io-extended-01KQ8TD2 WP06)
 */

import { computed, onMounted, ref } from 'vue';
import type { Artifact } from '@/lib/types';
import { getArtifactBytes } from '@/lib/artifactBytesCache';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import ImageLightbox from './ImageLightbox.vue';

const props = defineProps<{
  /** The artifact descriptor (must have source === 'model_output'). */
  artifact: Artifact;
}>();

const emit = defineEmits<{
  /**
   * Emitted when the user opens the full-resolution lightbox, so the
   * parent (MessageBubble / SessionsView) can also wire promote/delete
   * actions if it chooses to mount ArtifactPreview instead.
   */
  (e: 'open-artifact', artifact: Artifact): void;
}>();

const client = useHarnessClient();

/** base64 string decoded from the IPC payload, or null while loading. */
const bytesBase64 = ref<string | null>(null);
const loadError = ref<string | null>(null);
const lightboxOpen = ref(false);

// ── multimodal-out feature flag ─────────────────────────────────────────
// Default true — assumes enabled until Config_GetFlags returns. When false,
// the component renders nothing (HARNESS_MULTIMODAL_OUT=off kill switch).
// (multimodal-io-extended-01KQ8TD2 WP08)
const multimodalOutEnabled = ref(true);

onMounted(async () => {
  // Load the HARNESS_MULTIMODAL_OUT gate first; if the flag is off, skip
  // the image fetch entirely so we don't hit the artifact store at all.
  try {
    const flags = await client.config.getFlags();
    const outFlag = flags.find((f) => f.name === 'multimodal-out');
    if (outFlag) multimodalOutEnabled.value = outFlag.enabled;
  } catch {
    // Non-fatal: keep default (true = enabled).
  }
  if (!multimodalOutEnabled.value) return;

  try {
    const result = await getArtifactBytes(client, props.artifact.id);
    // `result.bytes` is a base64 string from Wails (Go []byte → JSON base64).
    bytesBase64.value =
      typeof result.bytes === 'string' ? result.bytes : '';
  } catch (err: unknown) {
    loadError.value = err instanceof Error ? err.message : 'Failed to load image';
  }
});

/** data: URL fed to the <img> tag. */
const dataURL = computed<string>(() => {
  if (!bytesBase64.value) return '';
  const mt = props.artifact.mimeType || 'image/png';
  return `data:${mt};base64,${bytesBase64.value}`;
});

const isLoading = computed(() => !bytesBase64.value && !loadError.value);
const titleLabel = computed(() => props.artifact.title || 'generated image');

/**
 * revisedPrompt — surfaced from the sourceRef when the provider rewrote the
 * user's prompt (DALL-E 3 safety augmentation). Shown as a tooltip on the
 * thumbnail so the user can inspect the actual prompt that was used without
 * cluttering the bubble.
 */
const revisedPrompt = computed<string>(
  () => props.artifact.sourceRef?.revisedPrompt ?? '',
);

function openLightbox() {
  if (!dataURL.value) return;
  lightboxOpen.value = true;
  emit('open-artifact', props.artifact);
}

function closeLightbox() {
  lightboxOpen.value = false;
}
</script>

<template>
  <!-- Hidden when HARNESS_MULTIMODAL_OUT=off (multimodal-io-extended-01KQ8TD2 WP08). -->
  <span v-if="multimodalOutEnabled" class="inline-block mr-2 mb-2 align-top">
    <!-- Loading skeleton -->
    <span
      v-if="isLoading"
      class="block w-[120px] h-[120px] rounded-md border border-border-muted bg-surface-2 animate-pulse"
      data-testid="generated-image-block-loading"
      role="status"
      aria-label="Loading generated image"
    />

    <!-- Error state -->
    <span
      v-else-if="loadError"
      class="block rounded-md border border-border-muted bg-surface-2 px-3 py-2 font-ui text-[11px] text-ink-muted"
      data-testid="generated-image-block-error"
    >
      Image unavailable
    </span>

    <!-- Loaded thumbnail -->
    <template v-else>
      <button
        type="button"
        class="block rounded-md border border-border-muted overflow-hidden hover:border-accent transition-fast"
        :aria-label="`Open generated image: ${titleLabel}`"
        :title="revisedPrompt || titleLabel"
        data-testid="generated-image-block-thumbnail"
        @click="openLightbox"
      >
        <img
          :src="dataURL"
          :alt="titleLabel"
          class="block max-w-[240px] max-h-[240px] object-contain bg-surface-2"
        />
      </button>
      <span
        class="block text-[10px] text-ink-dim text-center mt-0.5 max-w-[240px] truncate"
        data-testid="generated-image-block-title"
      >
        {{ titleLabel }}
      </span>
      <ImageLightbox
        :open="lightboxOpen"
        :src="dataURL"
        :filename="titleLabel"
        @close="closeLightbox"
      />
    </template>
  </span>
</template>
