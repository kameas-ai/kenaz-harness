<script setup lang="ts">
/**
 * DocumentChip — renders a document content block (PDF, etc.) as a
 * filename + size chip with a "view" affordance. Clicking the chip
 * downloads the bytes via a transient blob URL (v1 — a future polish
 * pass may add an inline PDF viewer).
 *
 * No raw color literals; no v-html.
 */

import { computed } from 'vue';
import { FileText, Download } from 'lucide-vue-next';
import type { MediaSource } from '@/lib/types';

const props = defineProps<{
  source: MediaSource;
  /** Page count from WP06 metadata extraction; shown when > 0. */
  pageCount?: number;
}>();

const filename = computed(() => props.source.original_name ?? 'document');

const sizeLabel = computed(() => {
  // Prefer the authoritative byte size from the backend (WP04 / FR-003).
  if (props.source.size_bytes && props.source.size_bytes > 0) {
    const n = props.source.size_bytes;
    if (n < 1024) return `${n} B`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
    return `${(n / 1024 / 1024).toFixed(1)} MB`;
  }
  // Fallback: approximate from base64 length.
  const data = props.source.data ?? '';
  if (!data) return '';
  const approxBytes = Math.floor((data.length * 3) / 4);
  if (approxBytes < 1024) return `${approxBytes} B`;
  if (approxBytes < 1024 * 1024) return `${(approxBytes / 1024).toFixed(1)} KB`;
  return `${(approxBytes / 1024 / 1024).toFixed(1)} MB`;
});

const pageLabel = computed(() => {
  const n = props.pageCount ?? 0;
  if (n <= 0) return '';
  return n === 1 ? '1 page' : `${n} pages`;
});

function download() {
  const data = props.source.data;
  if (!data) return;
  const mt = props.source.media_type || 'application/octet-stream';
  const binary = atob(data);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  const blob = new Blob([bytes], { type: mt });
  const url = URL.createObjectURL(blob);
  try {
    const a = document.createElement('a');
    a.href = url;
    a.download = filename.value;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
  } finally {
    // Defer revoke so the browser actually triggers the download.
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
}
</script>

<template>
  <span
    class="inline-flex items-center gap-2 px-3 py-1.5 mr-2 mb-2 rounded-md border border-border-muted bg-surface-2 font-ui text-[12px] text-ink align-top"
    data-testid="document-chip"
  >
    <FileText class="w-4 h-4 text-ink-dim" />
    <span class="truncate max-w-[28ch]">{{ filename }}</span>
    <span v-if="sizeLabel" class="text-ink-dim">· {{ sizeLabel }}</span>
    <span v-if="pageLabel" class="text-ink-dim">· {{ pageLabel }}</span>
    <button
      type="button"
      class="ml-1 text-ink-dim hover:text-accent"
      :aria-label="`Download ${filename}`"
      data-testid="document-chip-download"
      @click="download"
    >
      <Download class="w-3.5 h-3.5" />
    </button>
  </span>
</template>
