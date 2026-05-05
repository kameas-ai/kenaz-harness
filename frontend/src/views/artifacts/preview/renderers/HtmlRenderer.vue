<script setup lang="ts">
/**
 * HtmlRenderer.vue — thin shell over IframeSandbox for text/html artifacts
 * (artifact-preview-binary-rendering-01KQ8TD5 WP04, FR-006).
 *
 * Decoding is handled by the parent (ArtifactPreview.vue) which passes the
 * decoded HTML as bytesB64. We decode it here from base64 using the standard
 * UTF-8 decode path.
 */

import IframeSandbox from '../IframeSandbox.vue';
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

const html = decodeUtf8(props.bytesB64);
</script>

<template>
  <div data-testid="html-renderer">
    <IframeSandbox :html="html" />
  </div>
</template>
