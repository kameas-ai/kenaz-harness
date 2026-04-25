<script setup lang="ts">
/**
 * Button — owned shadcn-vue copy. Token-themed only. Radix-Vue
 * provides the accessibility scaffolding for the more complex
 * primitives; this one is a plain <button>.
 */
defineProps<{
  variant?: 'default' | 'ghost' | 'accent';
  size?: 'sm' | 'md';
  type?: 'button' | 'submit' | 'reset';
  disabled?: boolean;
}>();
</script>

<template>
  <button
    :type="type ?? 'button'"
    :disabled="disabled"
    class="font-ui rounded-sm transition-fast ease-kenaz disabled:opacity-50"
    :class="[
      size === 'sm' ? 'px-2 py-1 text-xs' : 'px-3 py-1.5 text-sm',
      variant === 'accent'
        ? 'text-accent border border-accent-hairline hover:bg-accent-glow'
        : variant === 'ghost'
          ? 'text-ink-muted hover:text-ink hover:bg-surface-2'
          : 'text-ink bg-surface-2 hover:bg-surface-3 border border-border-muted',
    ]"
  >
    <slot />
  </button>
</template>
