<script setup lang="ts">
/**
 * ContextPreview — renders the selected file's content with an
 * in-place editor (WP05).
 *
 * Read mode uses a `whitespace-pre-wrap` block — same approach as
 * MessageBubble. v-html / a markdown HTML pipeline is gated on a
 * sanitiser policy that hasn't been ratified; until then the textual
 * source is rendered as-is, which is the safe default and still
 * readable for the markdown-flavoured files users drop in.
 *
 * Edit mode swaps the read block for a textarea bound to a local
 * draft. Save persists via Contexts.Save and emits `saved` so the
 * parent can re-fetch the tree (timestamp + size refresh). Cancel
 * discards the draft. Esc cancels, Cmd/Ctrl+S saves. Files larger
 * than MaxFileBytes are rejected on Save with a clear message.
 *
 * The token chip in the header (US5) shows the rough heuristic
 * `Math.ceil(content.length / 4)` against the live draft when
 * editing or the cached content when previewing — same number that
 * NewSessionDialog displays for upload-flow estimates.
 */
import { computed, nextTick, ref, watch } from 'vue';

const MAX_FILE_BYTES = 1 << 20; // 1 MiB — mirrors core/contexts.MaxFileBytes

const props = defineProps<{
  path: string | null;
  content: string;
  loading?: boolean;
  error?: string | null;
  /**
   * Async save callback — the parent calls Contexts.Save and
   * re-fetches the tree, then resolves. Rejections surface as the
   * inline save-error string in the editor footer. Components that
   * mount the preview in a read-only context (the New Session dialog
   * picker) can omit this; the Edit button is hidden when undefined.
   */
  onSave?: (payload: { path: string; content: string }) => Promise<void>;
}>();

const editing = ref(false);
const draft = ref('');
const saveError = ref<string | null>(null);
const saving = ref(false);
const textareaRef = ref<HTMLTextAreaElement | null>(null);

const tokenEstimate = computed(() => {
  // Mirror NewSessionDialog's heuristic so preview + dialog agree.
  const text = editing.value ? draft.value : props.content;
  return Math.ceil(text.length / 4);
});

const draftBytes = computed(() => new Blob([draft.value]).size);
const draftOversize = computed(() => draftBytes.value > MAX_FILE_BYTES);

watch(
  () => [props.path, props.content] as const,
  () => {
    // Switching files cancels any in-flight edit — saving without
    // committing should not leak across selections.
    editing.value = false;
    draft.value = props.content;
    saveError.value = null;
  },
);

async function startEdit() {
  if (!props.path) return;
  draft.value = props.content;
  saveError.value = null;
  editing.value = true;
  await nextTick();
  textareaRef.value?.focus();
}

function cancelEdit() {
  editing.value = false;
  draft.value = props.content;
  saveError.value = null;
}

async function commitSave() {
  if (!props.path) return;
  if (draftOversize.value) {
    saveError.value =
      'File too large; the library is for reference snippets (1 MiB cap).';
    return;
  }
  if (!props.onSave) {
    // Defensive — the Edit button is hidden when onSave is undefined,
    // but a programmatic call (defineExpose) could still reach here.
    return;
  }
  saving.value = true;
  saveError.value = null;
  try {
    await props.onSave({ path: props.path, content: draft.value });
    editing.value = false;
  } catch (e) {
    saveError.value = e instanceof Error ? e.message : 'Save failed.';
  } finally {
    saving.value = false;
  }
}

function onKeydown(ev: KeyboardEvent) {
  if (ev.key === 'Escape') {
    ev.preventDefault();
    cancelEdit();
    return;
  }
  if ((ev.metaKey || ev.ctrlKey) && (ev.key === 's' || ev.key === 'S')) {
    ev.preventDefault();
    void commitSave();
  }
}

defineExpose({ startEdit, cancelEdit, commitSave });
</script>

<template>
  <section
    class="h-full flex flex-col"
    :aria-label="path ? `Preview of ${path}` : 'Context preview'"
  >
    <header
      v-if="path"
      class="px-4 py-2 border-b border-border-muted bg-surface-1 flex items-baseline gap-2"
    >
      <span class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle">
        {{ editing ? 'Edit' : 'Preview' }}
      </span>
      <span class="font-mono text-[12px] text-ink truncate flex-1">{{ path }}</span>
      <span
        class="font-ui text-[10px] text-ink-dim shrink-0"
        data-testid="context-preview-tokens"
      >
        ~{{ tokenEstimate }} tokens
      </span>
      <button
        v-if="!editing && onSave"
        type="button"
        class="font-ui text-[11px] text-ink-dim hover:text-accent transition-fast"
        data-testid="context-preview-edit"
        @click="startEdit"
      >
        Edit
      </button>
    </header>
    <div class="flex-1 overflow-auto flex flex-col min-h-0">
      <div
        v-if="loading"
        class="px-6 py-4 font-ui text-sm text-ink-muted"
        data-testid="context-preview-loading"
      >
        Loading…
      </div>
      <div
        v-else-if="error"
        class="px-6 py-4 font-ui text-sm text-signal-danger"
        role="alert"
        data-testid="context-preview-error"
      >
        {{ error }}
      </div>
      <div
        v-else-if="!path"
        class="px-6 py-4 font-ui text-sm text-ink-subtle"
        data-testid="context-preview-empty"
      >
        Select a file to preview.
      </div>
      <template v-else-if="editing">
        <textarea
          ref="textareaRef"
          v-model="draft"
          spellcheck="false"
          class="flex-1 w-full px-6 py-4 font-mono text-[12px] leading-relaxed text-ink bg-surface-0 border-0 outline-none resize-none whitespace-pre-wrap"
          data-testid="context-preview-editor"
          @keydown="onKeydown"
        />
        <div
          class="border-t border-border-muted bg-surface-1 px-4 py-2 flex items-center gap-3"
        >
          <span
            class="font-ui text-[10px] text-ink-dim"
            :class="draftOversize ? 'text-signal-danger' : ''"
            data-testid="context-preview-bytes"
          >
            {{ draftBytes.toLocaleString() }} bytes
          </span>
          <span
            v-if="saveError || draftOversize"
            class="font-ui text-[11px] text-signal-danger flex-1 truncate"
            role="alert"
            data-testid="context-preview-save-error"
          >
            {{
              saveError ||
              'File too large; the library is for reference snippets (1 MiB cap).'
            }}
          </span>
          <span v-else class="flex-1" />
          <button
            type="button"
            class="font-ui text-[11px] text-ink-dim hover:text-ink transition-fast"
            data-testid="context-preview-cancel"
            @click="cancelEdit"
          >
            Cancel
          </button>
          <button
            type="button"
            class="font-ui text-[11px] text-accent hover:text-accent-muted transition-fast disabled:opacity-50"
            :disabled="saving || draftOversize"
            data-testid="context-preview-save"
            @click="commitSave"
          >
            {{ saving ? 'Saving…' : 'Save' }}
          </button>
        </div>
      </template>
      <pre
        v-else
        class="px-6 py-4 font-mono text-[12px] leading-relaxed text-ink whitespace-pre-wrap"
        data-testid="context-preview-content"
      >{{ content }}</pre>
    </div>
  </section>
</template>
