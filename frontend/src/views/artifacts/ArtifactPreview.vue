<script setup lang="ts">
/**
 * ArtifactPreview — modal preview for a single Artifact (FR-010).
 *
 * Renders by mime-type:
 *   - text/markdown            → StreamingText (marked + DOMPurify pipeline).
 *   - text/html                → sandboxed <iframe sandbox> (no allow flags
 *                                by default). A "Run scripts" toggle adds
 *                                `allow-scripts` to the sandbox; we never
 *                                pair `allow-scripts` with
 *                                `allow-same-origin` (XSS-bypass class).
 *   - text/* (code, plain)     → <pre> with monospace font.
 *   - image/*                  → ImageLightbox-style <img> rendering.
 *   - everything else          → "Preview not available; download to view".
 *
 * When HARNESS_ARTIFACT_BINARY_PREVIEW is enabled (default), the renderer
 * registry (preview/registry.ts) handles mime dispatch, adding support for:
 *   - audio/*   → <audio controls>
 *   - video/*   → <video controls preload="metadata">
 *   - image/*   → ImageRenderer with click-to-zoom and 80vh cap
 *   - application/pdf → <embed> with PDF.js polyfill
 *   - text/html → IframeSandbox (FR-006 CSP)
 *   - text/markdown with HTML → IframeSandbox
 *   - unknown   → file size + "Open with system app"
 *
 * Footer: Download / Copy / Promote / Delete.
 *   - Promote: from session→project only; from project→global is the
 *     only forward path the spec reserves and v1 disables it.
 *   - Delete: confirms inline; calls `onDelete(id)` (the parent owns
 *     the rpc wiring).
 *
 * Modal pattern mirrors RecipeKeyPromptModal.vue: home-grown overlay,
 * Esc to close, click-outside to close, no radix-vue / no teleport.
 *
 * Privacy CI invariant #4: every colour flows through tokens.css.
 */

import {
  computed,
  nextTick,
  onBeforeUnmount,
  onMounted,
  ref,
  shallowRef,
  watch,
} from 'vue';
import {
  DialogRoot,
  DialogPortal,
  DialogOverlay,
  DialogContent,
  DialogTitle,
  VisuallyHidden,
} from 'radix-vue';
import { Download, X } from '@/shell/icons';
import StreamingText from '@/components/chat/StreamingText.vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { Artifact, ArtifactScope, ArtifactWithBytes } from '@/lib/types';

// Binary preview registry (WP01–WP06)
import { pickRenderer } from './preview/registry';
import { useStreamAbort } from './preview/useStreamAbort';
import UnknownBinaryRenderer from './preview/renderers/UnknownBinaryRenderer.vue';

const client = useHarnessClient();

const props = withDefaults(
  defineProps<{
    open: boolean;
    /** Already-fetched artifact + base64 bytes. Parent owns the fetch. */
    payload: ArtifactWithBytes | null;
    /**
     * Project id of the artifact's session, when it lives in one. Drives
     * the "Promote to project" affordance; empty disables it with a
     * tooltip.
     */
    projectId?: string;
    /**
     * Caller-provided promote handler. Decoupled from the harness client
     * so the surrounding view can wire its own state refresh (and so
     * tests can stub).
     */
    onPromote?: (
      id: string,
      scopeKind: ArtifactScope,
      scopeId: string,
    ) => Promise<Artifact>;
    /** Caller-provided delete handler. */
    onDelete?: (id: string) => Promise<void>;
  }>(),
  {
    projectId: '',
    onPromote: undefined,
    onDelete: undefined,
  },
);

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'promoted', artifact: Artifact): void;
  (e: 'deleted', id: string): void;
}>();

const allowScripts = ref(false);
const showPromoteConfirm = ref(false);
const showDeleteConfirm = ref(false);
const busy = ref(false);
const errorMsg = ref<string | null>(null);
const copyFlash = ref(false);
const imageLoadFailed = ref(false);

