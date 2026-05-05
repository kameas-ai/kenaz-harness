<script setup lang="ts">
/**
 * MarkdownIframedRenderer.vue — markdown-with-HTML path via IframeSandbox
 * (artifact-preview-binary-rendering-01KQ8TD5 WP05, FR-007).
 *
 * Used when the markdown source contains embedded HTML tags.
 * Compiles markdown → HTML (via markdownToHtml, which includes DOMPurify
 * defence-in-depth), then hands the result to IframeSandbox with the FR-006
 * CSP injected.
 *
 * "Open in browser" is wired (FR-007c): opens the compiled HTML via the OS
 * browser. For now we surface the option only if shell.openInOSBrowser is
 * callable; without a materializePreview RPC we cannot provide a file path,
 * so we use a data: URL fallback via a Blob object URL.
 */

import { computed, onBeforeUnmount } from 'vue';
import IframeSandbox from '../IframeSandbox.vue';
import { markdownToHtml } from '../markdownToHtml';
import { composeIframeDoc } from '../composeIframeDoc';
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

const compiledHtml = computed(() => markdownToHtml(decodeUtf8(props.bytesB64)));

// Create a Blob object URL for the "Open in browser" path.
// The blob contains the fully composed iframe document so the OS browser
// renders it with the same CSP treatment (the OS browser ignores the srcdoc
// CSP but the user is knowingly opening it).
let blobUrl: string | null = null;

const openExternallyPath = computed(() => {
  if (typeof URL === 'undefined' || typeof Blob === 'undefined') return undefined;
  // Revoke previous blob if HTML changed.
  if (blobUrl) {
    URL.revokeObjectURL(blobUrl);
    blobUrl = null;
  }
  const composed = composeIframeDoc(compiledHtml.value);
  const blob = new Blob([composed], { type: 'text/html' });
  blobUrl = URL.createObjectURL(blob);
  return blobUrl;
});

onBeforeUnmount(() => {
  if (blobUrl) {
    URL.revokeObjectURL(blobUrl);
    blobUrl = null;
  }
});
</script>

<template>
  <div data-testid="markdown-iframed-renderer">
    <IframeSandbox
      :html="compiledHtml"
      :open-externally-path="openExternallyPath"
    />
  </div>
</template>
