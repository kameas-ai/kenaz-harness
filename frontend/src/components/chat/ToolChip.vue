<script setup lang="ts">
/**
 * ToolChip — one tool call rendered inline in a move trail
 * (model-moves-transcript-01PMCH01 WP04, FR-003).
 *
 * The shape this replaces: before WP04 a `tool_call` / `tool_result` row
 * rendered as a bare monospace MessageBubble line with no
 * `whitespace-pre-wrap`, i.e. the raw untruncated tool output flattened
 * into one run-on line in the conversation. A chip is compact by
 * construction and the output is behind a disclosure, so a 4000-line
 * grep result costs one row of chat instead of scrolling the answer off
 * the screen.
 *
 * Visual register: the ArtifactChip idiom (rounded-sm, border-muted,
 * surface-1, 11px font-ui, monospace for the machine-readable parts)
 * with a status dot on the leading edge. Colours come from the signal
 * ramp so both themes track automatically:
 *   running → accent, gently pulsing (brass = live state, privacy CI #4)
 *   ok      → signal-ok
 *   error   → signal-danger, and the chip itself takes the danger tint
 *
 * REDACTION. `argsSummary` is the display-layer summary the backend
 * computed (argument names + value types). The wire type carries no raw
 * argument values at all, so there is nothing here that could leak them.
 */

import { computed, ref } from 'vue';
import { AlertTriangle, ChevronDown, ChevronRight, Wrench } from '@/shell/icons';
import type { ToolChip } from '@/lib/transcript';

const props = defineProps<{
  chip: ToolChip;
}>();

/**
 * Tool output can be arbitrarily large. Even behind a disclosure it goes
 * into the DOM once expanded, so it is capped — the transcript is a
 * conversation surface, not a log viewer.
 */
const OUTPUT_CAP = 4000;

const expanded = ref(false);

const hasOutput = computed(() => props.chip.output.length > 0);

/**
 * Cut at OUTPUT_CAP code units, then step back off a lone surrogate.
 *
 * `slice` counts UTF-16 code units, so a cap that lands between the two
 * halves of an astral character (emoji, CJK extension B, most of the
 * output of a tool that pretty-prints unicode) leaves a lone surrogate
 * and the browser renders U+FFFD. This repo has shipped that same
 * mid-rune cut twice before in the Go layer; the JS layer has the same
 * hazard with a different unit. Stepping back one code unit is enough:
 * the only way to end on a high surrogate is to have split exactly one
 * pair.
 */
function cutAtRuneBoundary(s: string, cap: number): string {
  const code = s.charCodeAt(cap - 1);
  const splitPair = code >= 0xd800 && code <= 0xdbff;
  return s.slice(0, splitPair ? cap - 1 : cap);
}

const outputText = computed(() => {
  const raw = props.chip.output;
  if (raw.length <= OUTPUT_CAP) return raw;
  const head = cutAtRuneBoundary(raw, OUTPUT_CAP);
  const dropped = raw.length - head.length;
  return `${head}\n…output truncated (${dropped} more characters)`;
});

const statusLabel = computed(() => {
  switch (props.chip.status) {
    case 'running':
      return 'running';
    case 'error':
      return 'error';
    default:
      return 'ok';
  }
});

const dotClass = computed(() => {
  switch (props.chip.status) {
    case 'running':
      return 'bg-accent tool-chip-pulse';
    case 'error':
      return 'bg-signal-danger';
    default:
      return 'bg-signal-ok';
  }
});

const shellClass = computed(() => {
  const base =
    'w-full flex items-center gap-2 rounded-sm border px-2 py-1 font-ui text-[11px] text-left transition-fast';
  if (props.chip.status === 'error') {
    return `${base} border-signal-danger bg-signal-danger-soft text-ink`;
  }
  return `${base} border-border-muted bg-surface-1 text-ink-muted hover:bg-surface-2`;
});

function toggle() {
  if (!hasOutput.value) return;
  expanded.value = !expanded.value;
}
</script>

<template>
  <div class="tool-chip" :data-tool-status="chip.status" data-testid="tool-chip">
    <component
      :is="hasOutput ? 'button' : 'div'"
      :type="hasOutput ? 'button' : undefined"
      :class="shellClass"
      :aria-expanded="hasOutput ? expanded : undefined"
      :aria-label="`Tool ${chip.name}, ${statusLabel}`"
      :data-testid="`tool-chip-${chip.callId}`"
      @click="toggle"
    >
      <span
        class="h-1.5 w-1.5 shrink-0 rounded-full"
        :class="dotClass"
        aria-hidden="true"
      ></span>
      <Wrench :size="12" class="shrink-0 text-ink-subtle" aria-hidden="true" />
      <span class="font-mono shrink-0 text-ink">{{ chip.name }}</span>
      <span
        v-if="chip.argsSummary"
        class="font-mono truncate text-ink-subtle"
        data-testid="tool-chip-args"
      >
        {{ chip.argsSummary }}
      </span>
      <span class="ml-auto shrink-0 flex items-center gap-1">
        <AlertTriangle
          v-if="chip.status === 'error'"
          :size="12"
          class="text-signal-danger"
          aria-hidden="true"
        />
        <span
          class="uppercase tracking-[0.14em] text-[10px]"
          :class="chip.status === 'error' ? 'text-signal-danger' : 'text-ink-subtle'"
          data-testid="tool-chip-status"
        >
          {{ statusLabel }}
        </span>
        <component
          :is="expanded ? ChevronDown : ChevronRight"
          v-if="hasOutput"
          :size="12"
          class="text-ink-subtle"
          aria-hidden="true"
        />
      </span>
    </component>

    <!-- Output on demand. Never in the conversation flow: scrolls inside
         its own box and is capped at OUTPUT_CAP characters. -->
    <pre
      v-if="expanded && hasOutput"
      class="mt-1 max-h-64 overflow-auto rounded-sm border border-border-muted bg-surface-0 px-2 py-1.5 font-mono text-[11px] leading-relaxed text-ink-muted whitespace-pre-wrap break-words"
      data-testid="tool-chip-output"
    >{{ outputText }}</pre>
  </div>
</template>

<style scoped>
.tool-chip-pulse {
  animation: tool-chip-breathe 1.4s ease-in-out infinite;
}

@keyframes tool-chip-breathe {
  0%,
  100% {
    opacity: 0.45;
  }
  50% {
    opacity: 1;
  }
}

@media (prefers-reduced-motion: reduce) {
  .tool-chip-pulse {
    animation: none;
  }
}
</style>
