<script setup lang="ts">
/**
 * DirectoryPicker — chip-list editor for an array of directory paths.
 *
 * v-model contract: `string[]`. Emits `update:modelValue` with a fresh
 * array on add / remove / inline-edit commit.
 *
 * Add semantics:
 *   - Native picker: Wails 2.x runtime does NOT expose an
 *     OpenDirectoryDialog in its TS surface, so we fall back to an
 *     `<input type="file" webkitdirectory>` element. Selecting any
 *     file in a folder gives us `webkitRelativePath = "<root>/sub/file"`,
 *     and we strip the trailing path components down to the picked
 *     root. The browser only surfaces the relative path (no absolute
 *     prefix), so the chip is added as-is and the backend's
 *     ValidateAllowedDir will reject anything that doesn't resolve.
 *     For Wails desktop, the user can always type or paste the
 *     absolute path directly via inline edit.
 *   - Inline edit + free-text: clicking a chip enters edit mode;
 *     Enter commits, Escape reverts. Empty edits remove the chip.
 *
 * Validation lives entirely on the backend — `path_validation.go`'s
 * deny-list runs at install time. The modal surfaces any rejection
 * via its error banner; the picker stays passively additive.
 */

import { computed, nextTick, ref, watch } from 'vue';
import { Plus, X } from '@/shell/icons';
import { useHarnessClient } from '@/lib/harnessClientContext';

const client = useHarnessClient();

const props = withDefaults(
  defineProps<{
    modelValue?: readonly string[];
    placeholder?: string;
    inputId?: string;
  }>(),
  {
    modelValue: () => [],
    placeholder: '/path/to/directory',
    inputId: '',
  },
);

const emit = defineEmits<{
  (e: 'update:modelValue', value: string[]): void;
}>();

const editingIndex = ref<number | null>(null);
const editingValue = ref<string>('');
const editingOriginal = ref<string>('');
const fileInput = ref<HTMLInputElement | null>(null);
const editingInput = ref<HTMLInputElement | null>(null);

const list = computed<readonly string[]>(() => props.modelValue ?? []);

function emitNext(next: readonly string[]) {
  emit('update:modelValue', [...next]);
}

function add(path: string) {
  const trimmed = path.trim();
  if (!trimmed) return;
  // De-dupe: dropping an already-listed path is a no-op rather than
  // an error — keeps the modal-submit path predictable when the user
  // re-picks a folder.
  if (list.value.includes(trimmed)) return;
  emitNext([...list.value, trimmed]);
}

function remove(index: number) {
  if (index < 0 || index >= list.value.length) return;
  const next = list.value.slice();
  next.splice(index, 1);
  emitNext(next);
  if (editingIndex.value === index) {
    editingIndex.value = null;
  }
}

function startEdit(index: number) {
  if (editingIndex.value !== null) return;
  editingOriginal.value = list.value[index] ?? '';
  editingValue.value = editingOriginal.value;
  editingIndex.value = index;
  void nextTick(() => {
    // editingInput is a single ref bound inside v-if/v-else inside v-for.
    // Vue 3 may surface it as a single Element OR an array depending on
    // template structure; tolerate both shapes.
    const raw = editingInput.value as unknown;
    const el = Array.isArray(raw)
      ? (raw[0] as HTMLInputElement | undefined)
      : (raw as HTMLInputElement | null | undefined);
    el?.focus?.();
    el?.select?.();
  });
}

function commitEdit() {
  if (editingIndex.value === null) return;
  const idx = editingIndex.value;
  const next = list.value.slice();
  const value = editingValue.value.trim();
  if (!value) {
    // Empty commit removes the chip — matches the "X" affordance and
    // gives the user a single keyboard path (Backspace + Enter).
    next.splice(idx, 1);
  } else if (next.includes(value) && next[idx] !== value) {
    // Collapsing onto an existing chip drops the duplicate.
    next.splice(idx, 1);
  } else {
    next[idx] = value;
  }
  emitNext(next);
  editingIndex.value = null;
}

function cancelEdit() {
  editingIndex.value = null;
}

function onEditKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter') {
    e.preventDefault();
    commitEdit();
  } else if (e.key === 'Escape') {
    e.preventDefault();
    cancelEdit();
  }
}

