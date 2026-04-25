<script setup lang="ts">
import { computed, ref, watch, nextTick } from 'vue';
import { useCommandPalette } from '@/lib/useCommandPalette';

/**
 * CommandPalette — Cmd/Ctrl+K modal palette (FR-010). Implements the
 * keyboard binding via useCommandPalette and renders a simple list +
 * filter. Radix-Vue's Dialog is the canonical primitive once the
 * shadcn-vue copy lands; this is the lightweight v1.0 surface.
 */
const palette = useCommandPalette();
const filter = ref('');
const inputEl = ref<HTMLInputElement | null>(null);

const filtered = computed(() => {
  const q = filter.value.toLowerCase();
  if (!q) return palette.actions.value;
  return palette.actions.value.filter((a) =>
    a.label.toLowerCase().includes(q),
  );
});

watch(palette.isOpen, (open) => {
  if (open) {
    filter.value = '';
    void nextTick(() => inputEl.value?.focus());
  }
});

async function run(id: string) {
  const a = palette.actions.value.find((x) => x.id === id);
  if (!a) return;
  await a.perform();
  palette.close();
}
</script>

<template>
  <div
    v-if="palette.isOpen.value"
    class="fixed inset-0 z-50 grid place-items-start pt-24"
    role="dialog"
    aria-modal="true"
    aria-label="Command palette"
    @click.self="palette.close()"
  >
    <div class="absolute inset-0 bg-modal-overlay"></div>
    <div
      class="relative w-[480px] max-w-[90vw] rounded-md border border-border-strong bg-surface-2 shadow-lg"
    >
      <div class="px-3 py-2 border-b border-border-muted">
        <input
          ref="inputEl"
          v-model="filter"
          type="text"
          class="w-full bg-transparent font-mono text-sm text-ink outline-none placeholder:text-ink-subtle"
          placeholder="Type a command…"
          aria-label="Filter commands"
        />
      </div>
      <ul class="max-h-72 overflow-y-auto py-1" role="listbox">
        <li v-if="filtered.length === 0" class="px-3 py-2 text-sm text-ink-subtle">
          No matching commands
        </li>
        <li
          v-for="a in filtered"
          :key="a.id"
          class="px-3 py-2 hover:bg-surface-3 cursor-pointer"
          role="option"
          :aria-label="a.label"
          @click="run(a.id)"
        >
          <div class="font-ui text-sm text-ink">{{ a.label }}</div>
          <div v-if="a.hint" class="font-ui text-xs text-ink-subtle">
            {{ a.hint }}
          </div>
        </li>
      </ul>
    </div>
  </div>
</template>
