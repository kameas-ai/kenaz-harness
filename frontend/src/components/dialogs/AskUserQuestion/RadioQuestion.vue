<script setup lang="ts">
/**
 * RadioQuestion — single-choice list for question kind="radio".
 *
 * Renders a set of radio buttons. The first option is pre-selected
 * when no default_value is provided. Emits update:modelValue with
 * the selected option value string.
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
  'update:modelValue': [value: string | null];
}>();

const selected = ref<string | null>(null);

onMounted(() => {
  const dv = props.defaultValue;
  if (typeof dv === 'string' && props.options.some((o) => o.value === dv)) {
    selected.value = dv;
  } else if (props.options.length > 0) {
    selected.value = props.options[0].value;
  }
  emit('update:modelValue', selected.value);
});

watch(selected, (v) => emit('update:modelValue', v));
</script>

<template>
  <fieldset
    class="space-y-2"
    data-testid="radio-question"
  >
    <legend class="sr-only">Select one option</legend>
    <label
      v-for="opt in options"
      :key="opt.value"
      class="flex cursor-pointer items-center gap-3 rounded-md border border-border-muted bg-surface-2 px-3 py-2 font-ui text-[13px] text-ink hover:border-accent hover:bg-surface-1"
      :class="{
        'border-accent bg-accent/5': selected === opt.value,
      }"
      :data-testid="`radio-option-${opt.value}`"
    >
      <input
        type="radio"
        :value="opt.value"
        :checked="selected === opt.value"
        class="accent-accent"
        @change="selected = opt.value"
      />
      {{ opt.label }}
    </label>
  </fieldset>
</template>
