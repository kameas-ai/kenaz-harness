<script setup lang="ts">
/**
 * BaseDialog — shared modal primitive (FR-001 / WP01).
 *
 * Provides:
 *   - focus trap: Tab cycles only within the dialog panel
 *   - Escape-to-close (calls onClose)
 *   - initial focus: first focusable child, or the panel itself
 *   - return focus: restores focus to the element that opened the dialog
 *   - overlay click closes (unless closeOnOverlayClick=false)
 *   - aria role="dialog" + aria-modal="true"
 *
 * Native-smoke note (2026-07-05): BaseDialog is the fix for the confirmed
 * defect in which the New-Session dialog did not close on Escape in the
 * native .app build. All hand-rolled overlays that adopt BaseDialog inherit
 * the Escape-to-close behaviour automatically.
 *
 * Usage:
 *   <BaseDialog :open="open" :title="'My dialog'" @close="open = false">
 *     <p>Content here</p>
 *   </BaseDialog>
 */

import {
  computed,
  nextTick,
  onBeforeUnmount,
  ref,
  watch,
} from 'vue';

const props = withDefaults(
  defineProps<{
    /** Whether the dialog is visible. */
    open: boolean;
    /** Accessible title — rendered as aria-label when no slot title is used. */
    title?: string;
    /** Extra CSS class applied to the panel (for width/height overrides). */
    panelClass?: string;
    /** z-index class; defaults to z-50 */
    zClass?: string;
    /** Whether clicking the overlay backdrop closes the dialog. Default true. */
    closeOnOverlayClick?: boolean;
  }>(),
  {
    title: undefined,
    panelClass: undefined,
    zClass: 'z-50',
    closeOnOverlayClick: true,
  },
);

const emit = defineEmits<{
  (e: 'close'): void;
}>();

// ── refs ──────────────────────────────────────────────────────────────
const panelRef = ref<HTMLDivElement | null>(null);
/** The element that had focus before the dialog opened. */
let returnFocusEl: Element | null = null;

// ── focus trap helpers ────────────────────────────────────────────────
const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

function getFocusable(): HTMLElement[] {
  return Array.from(panelRef.value?.querySelectorAll<HTMLElement>(FOCUSABLE) ?? []);
}

function trapFocus(e: KeyboardEvent) {
  if (!panelRef.value) return;
  const focusable = getFocusable();
  if (focusable.length === 0) {
    e.preventDefault();
    return;
  }
  const first = focusable[0];
  const last = focusable[focusable.length - 1];
  const active = document.activeElement;

  if (e.shiftKey) {
    if (active === first || !panelRef.value.contains(active)) {
      e.preventDefault();
      last.focus();
    }
  } else {
    if (active === last || !panelRef.value.contains(active)) {
      e.preventDefault();
      first.focus();
    }
  }
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    e.preventDefault();
    emit('close');
  } else if (e.key === 'Tab') {
    trapFocus(e);
  }
}

// ── lifecycle ─────────────────────────────────────────────────────────
async function onOpen() {
  returnFocusEl = document.activeElement;
  document.addEventListener('keydown', onKeydown, true);
  await nextTick();
  // Focus the first focusable element, or the panel itself.
  const focusable = getFocusable();
  if (focusable.length > 0) {
    focusable[0].focus();
  } else {
    panelRef.value?.focus();
  }
}

function onClose() {
  document.removeEventListener('keydown', onKeydown, true);
  // Return focus to the triggering element.
  if (returnFocusEl && typeof (returnFocusEl as HTMLElement).focus === 'function') {
    (returnFocusEl as HTMLElement).focus();
  }
  returnFocusEl = null;
}

watch(
  () => props.open,
  (open) => {
    if (open) {
      void onOpen();
    } else {
      onClose();
    }
  },
  { immediate: true },
);

onBeforeUnmount(() => {
  if (props.open) onClose();
});

const overlayClass = computed(() =>
  ['fixed inset-0 flex items-center justify-center', props.zClass].join(' '),
);

function onOverlayClick() {
  if (props.closeOnOverlayClick) {
    emit('close');
  }
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open"
      :class="overlayClass"
      role="dialog"
      aria-modal="true"
      :aria-label="title"
    >
      <!-- Backdrop -->
      <div
        class="absolute inset-0 bg-modal-overlay"
        aria-hidden="true"
        @click="onOverlayClick"
      />
      <!-- Panel -->
      <div
        ref="panelRef"
        :class="['relative', panelClass]"
        tabindex="-1"
        @click.stop
      >
        <slot />
      </div>
    </div>
  </Teleport>
</template>
