<script setup lang="ts">
/**
 * SlashArgFill — arg-fill chip strip + 6 input-type widgets.
 *
 * Shown after the user picks a slash command that declares one or more
 * UserCommandInput entries. The component owns the fill-state machine:
 *
 *   idle → filling (per-arg focused) → all-filled → [submit | cancel]
 *
 * The parent (SessionsView / ChatInput integration layer) renders this
 * panel above the chat composer when a user-defined command is selected
 * and its args list is non-empty. When all args are resolved the panel
 * emits `submit(args)` so the parent can call `client.slashcmd.run`.
 *
 * Input widget types mirror UserCommandInputKind (6 kinds):
 *   text        — single-line <input type="text">
 *   number      — <input type="number">
 *   enum        — <select> driven by input.enumValues
 *   file        — path text input with optional OS picker (Shell_PickFile)
 *   artifact_ref — text input (artifact ID / name; no picker in v1)
 *   project_ref  — text input (project ID / name; no picker in v1)
 *
 * user-slash-commands-01KQ8TD9 WP06 (v0.8.x patch lane).
 */

import { computed, onMounted, ref, watch } from 'vue';
import { X, FolderOpen } from 'lucide-vue-next';
import type { UserCommand, UserCommandInput } from '@/lib/types';
import { useHarnessClient } from '@/lib/harnessClientContext';

// ── Props / Emits ───────────────────────────────────────────────────────

const props = defineProps<{
  /** The selected command whose inputs must be filled. */
  command: UserCommand;
  /**
   * Pre-resolved args parsed from what the user typed in the composer
   * (positional or named). Keys must match input.name values.
   * Allows skipping already-typed args.
   */
  prefilled?: Record<string, string>;
}>();

const emit = defineEmits<{
  /**
   * All required args are filled and the user confirmed. The parent
   * should call client.slashcmd.run(command.name, args, ...).
   */
  (e: 'submit', args: Record<string, string>): void;
  /** The user dismissed the panel without submitting. */
  (e: 'cancel'): void;
}>();

// ── Client (for file picker) ────────────────────────────────────────────

const client = useHarnessClient();

// ── Input list ─────────────────────────────────────────────────────────

/**
 * Inputs to fill — always ordered as declared on the command.
 * Empty when the command has no inputs (parent shouldn't open the panel).
 */
const inputs = computed<readonly UserCommandInput[]>(
  () => props.command.inputs ?? [],
);

// ── Fill state machine ──────────────────────────────────────────────────
//
// `values` maps input.name → current string value.
// Seeded from props.prefilled on mount + when the command changes.
// `focusedIndex` tracks which chip / widget is active (keyboard flow).

const values = ref<Record<string, string>>({});
const focusedIndex = ref(0);

function seedValues() {
  const next: Record<string, string> = {};
  for (const inp of inputs.value) {
    const pre = props.prefilled?.[inp.name] ?? '';
    next[inp.name] = pre !== '' ? pre : (inp.default ?? '');
  }
  values.value = next;
  // Focus the first unfilled required input, else the first input.
  const firstUnfilled = inputs.value.findIndex(
    (inp) => inp.required && !values.value[inp.name],
  );
  focusedIndex.value = firstUnfilled >= 0 ? firstUnfilled : 0;
}

onMounted(seedValues);
watch(() => props.command, seedValues);
watch(() => props.prefilled, seedValues, { deep: true });

// ── Validation helpers ──────────────────────────────────────────────────

function isFilled(inp: UserCommandInput): boolean {
  const raw = values.value[inp.name];
  const val = raw !== undefined && raw !== null ? String(raw) : '';
  return !inp.required || val.trim() !== '';
}

const allFilled = computed(() => inputs.value.every(isFilled));

function chipStatus(inp: UserCommandInput): 'done' | 'active' | 'pending' {
  const idx = inputs.value.indexOf(inp);
  if (isFilled(inp)) return 'done';
  if (idx === focusedIndex.value) return 'active';
  return 'pending';
}

// ── Submission ──────────────────────────────────────────────────────────

const submitting = ref(false);
const submitError = ref<string | null>(null);

async function submit() {
  if (!allFilled.value) return;
  submitting.value = true;
  submitError.value = null;
  try {
    // Build a clean copy — stringify (number v-model coerces to number)
    // and strip extra whitespace from text-like inputs.
    const args: Record<string, string> = {};
    for (const inp of inputs.value) {
      const raw = values.value[inp.name];
      args[inp.name] = (raw !== undefined && raw !== null ? String(raw) : '').trim();
    }
    emit('submit', args);
  } catch (e) {
    submitError.value = e instanceof Error ? e.message : String(e);
  } finally {
    submitting.value = false;
  }
}

function cancel() {
  emit('cancel');
}

// ── File picker (Shell_PickFile) ────────────────────────────────────────

const filePicking = ref<string | null>(null); // input.name being picked

