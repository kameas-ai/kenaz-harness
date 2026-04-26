<script setup lang="ts">
/**
 * ScopePicker — segmented control for picking an attachment scope.
 *
 * Shows three radios: global / project / session. The `project` option
 * is hidden (or rendered disabled with a tooltip) when no project id is
 * available — loose sessions can't attach project-scoped contexts. The
 * `session` option is hidden when no session id is in scope (the
 * Settings → Global page is global-only).
 *
 * Emits `update:modelValue` for v-model; emit values are typed as
 * AttachmentScopeKind so callers can pass the kind straight to
 * Attachments_Add.
 */

import { computed } from 'vue';
import type { AttachmentScopeKind } from '@/lib/types';

const props = withDefaults(
  defineProps<{
    modelValue: AttachmentScopeKind;
    /** When false, the project radio is hidden. Defaults to true. */
    showProject?: boolean;
    /** When false, the session radio is hidden. Defaults to true. */
    showSession?: boolean;
  }>(),
  { showProject: true, showSession: true },
);

const emit = defineEmits<{
  (e: 'update:modelValue', v: AttachmentScopeKind): void;
}>();

interface Option {
  value: AttachmentScopeKind;
  label: string;
  glyph: string;
}

const options = computed<readonly Option[]>(() => {
  const list: Option[] = [
    { value: 'global', label: 'Global', glyph: 'G' },
  ];
  if (props.showProject) {
    list.push({ value: 'project', label: 'Project', glyph: 'P' });
  }
  if (props.showSession) {
    list.push({ value: 'session', label: 'Session', glyph: 'S' });
  }
  return list;
});

function select(v: AttachmentScopeKind) {
  emit('update:modelValue', v);
}
</script>

<template>
  <div
    class="inline-flex rounded-sm border border-border-muted"
    role="radiogroup"
    aria-label="Attachment scope"
    data-testid="scope-picker"
  >
    <button
      v-for="opt in options"
      :key="opt.value"
      type="button"
      role="radio"
      :aria-checked="modelValue === opt.value ? 'true' : 'false'"
      class="flex items-center gap-1.5 px-3 py-1 font-ui text-[12px] border-r border-border-muted last:border-r-0 transition-colors"
      :class="modelValue === opt.value
        ? 'bg-surface-3 text-ink'
        : 'bg-surface-1 text-ink-muted hover:text-ink'"
      :data-testid="`scope-picker-${opt.value}`"
      @click="select(opt.value)"
    >
      <span class="font-mono text-[10px] text-ink-dim">{{ opt.glyph }}</span>
      <span>{{ opt.label }}</span>
    </button>
  </div>
</template>
