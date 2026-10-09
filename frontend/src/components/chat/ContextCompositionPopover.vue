<script setup lang="ts">
/**
 * ContextCompositionPopover — wraps the context meter and, on hover or
 * click, lists what the last model call's prompt was made of: system
 * prompt, tool definitions, conversation history, attachments, memory,
 * and how much the provider served from its prompt cache.
 *
 * The parts are harness estimates (ceil(bytes / 3.5)); `cached` is the
 * provider's own count. Memory is listed only when non-zero: memory
 * snippets that arrive as messages are counted in history.
 */
import { computed, ref } from 'vue';
import type { UsageComposition } from '@/lib/types';

const props = defineProps<{
  composition: UsageComposition | null | undefined;
}>();

const open = ref(false);

function fmt(n: number): string {
  if (n < 1_000) return String(n);
  if (n < 10_000) return (n / 1_000).toFixed(1).replace(/\.0$/, '') + 'k';
  return Math.round(n / 1_000) + 'k';
}

interface Row {
  key: string;
  label: string;
  tokens: number;
}

const rows = computed<Row[]>(() => {
  const c = props.composition;
  if (!c) return [];
  const out: Row[] = [
    { key: 'system', label: 'System prompt', tokens: c.system },
    {
      key: 'tools',
      label: `Tools (${c.toolsFull} ${c.toolsFull === 1 ? 'definition' : 'definitions'})`,
      tokens: c.tools,
    },
    { key: 'history', label: 'History', tokens: c.history },
    { key: 'attachments', label: 'Attachments', tokens: c.attachments },
  ];
  if (c.memory > 0) out.push({ key: 'memory', label: 'Memory', tokens: c.memory });
  return out;
});

const total = computed(() => rows.value.reduce((sum, r) => sum + r.tokens, 0));
</script>

<template>
  <div
    class="relative"
    @mouseenter="open = true"
    @mouseleave="open = false"
  >
    <button
      type="button"
      class="flex items-center gap-2"
      data-testid="context-composition-trigger"
      :aria-expanded="open"
      aria-haspopup="dialog"
      @click="open = !open"
    >
      <slot />
    </button>
    <div
      v-if="open"
      role="dialog"
      aria-label="Context composition"
      class="absolute bottom-full right-0 z-20 mb-2 w-64 rounded-md border border-hairline bg-surface-1 p-3 text-xs shadow-lg"
      data-testid="context-composition-popover"
    >
      <template v-if="composition">
        <p class="mb-2 text-ink-subtle">Last request, estimated tokens</p>
        <ul class="flex flex-col gap-1">
          <li
            v-for="row in rows"
            :key="row.key"
            class="flex justify-between gap-3"
            :data-testid="`context-composition-${row.key}`"
          >
            <span class="text-ink-muted">{{ row.label }}</span>
            <span class="font-mono tabular-nums text-ink">{{ fmt(row.tokens) }}</span>
          </li>
          <li class="mt-1 flex justify-between gap-3 border-t border-hairline pt-1">
            <span class="text-ink-muted">Total (est.)</span>
            <span class="font-mono tabular-nums text-ink" data-testid="context-composition-total">{{ fmt(total) }}</span>
          </li>
          <li
            class="flex justify-between gap-3"
            data-testid="context-composition-cached"
          >
            <span class="text-ink-muted">Served from cache</span>
            <span class="font-mono tabular-nums text-ink">{{ fmt(composition.cached) }}</span>
          </li>
        </ul>
      </template>
      <p
        v-else
        class="text-ink-subtle"
        data-testid="context-composition-empty"
      >
        No breakdown yet. It appears after the next reply.
      </p>
    </div>
  </div>
</template>
