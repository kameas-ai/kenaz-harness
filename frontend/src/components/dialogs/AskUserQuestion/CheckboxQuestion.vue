<script setup lang="ts">
/**
 * CheckboxQuestion — multi-select list for question kind="checkbox".
 *
 * Renders a set of checkboxes. default_value may be an array of
 * pre-selected option values. Emits update:modelValue with a string[].
 */
import { ref, watch, onMounted } from 'vue';

interface Option {
  value: string;
  label: string;
}

const props = withDefaults(
  defineProps<{
    options: Option[];
    defaultValue?: unknown;
  }>(),
  {
    options: () => [],
    defaultValue: undefined,
  },
);

const emit = defineEmits<{
  'update:modelValue': [value: string[]];
}>();

const selected = ref<Set<string>>(new Set());

onMounted(() => {
  const dv = props.defaultValue;
  if (Array.isArray(dv)) {
    const initial = new Set<string>();
    for (const v of dv) {
      if (typeof v === 'string' && props.options.some((o) => o.value === v)) {
        initial.add(v);
      }
    }
    selected.value = initial;
  }
  emit('update:modelValue', [...selected.value]);
});

function toggle(value: string) {
  const next = new Set(selected.value);
  if (next.has(value)) {
    next.delete(value);
  } else {
    next.add(value);
  }
  selected.value = next;
}

watch(
  selected,
  (s) => emit('update:modelValue', [...s]),
  { deep: true },
);
</script>

<template>
  <fieldset
    class="space-y-2"
    data-testid="checkbox-question"
  >
    <legend class="sr-only">Select one or more options</legend>
    <label
      v-for="opt in options"
      :key="opt.value"
      class="flex cursor-pointer items-center gap-3 rounded-md border border-border-muted bg-surface-2 px-3 py-2 font-ui text-[13px] text-ink hover:border-accent hover:bg-surface-1"
      :class="{
        'border-accent bg-accent/5': selected.has(opt.value),
      }"
      :data-testid="`checkbox-option-${opt.value}`"
    >
      <input
        type="checkbox"
        :value="opt.value"
        :checked="selected.has(opt.value)"
        class="accent-accent"
        @change="toggle(opt.value)"
      />
      {{ opt.label }}
    </label>
  </fieldset>
</template>
