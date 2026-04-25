<script setup lang="ts">
/**
 * StreamingText — renders streaming text token-by-token without layout
 * thrash. Consumes a reactive `text` ref/prop that grows in place.
 *
 * Strategy: keep the full text in a single text node and render a brass
 * caret beside it while `streaming` is true. We avoid per-token DOM
 * inserts (which trigger layout) — the entire string is replaced via
 * Vue's textInterpolation, which only patches the text node.
 *
 * The caret animates with a CSS keyframe; tokens.css owns the colour.
 */
defineProps<{
  text: string;
  streaming?: boolean;
}>();
</script>

<template>
  <span class="streaming-text" role="text">
    <span class="whitespace-pre-wrap break-words">{{ text }}</span>
    <span
      v-if="streaming"
      aria-hidden="true"
      class="streaming-caret inline-block w-[2px] h-[1em] align-text-bottom ml-0.5 bg-accent"
    ></span>
  </span>
</template>

<style scoped>
.streaming-caret {
  animation: streaming-caret-blink 900ms steps(2, start) infinite;
}

@keyframes streaming-caret-blink {
  to {
    visibility: hidden;
  }
}
</style>
