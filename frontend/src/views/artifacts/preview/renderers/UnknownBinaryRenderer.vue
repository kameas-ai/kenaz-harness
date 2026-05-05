<script setup lang="ts">
/**
 * UnknownBinaryRenderer.vue — fallback renderer for unsupported MIME types
 * and for artifacts that exceed the size/time cap (FR-008, FR-009).
 *
 * Shows: file size + MIME type + "Open externally" affordance via
 * artifacts.saveAs (triggers the OS native save-file dialog).
 */

import { useHarnessClient } from '@/lib/harnessClientContext';
import type { RendererProps } from '../types';

interface UnknownBinaryProps extends RendererProps {
  /** Reason for showing this fallback, if the cap was tripped. */
  capReason?: 'size' | 'time' | null;
}

const props = defineProps<UnknownBinaryProps>();

const client = useHarnessClient();

async function openExternally() {
  try {
    const suggested =
      (props.artifact.sourceRef as { filename?: string })?.filename ||
      props.artifact.title ||
      props.artifact.id;
    await client.artifacts.saveAs(props.artifact.id, suggested);
  } catch {
    // best-effort
  }
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MiB`;
}
</script>

<template>
  <div
    class="rounded-sm border border-border-muted bg-surface-1 px-4 py-6 text-center space-y-3"
    data-testid="unknown-binary-renderer"
  >
    <div
      v-if="capReason === 'size'"
      class="text-[12px] text-ink-muted"
      data-testid="unknown-binary-cap-size"
    >
      Preview unavailable for files &gt; 5 MB.
    </div>
    <div
      v-else-if="capReason === 'time'"
      class="text-[12px] text-ink-muted"
      data-testid="unknown-binary-cap-time"
    >
      Preview timed out loading this file.
    </div>
    <div
      v-else
      class="text-[12px] text-ink-muted"
      data-testid="unknown-binary-no-preview"
    >
      Preview not available for this file type.
    </div>

    <div class="font-mono text-[11px] text-ink-dim">
      {{ artifact.mimeType }} · {{ formatBytes(artifact.byteSize) }}
    </div>

    <button
      type="button"
      class="rounded-sm border border-border-muted bg-surface-1 px-3 py-1.5 text-[12px] text-ink-muted hover:bg-surface-2 hover:text-ink"
      data-testid="unknown-binary-open-external"
      @click="openExternally"
    >
      Open with system app
    </button>
  </div>
</template>
