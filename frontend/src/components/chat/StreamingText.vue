<script setup lang="ts">
/**
 * StreamingText — thin shim that delegates markdown rendering to
 * MarkdownBlock and overlays the streaming caret.
 *
 * All rendering logic (GFM, KaTeX, Mermaid, code-block header,
 * collapse, sanitization) lives in MarkdownBlock.vue. This component
 * is kept alive solely for its public prop contract and the caret
 * animation so existing callers need no changes.
 */
import MarkdownBlock from '@/components/rendering/MarkdownBlock.vue';

defineProps<{
  text: string;
  streaming?: boolean;
}>();
</script>

<template>
  <span class="streaming-text" role="text">
    <MarkdownBlock :text="text" :streaming="streaming" />
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
