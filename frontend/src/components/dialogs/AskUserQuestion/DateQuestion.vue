<script setup lang="ts">
/**
 * DateQuestion — date-only picker for question kind="date".
 *
 * Renders an <input type="date"> (YYYY-MM-DD). Emits
 * update:modelValue with the date string or null when empty.
 */
import { ref, onMounted } from 'vue';

const props = withDefaults(
  defineProps<{
    defaultValue?: unknown;
    /** Accessible label for the input (threaded from parent question text). WP07 D-10. */
    ariaLabel?: string;
  }>(),
  {
    defaultValue: undefined,
    ariaLabel: 'Date',
  },
);

const emit = defineEmits<{
  'update:modelValue': [value: string | null];
}>();

const value = ref<string>('');

onMounted(() => {
  if (typeof props.defaultValue === 'string' && /^\d{4}-\d{2}-\d{2}$/.test(props.defaultValue)) {
    value.value = props.defaultValue;
  }
  emit('update:modelValue', value.value || null);
});

function onInput(e: Event) {
  value.value = (e.target as HTMLInputElement).value;
  emit('update:modelValue', value.value || null);
}
</script>

<template>
  <div data-testid="date-question">
    <input
      type="date"
      :value="value"
      :aria-label="ariaLabel"
      class="w-full rounded-md border border-border-muted bg-surface-2 px-3 py-2 font-ui text-[13px] text-ink focus:border-accent focus:outline-none"
      data-testid="date-question-input"
      @input="onInput"
    />
  </div>
</template>
