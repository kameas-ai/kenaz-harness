/**
 * useConfirmDialog — imperative API for showing a ConfirmDialog (FR-003 / WP03).
 *
 * Usage:
 *   // In component script:
 *   const { confirmState, confirm } = useConfirmDialog();
 *
 *   // In component template:
 *   <ConfirmDialog
 *     v-bind="confirmState"
 *     @confirm="confirmState.resolve(true)"
 *     @cancel="confirmState.resolve(false)"
 *   />
 *
 *   // In a handler:
 *   const ok = await confirm({ title: 'Delete?', danger: true });
 *   if (ok) { ... }
 */
import { reactive } from 'vue';

export interface ConfirmOptions {
  title: string;
  message?: string;
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
}

export interface ConfirmState extends ConfirmOptions {
  open: boolean;
  resolve: (value: boolean) => void;
}

export function useConfirmDialog() {
  const confirmState = reactive<ConfirmState>({
    open: false,
    title: '',
    message: undefined,
    confirmLabel: undefined,
    cancelLabel: undefined,
    danger: false,
    resolve: (_v: boolean) => {},
  });

  function confirm(opts: ConfirmOptions): Promise<boolean> {
    return new Promise((resolve) => {
      confirmState.open = true;
      confirmState.title = opts.title;
      confirmState.message = opts.message;
      confirmState.confirmLabel = opts.confirmLabel;
      confirmState.cancelLabel = opts.cancelLabel;
      confirmState.danger = opts.danger ?? false;
      confirmState.resolve = (value: boolean) => {
        confirmState.open = false;
        resolve(value);
      };
    });
  }

  return { confirmState, confirm };
}
