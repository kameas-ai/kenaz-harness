<script setup lang="ts">
/**
 * TextQuestion — freeform text input for question kind="text".
 *
 * Renders a textarea. Emits update:modelValue with the string value.
 * Emits "submit" when the user presses Enter (without Shift) so the
 * parent dialog can submit immediately.
 */
import { ref, onMounted } from 'vue';

const props = withDefaults(
  defineProps<{
    placeholder?: string;
    defaultValue?: unknown;
    /** Accessible label for the textarea (threaded from parent question text). WP07 D-10. */
    ariaLabel?: string;
  }>(),
  {
    placeholder: '',
    defaultValue: undefined,
    ariaLabel: 'Text answer',
  },
);

const emit = defineEmits<{
  'update:modelValue': [value: string];
  submit: [];
}>();

const value = ref<string>('');

onMounted(() => {
  if (typeof props.defaultValue === 'string') {
    value.value = props.defaultValue;
  }
  emit('update:modelValue', value.value);
});

function onInput(e: Event) {
  value.value = (e.target as HTMLTextAreaElement).value;
  emit('update:modelValue', value.value);
}

function onKeydown(e: KeyboardEvent) {
  // Enter without Shift = submit the form.
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault();
    emit('submit');
  }
}
</script>

<template>
  <div data-testid="text-question">
    <textarea
      :value="value"
      :placeholder="placeholder || 'Type your answer…'"
      :aria-label="ariaLabel"
      rows="4"
      class="w-full resize-y rounded-md border border-border-muted bg-surface-2 px-3 py-2 font-ui text-[13px] text-ink placeholder:text-ink-subtle focus:border-accent focus:outline-none"
      data-testid="text-question-input"
      @input="onInput"
      @keydown="onKeydown"
    />
    <p class="mt-1 font-ui text-[10px] text-ink-subtle">
      Enter to submit · Shift+Enter for new line
    </p>
  </div>
</template>
