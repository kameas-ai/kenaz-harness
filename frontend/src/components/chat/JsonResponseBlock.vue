<script setup lang="ts">
/**
 * JsonResponseBlock — renders a structured JSON assistant response.
 *
 * When a model is called with `response_format: { type: "json_schema" }` or
 * `"json_object"`, the Go-side normaliser boxes the raw JSON string into a
 * ContentBlock with `type = "json_response"` and places the raw text in
 * `block.text`. This component:
 *
 *   1. Attempts `JSON.parse(block.text)`.
 *   2. On success: pretty-prints via `JSON.stringify(…, null, 2)` inside a
 *      `<pre><code>` block (syntax-highlighted via a CSS class that the
 *      theme token-system can target).
 *   3. On parse failure (malformed JSON from the model): falls back to a
 *      plain `<pre>` so the raw text is still visible.
 *
 * Privacy CI invariant #4 (no raw color literals) — all colours resolve
 * through design tokens.
 * No v-html — pure text rendering inside <pre>.
 *
 * (multimodal-io-extended-01KQ8TD2 WP06)
 */

import { computed } from 'vue';

const props = defineProps<{
  /** Raw JSON string from the assistant (the content of `block.text`). */
  rawJson: string;
}>();

interface ParseResult {
  ok: true;
  formatted: string;
  parsed: unknown;
}

interface ParseError {
  ok: false;
  raw: string;
}

const parsed = computed<ParseResult | ParseError>(() => {
  try {
    const obj = JSON.parse(props.rawJson) as unknown;
    return { ok: true, formatted: JSON.stringify(obj, null, 2), parsed: obj };
  } catch {
    return { ok: false, raw: props.rawJson };
  }
});

const isValidJson = computed(() => parsed.value.ok);
const displayText = computed(() =>
  parsed.value.ok ? parsed.value.formatted : parsed.value.raw,
);
</script>

<template>
  <div
    class="my-1 rounded-md border border-border-muted bg-surface-1 overflow-auto"
    data-testid="json-response-block"
  >
    <div
      class="flex items-center gap-1.5 border-b border-border-muted px-3 py-1 font-ui text-[10px] uppercase tracking-[0.14em] text-ink-subtle"
    >
      <span>JSON</span>
      <span
        v-if="!isValidJson"
        class="text-signal-warn"
        data-testid="json-response-block-invalid"
        title="The model returned text that could not be parsed as valid JSON."
      >
        (invalid)
      </span>
    </div>
    <pre
      class="m-0 px-3 py-2 font-mono text-[12px] text-ink overflow-x-auto whitespace-pre-wrap break-words"
      data-testid="json-response-block-content"
    ><code>{{ displayText }}</code></pre>
  </div>
</template>
