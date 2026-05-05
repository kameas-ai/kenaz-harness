<script setup lang="ts">
/**
 * IframeSandbox.vue — single source of truth for the FR-006 / FR-007c
 * sandboxed iframe path (artifact-preview-binary-rendering-01KQ8TD5 WP04).
 *
 * Renders `html` inside an iframe with:
 *   - sandbox="allow-same-origin" (inline CSS works; scripts blocked by
 *     the missing allow-scripts flag AND by the injected CSP).
 *   - A CSP meta-tag injected via composeIframeDoc() — see FR-006.
 *
 * The "Open in browser" button is offered when `openExternallyPath` is
 * provided. It calls shell.openInOSBrowser with that path.
 */

import { computed } from 'vue';
import { composeIframeDoc } from './composeIframeDoc';
import { useHarnessClient } from '@/lib/harnessClientContext';

const props = defineProps<{
  /** Artifact HTML to embed in the sandboxed document. */
  html: string;
  /** When set, an "Open in browser" button is shown. */
  openExternallyPath?: string;
}>();

const client = useHarnessClient();

const srcdoc = computed(() => composeIframeDoc(props.html));

async function openInBrowser() {
  if (!props.openExternallyPath) return;
  try {
    await client.shell.openInOSBrowser(props.openExternallyPath);
  } catch {
    // best-effort; user can also download
  }
}
</script>

<template>
  <div class="space-y-2">
    <div
      v-if="openExternallyPath"
      class="rounded-sm border border-border-muted bg-surface-1 px-3 py-2 text-[11px] text-ink-muted flex items-center justify-between gap-2"
    >
      <span>Scripts and external resources are blocked in the preview.</span>
      <button
        type="button"
        class="rounded-sm border border-accent-hairline bg-surface-1 px-2 py-1 text-[11px] text-accent hover:bg-accent-glow"
        data-testid="iframe-sandbox-open-external"
        @click="openInBrowser"
      >
        Open in browser
      </button>
    </div>
    <iframe
      class="w-full h-[60vh] rounded-sm border border-border-muted bg-surface-1"
      sandbox="allow-same-origin"
      :srcdoc="srcdoc"
      title="Artifact preview"
      data-testid="iframe-sandbox"
    />
  </div>
</template>