async function openPicker() {
  // Try the OS-native folder dialog first (Wails-bound, returns the
  // absolute path). If it fails for any reason — Wails ctx not ready,
  // platform without a native dialog, etc. — fall back to the
  // <input type="file" webkitdirectory> path that gives us a
  // relative-path chip the user can edit inline.
  try {
    const picked = await client.tools.pickDirectory('Allow this directory', '');
    if (picked && picked.trim() !== '') {
      add(picked.trim());
      return;
    }
    // User cancelled the native dialog — empty string. Don't fall
    // through to the webkitdirectory input; that would re-prompt
    // immediately and feel like a UX bug.
    return;
  } catch {
    // Native picker unavailable — fall through to webkit fallback.
  }
  fileInput.value?.click();
}

function onFilePicked(event: Event) {
  const input = event.target as HTMLInputElement;
  const files = input.files;
  if (!files || files.length === 0) {
    input.value = '';
    return;
  }
  // webkitRelativePath looks like "rootName/sub/file.ext". The user
  // picked the rootName folder; we surface it as the chip text. The
  // backend's ValidateAllowedDir rejects relative paths, so the user
  // typically follows up with an inline edit to paste the absolute
  // path. For Wails desktop the runtime DOES hand back
  // webkitRelativePath, but the file layer can't expose the full
  // OS path — that's a browser-sandbox restriction.
  //
  // Picking the same root multiple times only adds it once thanks
  // to the de-dupe guard in `add`.
  const seen = new Set<string>();
  for (let i = 0; i < files.length; i++) {
    const f = files[i];
    const rel = (f as File & { webkitRelativePath?: string }).webkitRelativePath ?? '';
    if (!rel) continue;
    const root = rel.split('/')[0];
    if (root && !seen.has(root)) {
      seen.add(root);
      add(root);
    }
  }
  input.value = '';
}

watch(
  () => props.modelValue,
  () => {
    // External mutations (e.g. modal "reset to defaults") cancel an
    // in-progress edit so the chip set stays coherent.
    editingIndex.value = null;
  },
);
</script>

<template>
  <div class="space-y-2" :data-testid="inputId ? `dirpicker-${inputId}` : 'dirpicker'">
    <ul
      v-if="list.length > 0"
      class="flex flex-wrap gap-1.5"
      :data-testid="inputId ? `dirpicker-list-${inputId}` : 'dirpicker-list'"
    >
      <li
        v-for="(p, i) in list"
        :key="`${i}:${p}`"
        class="inline-flex items-center gap-1 rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-[11px] text-ink"
        :data-testid="`dirpicker-chip-${i}`"
      >
        <input
          v-if="editingIndex === i"
          ref="editingInput"
          v-model="editingValue"
          type="text"
          :aria-label="`Edit directory path ${i + 1}`"
          class="min-w-[12rem] bg-transparent font-mono text-[11px] text-ink focus:outline-none"
          spellcheck="false"
          autocomplete="off"
          :data-testid="`dirpicker-edit-${i}`"
          @keydown="onEditKeydown"
          @blur="commitEdit"
        />
        <button
          v-else
          type="button"
          class="text-left font-mono text-[11px] text-ink hover:text-accent"
          :data-testid="`dirpicker-chip-text-${i}`"
          @click="startEdit(i)"
        >
          {{ p }}
        </button>
        <button
          type="button"
          class="ml-0.5 inline-flex h-4 w-4 items-center justify-center rounded-sm text-ink-dim hover:text-signal-danger"
          :aria-label="`Remove ${p}`"
          :data-testid="`dirpicker-remove-${i}`"
          @click="remove(i)"
        >
          <X class="h-3 w-3" aria-hidden="true" />
        </button>
      </li>
    </ul>

    <div class="flex items-center gap-2">
      <button
        type="button"
        class="inline-flex items-center gap-1 rounded-sm border border-border-muted bg-surface-1 px-2 py-1 font-ui text-[11px] text-ink-dim hover:text-ink hover:bg-surface-2"
        :data-testid="inputId ? `dirpicker-add-${inputId}` : 'dirpicker-add'"
        @click="openPicker"
      >
        <Plus class="h-3 w-3" aria-hidden="true" />
        <span>Add directory</span>
      </button>
      <span
        v-if="list.length === 0"
        class="font-ui text-[11px] text-ink-subtle"
        :data-testid="inputId ? `dirpicker-empty-${inputId}` : 'dirpicker-empty'"
      >
        No directories yet — click <span class="font-mono">Add directory</span>.
      </span>
    </div>

    <input
      ref="fileInput"
      type="file"
      aria-label="Select directory"
      aria-hidden="true"
      class="hidden"
      webkitdirectory
      directory
      multiple
      :data-testid="inputId ? `dirpicker-file-${inputId}` : 'dirpicker-file'"
      @change="onFilePicked"
    />
  </div>
</template>