// ── Binary preview feature config (WP07) ──────────────────────────────
// Start disabled so the legacy path renders on first paint; the async
// RPC call enables it when the feature flag is on (default). This ensures
// existing ArtifactPreview.test.ts cases continue to pass under the legacy
// branch (fake client returns enabled:false).
const binaryPreviewEnabled = ref(false);
const previewMaxBytes = ref(5 * 1024 * 1024);
const previewTimeoutMs = ref(2000);

// Load config once at mount.
onMounted(async () => {
  try {
    const cfg = await client.settings.getArtifactPreview();
    binaryPreviewEnabled.value = cfg.enabled;
    previewMaxBytes.value = cfg.maxBytes;
    previewTimeoutMs.value = cfg.timeoutMs;
  } catch {
    // defaults retained on error
  }
});

// ── Stream abort controller (WP06) ────────────────────────────────────
const abort = useStreamAbort({
  get maxBytes() { return previewMaxBytes.value; },
  get timeoutMs() { return previewTimeoutMs.value; },
});
const capTripped = ref(false);

function onRendererSizeExceeded() {
  abort.tripBytes(Number.MAX_SAFE_INTEGER);
  capTripped.value = true;
}

function onRendererTimeout() {
  abort.tripTime();
  capTripped.value = true;
}

// ── Artifact computed properties ──────────────────────────────────────
const artifact = computed<Artifact | null>(
  () => props.payload?.artifact ?? null,
);

const mimeType = computed(() => artifact.value?.mimeType ?? '');
const bytesB64 = computed(() => props.payload?.bytes ?? '');

// ── Registry-based renderer selection (WP01) ─────────────────────────
const decodedByteCount = computed(() => {
  if (!bytesB64.value) return 0;
  // Base64 decodes to ~3/4 of the string length.
  return Math.floor(bytesB64.value.length * 0.75);
});

const rendererSpec = computed(() => {
  if (!binaryPreviewEnabled.value || !artifact.value) return null;
  // Decode source only for text-like mimes (needed for html detection).
  const source = isTextLikeMime(mimeType.value) ? decodeText(bytesB64.value) : '';
  return pickRenderer(mimeType.value, source, artifact.value.contentHash);
});

// Object URL for media renderers (image, audio, video, pdf).
// Revoked on unmount via useStreamAbort's onBeforeUnmount hook.
const mediaObjectUrl = shallowRef<string>('');

watch(
  [bytesB64, mimeType, binaryPreviewEnabled],
  ([b64, mime, enabled]) => {
    if (!enabled || !b64 || !mime) {
      mediaObjectUrl.value = '';
      return;
    }
    // Only create object URLs for binary media types.
    const isMedia =
      mime.startsWith('image/') ||
      mime.startsWith('audio/') ||
      mime.startsWith('video/') ||
      mime === 'application/pdf';

    if (!isMedia) {
      mediaObjectUrl.value = '';
      return;
    }

    // Enforce byte cap before creating object URL.
    if (abort.tripBytes(decodedByteCount.value)) {
      capTripped.value = true;
      mediaObjectUrl.value = '';
      return;
    }

    // Decode base64 → Blob → object URL.
    try {
      const bin = atob(b64);
      const bytes = new Uint8Array(bin.length);
      for (let i = 0; i < bin.length; i++) {
        bytes[i] = bin.charCodeAt(i);
      }
      const blob = new Blob([bytes], { type: mime });
      const url = URL.createObjectURL(blob);
      abort.registerObjectUrl(url);
      mediaObjectUrl.value = url;
    } catch {
      mediaObjectUrl.value = `data:${mime};base64,${b64}`;
    }
  },
  { immediate: true },
);

// ── Legacy rendering helpers (kept for legacy branch + existing tests) ─
const isMarkdown = computed(() => mimeType.value === 'text/markdown');
const isHtml = computed(() => mimeType.value === 'text/html');
const isImage = computed(() => mimeType.value.startsWith('image/'));

