<script setup lang="ts" generic="T extends string">
/**
 * RadioStrip — generic horizontal radio-button group used across
 * Settings (Theme, Compaction tier, Permission mode). Replaces the
 * three near-identical hand-rolled `<button v-for role=radio>` blocks
 * those sections each carried.
 *
 * Generic `T extends string` so the parent gets typed `value` round-
 * trips without a runtime cast.
 */
interface Option {
  value: string;
  label: string;
  /** Optional small note rendered after the label. */
  note?: string;
}

const props = defineProps<{
  modelValue: T;
  options: ReadonlyArray<Option>;
  ariaLabel?: string;
  testidPrefix?: string;
}>();

const emit = defineEmits<{
  'update:modelValue': [value: T];
}>();

function pick(value: string) {
  if (value === props.modelValue) return;
  emit('update:modelValue', value as T);
}
</script>

<template>
  <div
    class="inline-flex rounded-sm border border-border"
    role="radiogroup"
    :aria-label="ariaLabel"
  >
    <button
      v-for="opt in options"
      :key="opt.value"
      type="button"
      role="radio"
      :aria-checked="modelValue === opt.value"
      class="px-3 py-1.5 font-ui text-[12px] border-r border-border last:border-r-0 transition-colors capitalize"
      :class="modelValue === opt.value
        ? 'bg-surface-3 text-ink'
        : 'bg-surface-1 text-ink-muted hover:text-ink'"
      :data-testid="testidPrefix ? `${testidPrefix}-${opt.value}` : undefined"
      @click="pick(opt.value)"
    >
      {{ opt.label }}
      <span v-if="opt.note" class="ml-1 text-[10px] text-ink-subtle normal-case">
        ({{ opt.note }})
      </span>
    </button>
  </div>
</template>
