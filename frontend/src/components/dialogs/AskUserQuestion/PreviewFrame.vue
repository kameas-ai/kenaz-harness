<script setup lang="ts">
/**
 * PreviewFrame — side-by-side preview pane for AskUserQuestion.
 *
 * Dispatches to one of 8 renderer slots based on the `kind` prop:
 *
 *   markdown     — parsed Markdown via marked + DOMPurify
 *   code         — syntax-highlighted code block (language hint)
 *   image        — <img> tag with data-URL or URL src
 *   diff         — unified-diff coloured view (red/green lines)
 *   table        — HTML table from a simple 2D array (JSON array of arrays)
 *   html         — sanitised HTML rendered in a sandboxed iframe-like div
 *   vue-component — reserved (renders a "not yet available" notice for WP02)
 *   plain        — plain text in a pre block
 *
 * Security: markdown, html, and vue-component content is sanitised via
 * DOMPurify before insertion with innerHTML. Code and plain use textContent
 * only. Image src is only rendered when it looks like a data-URL or relative
 * path — remote URLs are blocked by the app's CSP (connect-src 'none').
 */

import { computed } from 'vue';
import { marked } from 'marked';
import DOMPurify from 'dompurify';

export type PreviewKind =
  | 'markdown'
  | 'code'
  | 'image'
  | 'diff'
  | 'table'
  | 'html'
  | 'vue-component'
  | 'plain';

const props = withDefaults(
  defineProps<{
    kind: string;
    content: string;
    language?: string;
  }>(),
  {
    language: '',
  },
);

// ── markdown ───────────────────────────────────────────────────────────

const markdownHtml = computed<string>(() => {
  if (props.kind !== 'markdown') return '';
  const raw = marked(props.content, { async: false }) as string;
  return DOMPurify.sanitize(raw);
});

// ── html ───────────────────────────────────────────────────────────────

const sanitisedHtml = computed<string>(() => {
  if (props.kind !== 'html') return '';
  return DOMPurify.sanitize(props.content);
});

// ── diff ───────────────────────────────────────────────────────────────

interface DiffLine {
  text: string;
  cls: 'added' | 'removed' | 'header' | 'context';
}

const diffLines = computed<DiffLine[]>(() => {
  if (props.kind !== 'diff') return [];
  return props.content.split('\n').map((line) => {
    if (line.startsWith('+')) return { text: line, cls: 'added' };
    if (line.startsWith('-')) return { text: line, cls: 'removed' };
    if (line.startsWith('@@') || line.startsWith('---') || line.startsWith('+++')) {
      return { text: line, cls: 'header' };
    }
    return { text: line, cls: 'context' };
  });
});

// ── table ─────────────────────────────────────────────────────────────

interface TableData {
  headers: string[];
  rows: string[][];
}

const tableData = computed<TableData | null>(() => {
  if (props.kind !== 'table') return null;
  try {
    const parsed = JSON.parse(props.content) as unknown;
    if (!Array.isArray(parsed) || parsed.length === 0) return null;
    const [first, ...rest] = parsed as unknown[][];
    if (!Array.isArray(first)) return null;
    return {
      headers: first.map(String),
      rows: rest.map((row) => (Array.isArray(row) ? row.map(String) : [])),
    };
  } catch {
    return null;
  }
});

// ── image ─────────────────────────────────────────────────────────────

// Allow data-URLs and relative paths only; block http(s) remote URLs
// since the CSP disallows img-src remote origins.
const imageSrc = computed<string>(() => {
  if (props.kind !== 'image') return '';
  const c = props.content.trim();
  if (c.startsWith('data:') || !c.startsWith('http')) return c;
  // Block remote URLs — return empty so the placeholder renders.
  return '';
});
</script>

