<script setup lang="ts">
/**
 * SliderQuestion — range slider for question kind="slider".
 *
 * Renders an <input type="range"> with required min/max/step props.
 * Emits update:modelValue with a number.
 */
import { ref, onMounted } from 'vue';

const props = withDefaults(
  defineProps<{
    min: number;
    max: number;
    step: number;
    defaultValue?: unknown;
  }>(),
  {
    defaultValue: undefined,
  },
);

const emit = defineEmits<{
  'update:modelValue': [value: number];
}>();

// Default to the midpoint when no default is provided.
const value = ref<number>(props.min + (props.max - props.min) / 2);

onMounted(() => {
  const dv = props.defaultValue;
  if (typeof dv === 'number' && !Number.isNaN(dv)) {
    value.value = Math.min(props.max, Math.max(props.min, dv));
  }
  emit('update:modelValue', value.value);
});

function onInput(e: Event) {
  value.value = parseFloat((e.target as HTMLInputElement).value);
  emit('update:modelValue', value.value);
}
</script>

<template>
  <div data-testid="slider-question" class="space-y-2">
    <div class="flex items-center justify-between font-ui text-[11px] text-ink-muted">
      <span>{{ min }}</span>
      <span
        class="rounded-sm bg-accent/10 px-2 py-0.5 font-mono text-[13px] text-accent"
        data-testid="slider-question-value"
      >
        {{ value }}
      </span>
      <span>{{ max }}</span>
    </div>
    <input
      type="range"
      :value="value"
      :min="min"
      :max="max"
      :step="step"
      class="w-full accent-accent"
      data-testid="slider-question-input"
      @input="onInput"
    />
  </div>
</template>
