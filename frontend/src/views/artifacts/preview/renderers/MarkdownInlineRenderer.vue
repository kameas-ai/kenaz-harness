<script setup lang="ts">
/**
 * MarkdownInlineRenderer.vue — plain markdown path via StreamingText
 * (artifact-preview-binary-rendering-01KQ8TD5 WP05, FR-007).
 *
 * Used when the markdown source contains no embedded HTML tags.
 * Delegates to the existing StreamingText (→ MarkdownBlock) pipeline.
 */

import StreamingText from '@/components/chat/StreamingText.vue';
import type { RendererProps } from '../types';

const props = defineProps<RendererProps>();

function decodeUtf8(b64: string): string {
  if (!b64) return '';
  try {
    return decodeURIComponent(escape(atob(b64)));
  } catch {
    try {
      return atob(b64);
    } catch {
      return '';
    }
  }
}

const text = decodeUtf8(props.bytesB64);
</script>

<template>
  <div
    class="prose-fit"
    data-testid="markdown-inline-renderer"
  >
    <StreamingText :text="text" />
  </div>
</template>
