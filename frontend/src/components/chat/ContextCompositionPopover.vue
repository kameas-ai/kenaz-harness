<script setup lang="ts">
/**
 * ContextCompositionPopover — wraps the context meter and lists what the
 * last model call's prompt was made of: system prompt, tool definitions,
 * conversation history, attachments, memory, and how much the provider
 * served from its prompt cache.
 *
 * Hover shows it; a click pins it open (a click while hovering pins, it
 * never closes what the pointer just opened). Escape, a second click on
 * a pinned popover, or focus leaving the trigger closes it. Focus stays on
 * the trigger, so the panel is a tooltip described by the button, not a
 * dialog.
 *
 * The parts are harness estimates (ceil(bytes / 3.5)); `cached` is the
 * provider's own count. Memory is listed only when non-zero: memory
 * snippets that arrive as messages are counted in history.
 *
 * The schema-budget line (spec §2.5) shows the budget the call's tool
 * definitions were fitted to (`composition.schemaBudget`, WP04) and how
 * many loaded tools were left out to fit it; it is absent when no budget
 * applied (tool exposure unavailable for the call).
 *
 * The default slot receives `{ open }` so the trigger content can drop its
 * own `title` while the panel is showing.
 */
import { computed, ref } from 'vue';
import type { UsageComposition } from '@/lib/types';

const props = defineProps<{
  composition: UsageComposition | null | undefined;
}>();

defineSlots<{ default(props: { open: boolean }): unknown }>();

const hovered = ref(false);
const pinned = ref(false);
const open = computed(() => hovered.value || pinned.value);
const panelId = `context-composition-${Math.random().toString(36).slice(2, 10)}`;

function onClick() {
  pinned.value = !pinned.value;
  if (!pinned.value) hovered.value = false;
}

function close() {
  pinned.value = false;
  hovered.value = false;
}

function onFocusOut(ev: FocusEvent) {
  const root = ev.currentTarget as HTMLElement | null;
  const next = ev.relatedTarget as Node | null;
  if (root && next && root.contains(next)) return;
  pinned.value = false;
}

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

const budget = computed(() => props.composition?.schemaBudget ?? 0);
const evicted = computed(() => props.composition?.toolsEvicted ?? 0);
</script>

<template>
  <div
    class="relative"
    @mouseenter="hovered = true"
    @mouseleave="hovered = false"
    @focusout="onFocusOut"
    @keydown.esc="close"
  >
    <button
      type="button"
      class="flex items-center gap-2"
      data-testid="context-composition-trigger"
      :aria-expanded="open"
      :aria-describedby="open ? panelId : undefined"
      @click="onClick"
    >
      <slot :open="open" />
    </button>
    <div
      v-if="open"
      :id="panelId"
      role="tooltip"
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
            v-if="budget > 0"
            class="flex justify-between gap-3"
            data-testid="context-composition-budget"
          >
            <span class="text-ink-muted">
              Tool budget<template v-if="evicted > 0"> ({{ evicted }} left out)</template>
            </span>
            <span class="font-mono tabular-nums text-ink">{{ fmt(composition.tools) }} / {{ fmt(budget) }}</span>
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
