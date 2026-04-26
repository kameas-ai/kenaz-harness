<script setup lang="ts">
/**
 * StreamingText — renders streaming model output as Markdown.
 *
 * Pipeline: text → marked.parse (GFM) → DOMPurify.sanitize → v-html.
 * The streaming caret persists beside the rendered tree while
 * `streaming` is true. Re-rendering on every token is cheap — marked
 * + DOMPurify run on a few KB in <1 ms; Vue patches the innerHTML diff
 * in place. All colours flow through tokens.css via the scoped CSS
 * below (privacy CI #4 — no raw color literals here).
 */
import { computed } from 'vue';
import { marked } from 'marked';
import DOMPurify from 'dompurify';

const props = defineProps<{
  text: string;
  streaming?: boolean;
}>();

marked.setOptions({
  gfm: true,
  breaks: false,
});

const html = computed(() => {
  if (!props.text) return '';
  const raw = marked.parse(props.text, { async: false }) as string;
  return DOMPurify.sanitize(raw, {
    ADD_ATTR: ['target', 'rel'],
  });
});
</script>

<template>
  <span class="streaming-text" role="text">
    <span class="md-body" v-html="html"></span>
    <span
      v-if="streaming"
      aria-hidden="true"
      class="streaming-caret inline-block w-[2px] h-[1em] align-text-bottom ml-0.5 bg-accent"
    ></span>
  </span>
</template>

<style scoped>
.md-body {
  display: block;
}
.md-body :deep(p) {
  margin: 0 0 0.65em 0;
  line-height: 1.55;
}
.md-body :deep(p:last-child) {
  margin-bottom: 0;
}
.md-body :deep(ul),
.md-body :deep(ol) {
  margin: 0 0 0.65em 1.25em;
  padding: 0;
  line-height: 1.55;
}
.md-body :deep(li) {
  margin-bottom: 0.15em;
}
.md-body :deep(ul) {
  list-style: disc;
}
.md-body :deep(ol) {
  list-style: decimal;
}
.md-body :deep(h1),
.md-body :deep(h2),
.md-body :deep(h3),
.md-body :deep(h4) {
  font-weight: 600;
  margin: 0.6em 0 0.3em 0;
  line-height: 1.25;
}
.md-body :deep(h1) {
  font-size: 1.2em;
}
.md-body :deep(h2) {
  font-size: 1.1em;
}
.md-body :deep(h3),
.md-body :deep(h4) {
  font-size: 1em;
  color: var(--ink-muted);
}
.md-body :deep(strong) {
  color: var(--ink);
  font-weight: 600;
}
.md-body :deep(em) {
  font-style: italic;
}
.md-body :deep(code) {
  font-family: var(--font-mono);
  font-size: 0.9em;
  background: var(--surface-2);
  border: 1px solid var(--border-muted);
  border-radius: var(--radius-sm);
  padding: 0.05em 0.35em;
  color: var(--ink);
}
.md-body :deep(pre) {
  font-family: var(--font-mono);
  font-size: 0.88em;
  background: var(--surface-2);
  border: 1px solid var(--border-muted);
  border-radius: var(--radius-sm);
  padding: 0.65em 0.85em;
  margin: 0.5em 0 0.75em 0;
  overflow-x: auto;
  line-height: 1.45;
}
.md-body :deep(pre code) {
  background: transparent;
  border: 0;
  padding: 0;
  color: var(--ink);
}
.md-body :deep(blockquote) {
  border-left: 2px solid var(--border-strong);
  padding-left: 0.75em;
  margin: 0.5em 0;
  color: var(--ink-muted);
}
.md-body :deep(a) {
  color: var(--accent);
  text-decoration: underline;
  text-underline-offset: 2px;
}
.md-body :deep(a:hover) {
  color: var(--ink);
}
.md-body :deep(hr) {
  border: 0;
  border-top: 1px solid var(--border-muted);
  margin: 0.85em 0;
}
.md-body :deep(table) {
  border-collapse: collapse;
  margin: 0.5em 0;
  font-size: 0.95em;
}
.md-body :deep(th),
.md-body :deep(td) {
  border: 1px solid var(--border-muted);
  padding: 0.3em 0.6em;
  text-align: left;
}
.md-body :deep(th) {
  background: var(--surface-2);
  font-weight: 600;
}

.streaming-caret {
  animation: streaming-caret-blink 900ms steps(2, start) infinite;
}

@keyframes streaming-caret-blink {
  to {
    visibility: hidden;
  }
}
</style>