async function pickFile(inputName: string) {
  if (filePicking.value) return;
  filePicking.value = inputName;
  try {
    const path = await client.shell.pickFile('Choose file', '', []);
    if (path) {
      values.value = { ...values.value, [inputName]: path };
    }
  } catch {
    // picker closed / cancelled — ignore
  } finally {
    filePicking.value = null;
  }
}

// ── Keyboard navigation (chip strip) ──────────────────────────────────

function onChipClick(idx: number) {
  focusedIndex.value = idx;
}

function onWidgetKeydown(ev: KeyboardEvent) {
  if (ev.key === 'Escape') {
    cancel();
    return;
  }
  if (ev.key === 'Tab') {
    ev.preventDefault();
    const next = ev.shiftKey ? focusedIndex.value - 1 : focusedIndex.value + 1;
    if (next >= 0 && next < inputs.value.length) {
      focusedIndex.value = next;
    }
    return;
  }
  if (ev.key === 'Enter' && !ev.shiftKey) {
    // Enter on the last input triggers submit.
    if (focusedIndex.value === inputs.value.length - 1) {
      if (allFilled.value) {
        void submit();
      }
    } else {
      ev.preventDefault();
      focusedIndex.value = Math.min(focusedIndex.value + 1, inputs.value.length - 1);
    }
  }
}

// ── Kind label helpers ──────────────────────────────────────────────────

const KIND_PLACEHOLDER: Record<string, string> = {
  text: 'Enter text…',
  number: 'Enter number…',
  enum: '',
  file: '/path/to/file',
  artifact_ref: 'Artifact ID or name',
  project_ref: 'Project ID or name',
};
</script>

