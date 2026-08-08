<script setup lang="ts">
/**
 * IframeSandbox.vue — single source of truth for the FR-006 / FR-007c
 * sandboxed iframe path (artifact-preview-binary-rendering-01KQ8TD5 WP04).
 *
 * Renders `html` inside an iframe with:
 *   - sandbox="" — no allow flags at all. Scripts are blocked by the
 *     missing allow-scripts flag; the frame's origin is opaque (no
 *     allow-same-origin), so even if a future change ever added
 *     allow-scripts, script running in the frame could not reach the
 *     parent's storage/cookies via a same-origin context. Belt-and-
 *     braces with the injected CSP (also see FR-006).
 *   - A CSP meta-tag injected via composeIframeDoc() — see FR-006.
 *
 * Corrected 2026-08 (spec 092, Task 1.H-FR6): the previous posture set
 * `allow-same-origin` with no script opt-in. That combination alone never
 * enabled script execution here (allow-scripts was never present), but
 * `allow-same-origin` + `allow-scripts` together is the canonical sandboxed-
 * iframe escape (a script can rewrite its own frame's sandbox attribute via
 * same-origin access to itself). Dropping allow-same-origin removes that
 * latent risk for any future change to this file, at zero cost to rendering
 * — nothing here relies on the frame's origin (everything is inlined /
 * data-URI per FR-006, and 'self' in the CSP can never match an opaque
 * origin anyway).
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
      sandbox=""
      :srcdoc="srcdoc"
      title="Artifact preview"
      data-testid="iframe-sandbox"
    />
  </div>
</template>
