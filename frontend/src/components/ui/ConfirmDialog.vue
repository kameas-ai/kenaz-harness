<script setup lang="ts">
/**
 * ConfirmDialog — shared confirmation dialog for destructive actions (FR-003 / WP03).
 *
 * Replaces window.confirm() calls in user flows.
 *
 * Usage (imperative via useConfirmDialog):
 *   const { confirm } = useConfirmDialog();
 *   const confirmed = await confirm({ title: 'Delete session?', danger: true });
 *   if (confirmed) { ... }
 *
 * Or declarative:
 *   <ConfirmDialog
 *     :open="showConfirm"
 *     title="Delete session?"
 *     message="This cannot be undone."
 *     :danger="true"
 *     @confirm="doDelete"
 *     @cancel="showConfirm = false"
 *   />
 */

import BaseDialog from '@/components/ui/BaseDialog.vue';
import Button from '@/components/ui/Button.vue';

defineProps<{
  open: boolean;
  title: string;
  message?: string;
  confirmLabel?: string;
  cancelLabel?: string;
  /** When true, the confirm button uses danger styling. */
  danger?: boolean;
}>();

const emit = defineEmits<{
  (e: 'confirm'): void;
  (e: 'cancel'): void;
}>();
</script>

<template>
  <BaseDialog
    :open="open"
    :title="title"
    :close-on-overlay-click="true"
    panel-class="w-full max-w-sm rounded-md border border-border-muted bg-surface-1 p-5 shadow-xl"
    @close="emit('cancel')"
  >
    <div>
      <h2 class="font-ui text-base font-semibold text-ink" data-testid="confirm-dialog-title">
        {{ title }}
      </h2>
      <p v-if="message" class="mt-2 font-ui text-sm text-ink-muted" data-testid="confirm-dialog-message">
        {{ message }}
      </p>
      <div class="mt-5 flex justify-end gap-2">
        <Button
          variant="ghost"
          data-testid="confirm-dialog-cancel"
          @click="emit('cancel')"
        >
          {{ cancelLabel ?? 'Cancel' }}
        </Button>
        <Button
          :variant="danger ? 'danger' : 'accent'"
          data-testid="confirm-dialog-confirm"
          @click="emit('confirm')"
        >
          {{ confirmLabel ?? (danger ? 'Delete' : 'Confirm') }}
        </Button>
      </div>
    </div>
  </BaseDialog>
</template>
