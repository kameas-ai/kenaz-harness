<script setup lang="ts">
/**
 * PdfRenderer.vue — renders application/pdf artifacts.
 * (artifact-preview-binary-rendering-01KQ8TD5 WP03, FR-003).
 *
 * Strategy: native <embed type="application/pdf"> first. When the browser
 * does not support embedded PDFs (onerror fires), lazy-imports pdfjs-dist
 * and renders the first page into a <canvas> element (risk R-1: PDF.js
 * chunk is loaded only when needed).
 *
 * The PDF.js lazy chunk is named "pdfjs-vendor" via the dynamic import
 * magic comment so reviewers can identify it in bundle reports.
 */

import { ref, onBeforeUnmount } from 'vue';
import type { RendererProps } from '../types';

const props = defineProps<RendererProps>();

const embedFailed = ref(false);
const canvasRef = ref<HTMLCanvasElement | null>(null);
const pdfJsError = ref<string | null>(null);
const pdfJsLoading = ref(false);

// pdfjs-dist 6 removed PDFDocumentProxy.destroy(); teardown (document +
// worker) now goes through the loading task.
let loadingTask: { destroy(): Promise<void> } | null = null;

async function loadWithPdfJs() {
  pdfJsLoading.value = true;
  pdfJsError.value = null;
  try {
    // Dynamic import so PDF.js is NOT in the main bundle (WP03 acceptance).
    const pdfjsLib = await import(
      /* @vite-ignore */
      'pdfjs-dist'
    );

    // Configure the worker. In test/jsdom environments this path won't
    // resolve; the catch below handles that case gracefully.
    pdfjsLib.GlobalWorkerOptions.workerSrc = new URL(
      'pdfjs-dist/build/pdf.worker.mjs',
      import.meta.url,
    ).href;

    // Security posture (GHSA-hq66-cqwq-w95j / CVE-2026-16633, fixed in
    // pdfjs-dist 6.2.108): artifact PDFs can be attacker-influenced (agent-
    // fetched URLs, tool output, uploads). This renderer deliberately uses
    // ONLY the core API — getDocument + page.render to a <canvas>. It never
    // mounts pdf.js's AnnotationLayer, XfaLayer, PDFViewer or the
    // PDFScriptingManager / pdf.sandbox bridge, which is where that
    // advisory's exploit chain lives. Keep it that way: do not add
    // `enableScripting`, an annotation/XFA layer, or the web/ viewer here
    // without a security review. `enableXfa: false` is the default; it is
    // pinned explicitly so a future default flip cannot turn on the XFA
    // HTML path. (`isEvalSupported` — the CVE-2024-4367 mitigation — no
    // longer exists in pdfjs-dist 6.x; font programs are no longer
    // compiled with eval/new Function, and script-src 'self' forbids eval
    // regardless.)
    const task = pdfjsLib.getDocument({
      data: decodedBytes(),
      enableXfa: false,
    });
    loadingTask = task;
    const pdf = await task.promise;

    const page = await pdf.getPage(1);
    const viewport = page.getViewport({ scale: 1.5 });

    const canvas = canvasRef.value;
    if (!canvas) return;

    canvas.width = viewport.width;
    canvas.height = viewport.height;

    await page.render({ canvas, viewport }).promise;
  } catch (e) {
    pdfJsError.value = e instanceof Error ? e.message : String(e);
    props.onSizeExceeded();
  } finally {
    pdfJsLoading.value = false;
  }
}

function decodedBytes(): Uint8Array {
  const b64 = props.bytesB64;
  if (!b64) return new Uint8Array(0);
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) {
    bytes[i] = bin.charCodeAt(i);
  }
  return bytes;
}

function onEmbedError() {
  embedFailed.value = true;
  void loadWithPdfJs();
}

onBeforeUnmount(() => {
  if (loadingTask) {
    loadingTask.destroy().catch(() => {
      // ignore — best-effort teardown on unmount
    });
    loadingTask = null;
  }
});
</script>

<template>
  <div
    class="bg-surface-1 rounded-sm border border-border-muted overflow-auto"
    data-testid="pdf-renderer"
  >
    <!-- Native embed — works in WebKit/CEF builds -->
    <embed
      v-if="!embedFailed"
      type="application/pdf"
      :src="sourceUrl"
      class="w-full h-[70vh]"
      data-testid="pdf-renderer-embed"
      @error="onEmbedError"
    />

    <!-- PDF.js fallback -->
    <div v-else class="p-4">
      <div
        v-if="pdfJsLoading"
        class="text-center text-[12px] text-ink-muted"
        data-testid="pdf-renderer-loading"
      >
        Loading PDF…
      </div>
      <div
        v-else-if="pdfJsError"
        class="text-center text-[12px] text-signal-danger"
        data-testid="pdf-renderer-error"
      >
        Could not render PDF: {{ pdfJsError }}
      </div>
      <!--
        v-show, not v-else: loadWithPdfJs() reads canvasRef while
        pdfJsLoading is still true, so the canvas must already be in the DOM.
        With v-else it was unmounted during loading, canvasRef was null, and
        the fallback silently rendered nothing.
      -->
      <canvas
        v-show="!pdfJsLoading && !pdfJsError"
        ref="canvasRef"
        class="block mx-auto max-w-full"
        data-testid="pdf-renderer-canvas"
      />
    </div>
  </div>
</template>
