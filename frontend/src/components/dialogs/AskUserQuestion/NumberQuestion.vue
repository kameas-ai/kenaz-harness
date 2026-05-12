<script setup lang="ts">
/**
 * NumberQuestion — numeric input for question kind="number".
 *
 * Renders an <input type="number"> with optional min/max/step.
 * Emits update:modelValue with a number (or null when empty).
 */
import { ref, onMounted } from 'vue';

const props = withDefaults(
  defineProps<{
    min?: number | null;
    max?: number | null;
    step?: number | null;
    defaultValue?: unknown;
  }>(),
  {
    min: null,
    max: null,
    step: null,
    defaultValue: undefined,
  },
);

const emit = defineEmits<{
  'update:modelValue': [value: number | null];
}>();

const value = ref<number | null>(null);

onMounted(() => {
  const dv = props.defaultValue;
  if (typeof dv === 'number' && !Number.isNaN(dv)) {
    value.value = dv;
  }
  emit('update:modelValue', value.value);
});

function onInput(e: Event) {
  const raw = (e.target as HTMLInputElement).value;
  value.value = raw === '' ? null : parseFloat(raw);
  emit('update:modelValue', value.value);
}
</script>

<template>
  <div data-testid="number-question">
    <input
      type="number"
      :value="value ?? ''"
      :min="min ?? undefined"
      :max="max ?? undefined"
      :step="step ?? undefined"
      class="w-full rounded-md border border-border-muted bg-surface-2 px-3 py-2 font-ui text-[13px] text-ink placeholder:text-ink-subtle focus:border-accent focus:outline-none"
      placeholder="Enter a number…"
      data-testid="number-question-input"
      @input="onInput"
    />
    <p
      v-if="min !== null || max !== null"
      class="mt-1 font-ui text-[10px] text-ink-subtle"
    >
      <template v-if="min !== null">Min: {{ min }}&nbsp;</template>
      <template v-if="max !== null">Max: {{ max }}&nbsp;</template>
      <template v-if="step !== null">Step: {{ step }}</template>
    </p>
  </div>
</template>
