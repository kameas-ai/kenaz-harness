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

import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { Download, X } from '@/shell/icons';
import StreamingText from '@/components/chat/StreamingText.vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { Artifact, ArtifactScope, ArtifactWithBytes } from '@/lib/types';

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

const artifact = computed<Artifact | null>(
  () => props.payload?.artifact ?? null,
);

const mimeType = computed(() => artifact.value?.mimeType ?? '');
const bytesB64 = computed(() => props.payload?.bytes ?? '');

const isMarkdown = computed(() => mimeType.value === 'text/markdown');
const isHtml = computed(() => mimeType.value === 'text/html');
const isImage = computed(() => mimeType.value.startsWith('image/'));
// Mimes whose body is plain UTF-8 text we can render in <pre>. Covers
// application/json, application/xml, application/javascript, and the
// "+json"/"+xml" suffix family (e.g. application/manifest+json) plus
// anything explicitly text/*. JSON specifically is the common save-
// artifact output, so treating it as previewable matters.
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

function onKeydown(ev: KeyboardEvent) {
  if (!props.open) return;
  if (ev.key === 'Escape') {
    ev.preventDefault();
    close();
  }
}

watch(
  () => props.open,
  (next) => {
    if (next) {
      allowScripts.value = false;
      showPromoteConfirm.value = false;
      showDeleteConfirm.value = false;
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
  <div
    v-if="open && artifact"
    class="fixed inset-0 z-50 flex items-center justify-center"
    role="dialog"
    aria-modal="true"
    :aria-label="`Preview ${artifact.title}`"
    data-testid="artifact-preview"
    @keydown="onKeydown"
  >
    <div class="absolute inset-0 bg-modal-overlay" @click="onOverlayClick" />
    <div
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
            :src="dataUrl"
            :alt="artifact.title || 'image artifact'"
            class="block max-w-full max-h-[70vh] mx-auto"
          />
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
  </div>
</template>
