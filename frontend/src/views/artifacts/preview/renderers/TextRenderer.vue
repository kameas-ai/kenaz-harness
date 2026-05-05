<script setup lang="ts">
/**
 * TextRenderer.vue — plain text / code / JSON / YAML renderer via <pre>.
 * Preserves the existing text rendering path (FR-007, text/* branch).
 */

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
  <pre
    class="whitespace-pre-wrap break-words font-mono text-[12px] text-ink bg-surface-1 border border-border-muted rounded-sm p-3 overflow-x-auto"
    data-testid="text-renderer"
  >{{ text }}</pre>
</template>