<template>
  <div
    class="flex h-full flex-col overflow-hidden bg-surface-2"
    data-testid="preview-frame"
    :data-preview-kind="kind"
  >
    <!-- Header bar -->
    <div
      class="flex items-center gap-2 border-b border-border-muted px-4 py-2 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle"
    >
      <span>Preview</span>
      <span class="rounded-sm bg-surface-1 px-1.5 py-0.5 font-mono text-ink-muted">
        {{ kind }}
      </span>
      <span
        v-if="language"
        class="rounded-sm bg-surface-1 px-1.5 py-0.5 font-mono text-ink-subtle"
      >
        {{ language }}
      </span>
    </div>

    <!-- Content area -->
    <div class="flex-1 overflow-auto p-4">
      <!-- markdown -->
      <div
        v-if="kind === 'markdown'"
        class="prose prose-sm max-w-none font-ui text-ink"
        data-testid="preview-markdown"
        v-html="markdownHtml"
      />

      <!-- code -->
      <pre
        v-else-if="kind === 'code'"
        class="overflow-auto rounded-sm bg-surface-1 p-3 font-mono text-[12px] text-ink"
        data-testid="preview-code"
      ><code>{{ content }}</code></pre>

      <!-- image -->
      <div
        v-else-if="kind === 'image'"
        class="flex items-center justify-center"
        data-testid="preview-image"
      >
        <img
          v-if="imageSrc"
          :src="imageSrc"
          alt="Preview"
          class="max-h-full max-w-full object-contain"
        />
        <div
          v-else
          class="font-ui text-[12px] text-ink-subtle"
        >
          Image unavailable (remote URLs blocked by CSP)
        </div>
      </div>

      <!-- diff -->
      <pre
        v-else-if="kind === 'diff'"
        class="overflow-auto rounded-sm bg-surface-1 p-3 font-mono text-[12px]"
        data-testid="preview-diff"
      ><div
          v-for="(line, i) in diffLines"
          :key="i"
          :class="{
            'text-signal-ok bg-signal-ok/10': line.cls === 'added',
            'text-signal-danger bg-signal-danger/10': line.cls === 'removed',
            'text-accent': line.cls === 'header',
            'text-ink-muted': line.cls === 'context',
          }"
        >{{ line.text }}</div></pre>

      <!-- table -->
      <div
        v-else-if="kind === 'table'"
        class="overflow-auto"
        data-testid="preview-table"
      >
        <table
          v-if="tableData"
          class="w-full border-collapse font-ui text-[12px] text-ink"
        >
          <thead>
            <tr class="border-b border-border-muted bg-surface-1">
              <th
                v-for="h in tableData.headers"
                :key="h"
                class="px-3 py-2 text-left font-semibold text-ink-muted"
              >
                {{ h }}
              </th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="(row, ri) in tableData.rows"
              :key="ri"
              class="border-b border-border-muted even:bg-surface-1"
            >
              <td
                v-for="(cell, ci) in row"
                :key="ci"
                class="px-3 py-1.5"
              >
                {{ cell }}
              </td>
            </tr>
          </tbody>
        </table>
        <div
          v-else
          class="font-ui text-[12px] text-signal-warn"
        >
          Invalid table data — expected JSON array of arrays.
        </div>
      </div>

      <!-- html -->
      <div
        v-else-if="kind === 'html'"
        class="font-ui text-[13px] text-ink"
        data-testid="preview-html"
        v-html="sanitisedHtml"
      />

      <!-- vue-component (deferred — WP05+) -->
      <div
        v-else-if="kind === 'vue-component'"
        class="flex flex-col items-center justify-center gap-2 text-center"
        data-testid="preview-vue-component"
      >
        <div class="font-ui text-[12px] text-ink-muted">
          Vue component preview is available in a future release.
        </div>
      </div>

      <!-- plain (default / fallback) -->
      <pre
        v-else
        class="overflow-auto whitespace-pre-wrap break-words font-mono text-[12px] text-ink"
        data-testid="preview-plain"
      >{{ content }}</pre>
    </div>
  </div>
</template>
