<script setup lang="ts">
/**
 * PinMenu — small home-grown popover that surfaces the three scope
 * choices for the 📌 pin button in MessageBubble.
 *
 * Layered behaviour (WP06 T005):
 *   - Three option rows: "Pin to session", "Pin to project",
 *     "Pin to global". Selecting one emits `pick(scope)`; the parent
 *     issues the RPC and flashes the confirmation badge.
 *   - "Pin to project" is disabled when `hasProject` is false; a
 *     tooltip explains "session is not in a project".
 *   - Keyboard-navigable: ArrowUp / ArrowDown move the highlight,
 *     Enter confirms the highlighted option, Escape closes the menu.
 *   - Click-outside / blur closes the menu (parent owns `open`).
 *
 * Visual register matches the existing model-switcher dropdown in
 * SessionsView.vue (surface-1 background, border-muted, shadow-lg).
 * No raw color literals — privacy CI invariant #4.
 */

import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue';
import type { MemoryScopeKind } from '@/lib/types';

const props = defineProps<{
  open: boolean;
  /**
   * When false, the "Pin to project" row is disabled and shows a
   * tooltip pointing the user at the session's project membership.
   */
  hasProject: boolean;
}>();

const emit = defineEmits<{
  (e: 'pick', scope: MemoryScopeKind): void;
  (e: 'close'): void;
}>();

interface Option {
  scope: MemoryScopeKind;
  label: string;
  glyph: string;
  disabled: boolean;
  title?: string;
}

const options = computed<Option[]>(() => [
  {
    scope: 'session',
    label: 'Pin to session',
    glyph: '💬',
    disabled: false,
  },
  {
    scope: 'project',
    label: 'Pin to project',
    glyph: '📁',
    disabled: !props.hasProject,
    title: props.hasProject
      ? undefined
      : 'session is not in a project',
  },
  {
    scope: 'global',
    label: 'Pin to global',
    glyph: '🌐',
    disabled: false,
  },
]);

// Highlighted index for keyboard nav. Defaults to the first
// non-disabled option each time the menu opens so Enter on the
// freshly-opened menu always pins to session (the legacy default).
const highlight = ref(0);

function firstEnabled(): number {
  const idx = options.value.findIndex((o) => !o.disabled);
  return idx === -1 ? 0 : idx;
}

watch(
  () => props.open,
  (isOpen) => {
    if (isOpen) {
      highlight.value = firstEnabled();
      void nextTick(() => {
        rootEl.value?.focus();
      });
    }
  },
);

const rootEl = ref<HTMLElement | null>(null);

function pickAt(index: number) {
  const opt = options.value[index];
  if (!opt || opt.disabled) return;
  emit('pick', opt.scope);
}

function move(delta: 1 | -1) {
  const len = options.value.length;
  for (let step = 0; step < len; step++) {
    const next = (highlight.value + delta * (step + 1) + len) % len;
    if (!options.value[next].disabled) {
      highlight.value = next;
      return;
    }
  }
}

function onKeydown(event: KeyboardEvent) {
  if (!props.open) return;
  switch (event.key) {
    case 'ArrowDown':
      event.preventDefault();
      move(1);
      break;
    case 'ArrowUp':
      event.preventDefault();
      move(-1);
      break;
    case 'Enter':
      event.preventDefault();
      pickAt(highlight.value);
      break;
    case 'Escape':
      event.preventDefault();
      emit('close');
      break;
  }
}

function onDocumentPointerDown(event: PointerEvent) {
  if (!props.open) return;
  const root = rootEl.value;
  if (!root) return;
  const target = event.target as Node | null;
  if (target && root.contains(target)) return;
  emit('close');
}

if (typeof window !== 'undefined') {
  window.addEventListener('pointerdown', onDocumentPointerDown, true);
}

onBeforeUnmount(() => {
  if (typeof window !== 'undefined') {
    window.removeEventListener('pointerdown', onDocumentPointerDown, true);
  }
});
</script>

<template>
  <div
    v-if="open"
    ref="rootEl"
    class="absolute right-0 top-full z-30 mt-1 w-52 rounded-sm border border-border-muted bg-surface-1 shadow-lg focus:outline-none"
    role="menu"
    aria-label="Pin scope menu"
    tabindex="-1"
    data-testid="pin-menu"
    @keydown="onKeydown"
  >
    <button
      v-for="(opt, idx) in options"
      :key="opt.scope"
      type="button"
      role="menuitem"
      class="block w-full px-3 py-1.5 text-left font-ui text-[12px]"
      :class="[
        opt.disabled
          ? 'text-ink-dim opacity-60 cursor-not-allowed'
          : 'text-ink hover:bg-surface-2',
        highlight === idx && !opt.disabled ? 'bg-surface-2 text-accent' : '',
      ]"
      :disabled="opt.disabled"
      :title="opt.title"
      :aria-disabled="opt.disabled"
      :data-testid="`pin-menu-${opt.scope}`"
      @click="pickAt(idx)"
      @mouseenter="opt.disabled ? null : (highlight = idx)"
    >
      <span class="mr-2" aria-hidden="true">{{ opt.glyph }}</span>
      <span>{{ opt.label }}</span>
    </button>
  </div>
</template>
