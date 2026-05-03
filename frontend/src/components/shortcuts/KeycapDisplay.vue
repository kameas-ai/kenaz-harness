<script setup lang="ts">
/**
 * KeycapDisplay — renders a keyboard shortcut binding string using
 * platform-appropriate symbols (keyboard-shortcuts-settings-01KQ8TDR
 * plan §2.5).
 *
 * Mac:      ⌘⌥⇧⌃ + key glyph
 * Win/Linux: Ctrl+Alt+Shift+Key as text
 *
 * Usage:
 *   <KeycapDisplay binding="Cmd+Shift+K" />
 *   <KeycapDisplay binding="" /> → renders "—"
 */
import { computed } from 'vue';
import { formatBinding } from '@/lib/shortcuts/platform';

const props = defineProps<{
  /** Canonical binding string (e.g. `Cmd+Shift+K`) or empty for unset. */
  binding: string;
}>();

const parts = computed<string[]>(() => {
  if (!props.binding || props.binding.trim() === '') return [];
  const originalParts = props.binding.split('+');
  if (originalParts.length <= 1) {
    // Single token (e.g. 'Escape', '?') — render as one <kbd>.
    return [formatBinding(props.binding)];
  }
  // Multi-token: format each original part individually so the symbol
  // map applies per-token (e.g. 'Cmd' → '⌘', 'K' → 'K').
  return originalParts.map((token) => formatBinding(token));
});

const isEmpty = computed(() => props.binding === '' || props.binding == null);
</script>

<template>
  <span
    v-if="isEmpty"
    class="font-mono text-[11px] text-ink-dim"
    aria-label="unbound"
  >
    —
  </span>
  <span v-else class="inline-flex items-center gap-0.5" :aria-label="binding">
    <kbd
      v-for="(part, i) in parts"
      :key="i"
      class="inline-flex items-center justify-center rounded border border-border bg-surface-2 px-1 py-0.5 font-mono text-[11px] text-ink leading-none"
    >{{ part }}</kbd>
  </span>
</template>