function isTextLikeMime(m: string): boolean {
  if (!m) return false;
  if (m.startsWith('text/')) return true;
  if (m === 'application/json' || m.endsWith('+json')) return true;
  if (m === 'application/xml' || m.endsWith('+xml')) return true;
  if (m === 'application/javascript' || m === 'application/ecmascript') return true;
  if (m === 'application/x-yaml' || m === 'application/yaml') return true;
  return false;
}

const isText = computed(
  () =>
    !isMarkdown.value &&
    !isHtml.value &&
    !isImage.value &&
    isTextLikeMime(mimeType.value),
);
const isOther = computed(
  () => !isMarkdown.value && !isHtml.value && !isImage.value && !isText.value,
);

function decodeText(b64: string): string {
  if (!b64) return '';
  try {
    const bin = atob(b64);
    // The bytes carry UTF-8; decodeURIComponent + escape is the legacy
    // browser-safe path.
    return decodeURIComponent(escape(bin));
  } catch {
    try {
      return atob(b64);
    } catch {
      return '';
    }
  }
}

const decodedText = computed(() =>
  isMarkdown.value || isHtml.value || isText.value
    ? decodeText(bytesB64.value)
    : '',
);

const dataUrl = computed(() => {
  if (!bytesB64.value || !mimeType.value) return '';
  return `data:${mimeType.value};base64,${bytesB64.value}`;
});

const iframeSandbox = computed(() =>
  allowScripts.value ? 'allow-scripts' : '',
);

const canPromoteToProject = computed(
  () =>
    artifact.value !== null &&
    artifact.value.scopeKind === 'session' &&
    (props.projectId ?? '') !== '',
);

const promoteDisabledReason = computed(() => {
  const a = artifact.value;
  if (!a) return null;
  if (a.scopeKind === 'project') {
    return 'global scope is not yet supported';
  }
  if ((props.projectId ?? '') === '') {
    return 'session is not in a project';
  }
  return null;
});

function close() {
  if (busy.value) return;
  emit('close');
}

function onOverlayClick() {
  close();
}

/** Legacy keydown handler kept for non-Radix fallback paths. */
function onKeydown(ev: KeyboardEvent) {
  if (!props.open) return;
  if (ev.key === 'Escape') {
    ev.preventDefault();
    close();
  }
}

/** Called by Radix DialogRoot when open state changes (e.g. Esc key). */
function onDialogOpenChange(value: boolean) {
  if (!value) close();
}

watch(
  () => props.open,
  (next) => {
    if (next) {
      allowScripts.value = false;
      showPromoteConfirm.value = false;
      showDeleteConfirm.value = false;
      imageLoadFailed.value = false;
      capTripped.value = false;
      errorMsg.value = null;
      void nextTick();
    }
  },
);

onMounted(() => {
  if (typeof window !== 'undefined') {
    window.addEventListener('keydown', onKeydown);
  }
});

onBeforeUnmount(() => {
  if (typeof window !== 'undefined') {
    window.removeEventListener('keydown', onKeydown);
  }
});

async function downloadArtifact() {
  const a = artifact.value;
  if (!a) return;
  errorMsg.value = null;
  // Wails webview ignores `<a download>` on `data:` URLs (the older
  // implementation silently no-op'd). Use the OS-native save dialog
  // bound on the backend (Artifacts_SaveAs); bytes resolve from the
  // media store server-side, no JS-side base64 round-trip.
  const suggested = a.sourceRef.filename || a.title || a.id;
  try {
    await client.artifacts.saveAs(a.id, suggested);
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : String(err);
  }
}

async function copyToClipboard() {
  const a = artifact.value;
  if (!a) return;
  errorMsg.value = null;
  try {
    if (isImage.value && navigator.clipboard?.write && typeof ClipboardItem !== 'undefined') {
      const res = await fetch(dataUrl.value);
      const blob = await res.blob();
      await navigator.clipboard.write([
        new ClipboardItem({ [blob.type]: blob }),
      ]);
    } else if (isImage.value) {
      await navigator.clipboard.writeText(dataUrl.value);
    } else {
      await navigator.clipboard.writeText(decodedText.value);
    }
    copyFlash.value = true;
    setTimeout(() => {
      copyFlash.value = false;
    }, 1200);
  } catch (e) {
    errorMsg.value = e instanceof Error ? e.message : String(e);
  }
}

