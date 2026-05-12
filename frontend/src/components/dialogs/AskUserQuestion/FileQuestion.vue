<script setup lang="ts">
/**
 * FileQuestion — file picker for question kind="file" (WP10).
 *
 * Uses Shell_PickFile (client.shell.pickFile) to open the OS-native
 * file-selection dialog. The binding is available in the Wails runtime;
 * in test/browser environments the mock returns an empty string, which
 * is treated as a cancellation.
 *
 * Props:
 *   - filters: optional Wails FileFilter strings in the form
 *              "Display Name|*.ext1;*.ext2". Forwarded verbatim to the
 *              Wails OpenFileDialog call.
 *
 * Emits update:modelValue with the selected path string or null.
 */
import { ref } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';

const props = withDefaults(
  defineProps<{
    /** Optional file-extension filters, e.g. ["Images|*.png;*.jpg"]. */
    filters?: string[];
  }>(),
  { filters: () => [] },
);

const client = useHarnessClient();

const emit = defineEmits<{
  'update:modelValue': [value: string | null];
}>();

const selectedPath = ref<string | null>(null);
const picking = ref(false);
const pickError = ref<string | null>(null);

async function openPicker() {
  picking.value = true;
  pickError.value = null;
  try {
    const path = await client.shell.pickFile('Choose a file', '', props.filters);
    selectedPath.value = path || null;
    emit('update:modelValue', selectedPath.value);
  } catch (err) {
    pickError.value = err instanceof Error ? err.message : String(err);
  } finally {
    picking.value = false;
  }
}

function clear() {
  selectedPath.value = null;
  emit('update:modelValue', null);
}
</script>

<template>
  <div
    data-testid="file-question"
    class="space-y-3"
  >
    <div
      class="flex items-center gap-3 rounded-md border border-border-muted bg-surface-2 p-3"
    >
      <span
        class="flex-1 truncate font-mono text-[12px]"
        :class="selectedPath ? 'text-ink' : 'text-ink-subtle'"
        data-testid="file-question-path"
      >
        {{ selectedPath ?? 'No file selected' }}
      </span>
      <button
        v-if="selectedPath"
        type="button"
        class="text-ink-muted hover:text-ink-subtle"
        data-testid="file-question-clear"
        aria-label="Clear selection"
        @click="clear"
      >
        ✕
      </button>
    </div>

    <button
      type="button"
      class="rounded-md border border-accent px-3 py-2 font-ui text-[11px] uppercase tracking-[0.18em] text-accent hover:bg-surface-2 disabled:cursor-not-allowed disabled:opacity-50"
      :disabled="picking"
      data-testid="file-question-pick-btn"
      @click="openPicker"
    >
      {{ picking ? 'Opening…' : 'Choose file…' }}
    </button>

    <div
      v-if="pickError"
      class="rounded-sm border border-signal-warn px-3 py-2 font-ui text-[12px] text-signal-warn"
      role="alert"
      data-testid="file-question-error"
    >
      {{ pickError }}
    </div>
  </div>
</template>