<template>
  <div
    class="border border-border-muted rounded-md bg-surface-1 shadow-md p-3 space-y-3"
    data-testid="slash-arg-fill"
    role="group"
    :aria-label="`Fill arguments for /${command.name}`"
  >
    <!-- Header: command name + cancel -->
    <div class="flex items-center justify-between">
      <span class="font-mono text-[12px] text-accent" data-testid="arg-fill-title">
        /{{ command.name }}
      </span>
      <button
        type="button"
        class="text-ink-dim hover:text-signal-danger"
        aria-label="Cancel arg fill"
        data-testid="arg-fill-cancel"
        @click="cancel"
      >
        <X class="w-3.5 h-3.5" />
      </button>
    </div>

    <!-- Chip strip: one chip per input arg -->
    <div
      v-if="inputs.length > 0"
      class="flex flex-wrap gap-1.5"
      data-testid="arg-chips"
      role="tablist"
      aria-label="Argument chips"
    >
      <button
        v-for="(inp, idx) in inputs"
        :key="inp.name"
        type="button"
        role="tab"
        :aria-selected="idx === focusedIndex"
        :data-testid="`arg-chip-${inp.name}`"
        class="px-2 py-0.5 rounded border font-mono text-[11px] transition-fast"
        :class="[
          chipStatus(inp) === 'done'
            ? 'border-accent-dim text-accent bg-surface-2'
            : chipStatus(inp) === 'active'
              ? 'border-accent text-accent bg-accent-glow'
              : 'border-border-muted text-ink-dim bg-surface-1',
        ]"
        @click="onChipClick(idx)"
      >
        {{ inp.name }}
        <span v-if="!inp.required" class="ml-0.5 text-ink-dim text-[10px]">?</span>
        <span v-if="chipStatus(inp) === 'done'" class="ml-0.5 text-[10px]">✓</span>
      </button>
    </div>

    <!-- Input widgets: show all at once, focused input is highlighted -->
    <div class="space-y-2" data-testid="arg-inputs">
      <div
        v-for="(inp, idx) in inputs"
        :key="inp.name"
        class="space-y-1"
        :data-testid="`arg-field-${inp.name}`"
      >
        <label
          :for="`arg-input-${inp.name}`"
          class="font-ui text-[11px] text-ink-dim uppercase tracking-[0.12em]"
        >
          {{ inp.name }}
          <span v-if="inp.required" class="text-signal-danger ml-0.5">*</span>
          <span v-if="inp.hint" class="normal-case tracking-normal ml-2 text-ink-dim">
            {{ inp.hint }}
          </span>
        </label>

        <!-- enum → <select> -->
        <select
          v-if="inp.kind === 'enum'"
          :id="`arg-input-${inp.name}`"
          v-model="values[inp.name]"
          :aria-label="inp.name"
          :data-testid="`arg-widget-enum-${inp.name}`"
          class="block w-full rounded-md border bg-surface-2 font-ui text-sm text-ink px-2 py-1.5 outline-none focus:border-accent"
          :class="idx === focusedIndex ? 'border-accent' : 'border-border-muted'"
          @focus="focusedIndex = idx"
          @keydown="onWidgetKeydown"
        >
          <option v-if="!inp.required" value="">— optional —</option>
          <option v-for="opt in inp.enumValues" :key="opt" :value="opt">{{ opt }}</option>
        </select>

        <!-- number → <input type="number"> -->
        <input
          v-else-if="inp.kind === 'number'"
          :id="`arg-input-${inp.name}`"
          v-model="values[inp.name]"
          type="number"
          :placeholder="inp.hint ?? KIND_PLACEHOLDER.number"
          :required="inp.required"
          :aria-label="inp.name"
          :data-testid="`arg-widget-number-${inp.name}`"
          class="block w-full rounded-md border bg-surface-2 font-ui text-sm text-ink px-2 py-1.5 outline-none focus:border-accent"
          :class="idx === focusedIndex ? 'border-accent' : 'border-border-muted'"
          @focus="focusedIndex = idx"
          @keydown="onWidgetKeydown"
        />

        <!-- file → text + folder-open picker button -->
        <div
          v-else-if="inp.kind === 'file'"
          class="flex gap-1.5"
          :data-testid="`arg-widget-file-${inp.name}`"
        >
          <input
            :id="`arg-input-${inp.name}`"
            v-model="values[inp.name]"
            type="text"
            :placeholder="inp.hint ?? KIND_PLACEHOLDER.file"
            :required="inp.required"
            :aria-label="inp.name"
            class="flex-1 rounded-md border bg-surface-2 font-mono text-sm text-ink px-2 py-1.5 outline-none focus:border-accent"
            :class="idx === focusedIndex ? 'border-accent' : 'border-border-muted'"
            @focus="focusedIndex = idx"
            @keydown="onWidgetKeydown"
          />
          <button
            type="button"
            class="px-2 py-1 rounded-md border border-border-muted bg-surface-2 text-ink-dim hover:text-accent hover:border-accent"
            :aria-label="`Pick file for ${inp.name}`"
            :data-testid="`arg-pick-file-${inp.name}`"
            :disabled="filePicking === inp.name"
            @click="pickFile(inp.name)"
          >
            <FolderOpen class="w-3.5 h-3.5" />
          </button>
        </div>

        <!-- artifact_ref → text input -->
        <input
          v-else-if="inp.kind === 'artifact_ref'"
          :id="`arg-input-${inp.name}`"
          v-model="values[inp.name]"
          type="text"
          :placeholder="inp.hint ?? KIND_PLACEHOLDER.artifact_ref"
          :required="inp.required"
          :aria-label="inp.name"
          :data-testid="`arg-widget-artifact_ref-${inp.name}`"
          class="block w-full rounded-md border bg-surface-2 font-ui text-sm text-ink px-2 py-1.5 outline-none focus:border-accent"
          :class="idx === focusedIndex ? 'border-accent' : 'border-border-muted'"
          @focus="focusedIndex = idx"
          @keydown="onWidgetKeydown"
        />

        <!-- project_ref → text input -->
        <input
          v-else-if="inp.kind === 'project_ref'"
          :id="`arg-input-${inp.name}`"
          v-model="values[inp.name]"
          type="text"
          :placeholder="inp.hint ?? KIND_PLACEHOLDER.project_ref"
          :required="inp.required"
          :aria-label="inp.name"
          :data-testid="`arg-widget-project_ref-${inp.name}`"
          class="block w-full rounded-md border bg-surface-2 font-ui text-sm text-ink px-2 py-1.5 outline-none focus:border-accent"
          :class="idx === focusedIndex ? 'border-accent' : 'border-border-muted'"
          @focus="focusedIndex = idx"
          @keydown="onWidgetKeydown"
        />

        <!-- text (default) → single-line <input> -->
        <input
          v-else
          :id="`arg-input-${inp.name}`"
          v-model="values[inp.name]"
          type="text"
          :placeholder="inp.hint ?? KIND_PLACEHOLDER.text"
          :required="inp.required"
          :aria-label="inp.name"
          :data-testid="`arg-widget-text-${inp.name}`"
          class="block w-full rounded-md border bg-surface-2 font-ui text-sm text-ink px-2 py-1.5 outline-none focus:border-accent"
          :class="idx === focusedIndex ? 'border-accent' : 'border-border-muted'"
          @focus="focusedIndex = idx"
          @keydown="onWidgetKeydown"
        />
      </div>
    </div>

    <!-- Error banner (post-submit failure, unlikely but guard it) -->
    <div
      v-if="submitError"
      class="rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
      role="alert"
      data-testid="arg-fill-error"
    >
      {{ submitError }}
    </div>

    <!-- Action row -->
    <div class="flex items-center justify-end gap-2">
      <button
        type="button"
        class="px-3 py-1 rounded-md border border-border-muted text-ink-dim font-ui text-[11px] uppercase tracking-[0.18em] hover:bg-surface-2"
        data-testid="arg-fill-cancel-btn"
        @click="cancel"
      >
        cancel
      </button>
      <button
        type="button"
        class="px-3 py-1 rounded-md border border-accent text-accent font-ui text-[11px] uppercase tracking-[0.18em] hover:bg-surface-2 disabled:opacity-50 disabled:cursor-not-allowed"
        :disabled="!allFilled || submitting"
        data-testid="arg-fill-submit"
        @click="submit"
      >
        {{ submitting ? 'running…' : 'run' }}
      </button>
    </div>
  </div>
</template>