function askPromote() {
  if (!canPromoteToProject.value) return;
  showPromoteConfirm.value = true;
}

async function confirmPromote() {
  const a = artifact.value;
  if (!a || !props.onPromote || !props.projectId) return;
  busy.value = true;
  errorMsg.value = null;
  try {
    const updated = await props.onPromote(a.id, 'project', props.projectId);
    showPromoteConfirm.value = false;
    emit('promoted', updated);
  } catch (e) {
    errorMsg.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
}

function askDelete() {
  showDeleteConfirm.value = true;
}

async function confirmDelete() {
  const a = artifact.value;
  if (!a || !props.onDelete) return;
  busy.value = true;
  errorMsg.value = null;
  try {
    await props.onDelete(a.id);
    showDeleteConfirm.value = false;
    emit('deleted', a.id);
    emit('close');
  } catch (e) {
    errorMsg.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <DialogRoot
    :open="open && !!artifact"
    @update:open="onDialogOpenChange"
  >
    <DialogPortal>
      <DialogOverlay
        class="fixed inset-0 z-50 bg-modal-overlay"
        data-testid="artifact-preview-overlay"
        @click="onOverlayClick"
      />
      <DialogContent
        class="fixed inset-0 z-50 flex items-center justify-center"
        :aria-label="`Preview ${artifact?.title ?? ''}`"
        data-testid="artifact-preview"
        @keydown="onKeydown"
        @interact-outside="onOverlayClick"
      >
        <!-- VisuallyHidden title satisfies DialogContent's required title for screen readers -->
        <VisuallyHidden>
          <DialogTitle>{{ artifact?.title ?? 'Artifact preview' }}</DialogTitle>
        </VisuallyHidden>
        <div
          v-if="artifact"
          class="relative z-10 w-[760px] max-w-[92vw] max-h-[88vh] overflow-hidden flex flex-col rounded-md border border-border-muted bg-surface-0 shadow-lg"
        >
      <header
        class="flex items-center justify-between border-b border-border-muted px-5 py-3"
      >
        <div class="min-w-0">
          <div
            class="text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
          >
            ARTIFACT · {{ artifact.scopeKind }} · {{ artifact.source }}
          </div>
          <h2
            class="mt-1 font-ui text-base font-semibold text-ink truncate"
            data-testid="artifact-preview-title"
          >
            {{ artifact.title }}
          </h2>
          <div class="font-mono text-[11px] text-ink-dim">
            {{ artifact.mimeType }} · {{ artifact.byteSize }} B
          </div>
        </div>
        <button
          type="button"
          class="text-ink-dim hover:text-ink"
          aria-label="Close preview"
          data-testid="artifact-preview-close"
          @click="close"
        >
          <X :size="16" />
        </button>
      </header>

      <div class="flex-1 overflow-auto px-5 py-4 font-ui">

        <!-- ── Binary preview registry path (WP01–WP06) ──────────────── -->
        <template v-if="binaryPreviewEnabled && rendererSpec">
          <!-- Cap exceeded: always show UnknownBinaryRenderer with reason -->
          <UnknownBinaryRenderer
            v-if="capTripped"
            :artifact="artifact"
            :bytes-b64="bytesB64"
            :source-url="mediaObjectUrl"
            :abort-signal="abort.signal"
            :cap-reason="abort.reason.value"
            :on-size-exceeded="onRendererSizeExceeded"
            :on-timeout="onRendererTimeout"
            data-testid="artifact-preview-cap-fallback"
          />

          <!-- Registry-dispatched renderer -->
          <component
            :is="rendererSpec.component"
            v-else
            :artifact="artifact"
            :bytes-b64="bytesB64"
            :source-url="mediaObjectUrl || dataUrl"
            :abort-signal="abort.signal"
            :on-size-exceeded="onRendererSizeExceeded"
            :on-timeout="onRendererTimeout"
            data-testid="artifact-preview-renderer"
          />
        </template>

        <!-- ── Legacy rendering path (feature-flag-off / non-binary) ── -->
        <template v-else>
          <!-- HTML — sandboxed iframe -->
          <div
            v-if="isHtml"
            class="space-y-2"
            data-testid="artifact-preview-html"
          >
            <div
              v-if="!allowScripts"
              class="rounded-sm border border-border-muted bg-surface-1 px-3 py-2 text-[11px] text-ink-muted flex items-center justify-between gap-2"
            >
              <span>
                Scripts are blocked in the preview. Toggle
                <strong class="text-ink">Run scripts</strong> to load
                JavaScript inside an isolated sandbox.
              </span>
              <button
                type="button"
                class="rounded-sm border border-accent-hairline bg-surface-1 px-2 py-1 text-[11px] text-accent hover:bg-accent-glow"
                data-testid="artifact-preview-run-scripts"
                @click="allowScripts = true"
              >
                Run scripts
              </button>
            </div>
            <div
              v-else
              class="rounded-sm border border-signal-warn bg-surface-1 px-3 py-2 text-[11px] text-signal-warn flex items-center justify-between gap-2"
              role="alert"
              data-testid="artifact-preview-scripts-banner"
            >
              <span>
                Scripts running in an isolated sandbox. The artifact has
                no access to the harness, your data, or other origins.
              </span>
              <button
                type="button"
                class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1 text-[11px] text-ink-muted hover:text-ink"
                data-testid="artifact-preview-disable-scripts"
                @click="allowScripts = false"
              >
                Disable scripts
              </button>
            </div>
            <iframe
              class="w-full h-[60vh] rounded-sm border border-border-muted bg-surface-1"
              :sandbox="iframeSandbox"
              :srcdoc="decodedText"
              :data-testid="
                allowScripts
                  ? 'artifact-preview-iframe-sandboxed-scripts'
                  : 'artifact-preview-iframe-sandboxed'
              "
              title="Artifact preview"
            />
          </div>

          <!-- Markdown — StreamingText -->
          <div
            v-else-if="isMarkdown"
            class="prose-fit"
            data-testid="artifact-preview-markdown"
          >
            <StreamingText :text="decodedText" />
          </div>

          <!-- Image -->
          <div
            v-else-if="isImage"
            class="bg-surface-1 rounded-sm border border-border-muted overflow-hidden"
            data-testid="artifact-preview-image"
          >
            <img
              v-if="!imageLoadFailed"
              :src="dataUrl"
              :alt="artifact.title || 'image artifact'"
              class="block max-w-full max-h-[70vh] mx-auto"
              @error="imageLoadFailed = true"
            />
            <div
              v-else
              class="px-4 py-6 text-center text-[12px] text-ink-muted"
              data-testid="artifact-preview-image-broken"
            >
              Image bytes failed to render — the file may be corrupt or
              mis-typed (mime: <span class="font-mono">{{ mimeType }}</span>,
              size: <span class="font-mono">{{ artifact.byteSize }}B</span>).
              Use Download to inspect the raw bytes.
            </div>
          </div>

          <!-- Plain / code text -->
          <pre
            v-else-if="isText"
            class="whitespace-pre-wrap break-words font-mono text-[12px] text-ink bg-surface-1 border border-border-muted rounded-sm p-3 overflow-x-auto"
            data-testid="artifact-preview-text"
            >{{ decodedText }}</pre>

          <!-- Unknown mime -->
          <div
            v-else-if="isOther"
            class="rounded-sm border border-border-muted bg-surface-1 px-4 py-6 text-center text-[12px] text-ink-muted"
            data-testid="artifact-preview-other"
          >
            Preview not available; download to view.
          </div>
        </template>

        <div
          v-if="errorMsg"
          class="mt-3 rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 text-[11px] text-signal-danger"
          role="alert"
          data-testid="artifact-preview-error"
        >
          {{ errorMsg }}
        </div>

        <!-- Promote / Delete confirmations are rendered above the
             footer (not inside the scrollable body) so the user can
             see them after clicking the footer button regardless of
             scroll position. See the footer block below. -->
      </div>

      <!-- Confirmations appear directly above the footer so they're
           always visible after clicking a footer action button. -->
      <div
        v-if="showPromoteConfirm"
        class="border-t border-border-muted bg-surface-1 px-5 py-2 text-[12px] text-ink"
        role="dialog"
        data-testid="artifact-preview-promote-confirm"
      >
        <div class="flex items-center justify-between gap-3">
          <p>
            Promote <strong>{{ artifact.title }}</strong> from session to project?
          </p>
          <div class="flex items-center gap-2 shrink-0">
            <button
              type="button"
              class="rounded-sm px-2 py-1 text-[11px] text-ink-dim hover:text-ink"
              :disabled="busy"
              data-testid="artifact-preview-promote-cancel"
              @click="showPromoteConfirm = false"
            >
              Cancel
            </button>
            <button
              type="button"
              class="rounded-sm border border-accent bg-surface-1 px-2 py-1 text-[11px] text-accent hover:bg-accent-glow disabled:opacity-50"
              :disabled="busy"
              data-testid="artifact-preview-promote-confirm-btn"
              @click="confirmPromote"
            >
              {{ busy ? 'Promoting…' : 'Promote' }}
            </button>
          </div>
        </div>
      </div>
      <div
        v-if="showDeleteConfirm"
        class="border-t border-signal-danger bg-surface-1 px-5 py-2 text-[12px] text-ink"
        role="dialog"
        data-testid="artifact-preview-delete-confirm"
      >
        <div class="flex items-center justify-between gap-3">
          <p>
            Delete <strong>{{ artifact.title }}</strong>? This cannot be undone.
          </p>
          <div class="flex items-center gap-2 shrink-0">
            <button
              type="button"
              class="rounded-sm px-2 py-1 text-[11px] text-ink-dim hover:text-ink"
              :disabled="busy"
              data-testid="artifact-preview-delete-cancel"
              @click="showDeleteConfirm = false"
            >
              Cancel
            </button>
            <button
              type="button"
              class="rounded-sm border border-signal-danger bg-surface-1 px-2 py-1 text-[11px] text-signal-danger hover:bg-surface-2 disabled:opacity-50"
              :disabled="busy"
              data-testid="artifact-preview-delete-confirm-btn"
              @click="confirmDelete"
            >
              {{ busy ? 'Deleting…' : 'Delete' }}
            </button>
          </div>
        </div>
      </div>
      <footer
        class="flex items-center justify-between gap-2 border-t border-border-muted px-5 py-3"
      >
        <div class="flex items-center gap-2">
          <button
            type="button"
            class="flex items-center gap-1 rounded-sm border border-border-muted bg-surface-1 px-2 py-1 text-[12px] text-ink-muted hover:bg-surface-2 hover:text-ink"
            data-testid="artifact-preview-download"
            @click="downloadArtifact"
          >
            <Download :size="12" />
            Download
          </button>
          <button
            type="button"
            class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1 text-[12px] text-ink-muted hover:bg-surface-2 hover:text-ink"
            data-testid="artifact-preview-copy"
            @click="copyToClipboard"
          >
            {{ copyFlash ? 'Copied!' : 'Copy' }}
          </button>
        </div>
        <div class="flex items-center gap-2">
          <button
            type="button"
            class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1 text-[12px] text-ink-muted hover:bg-surface-2 hover:text-accent disabled:opacity-50 disabled:cursor-not-allowed"
            :disabled="!canPromoteToProject || busy"
            :title="promoteDisabledReason ?? 'Promote scope from session to project'"
            data-testid="artifact-preview-promote"
            @click="askPromote"
          >
            Promote
          </button>
          <button
            type="button"
            class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1 text-[12px] text-ink-muted hover:bg-surface-2 hover:text-signal-danger"
            :disabled="busy"
            data-testid="artifact-preview-delete"
            @click="askDelete"
          >
            Delete
          </button>
        </div>
      </footer>
        </div>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
