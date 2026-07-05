/**
 * useAsyncAction — shared optimistic-write failure pathway (FR-001).
 *
 * Takes an async action that may fail, applies an optimistic state change
 * before the call, and reverts it on rejection while surfacing a toast
 * with a friendly error message.
 *
 * Usage pattern:
 *
 *   const { run } = useAsyncAction();
 *
 *   async function toggle() {
 *     await run({
 *       optimistic: () => { localState.value = !localState.value; },
 *       revert:     () => { localState.value = !localState.value; },
 *       action:     () => client.settings.set(updatedSettings),
 *       errorLabel: 'Save settings',
 *     });
 *   }
 *
 * Contract:
 * - `optimistic` is called synchronously before the action (may be omitted
 *   for non-optimistic callers).
 * - On rejection: `revert` is called (if provided), then a toast is pushed
 *   with a friendly message derived from the error.
 * - On resolution: no toast is emitted.
 * - The promise returned by `run` never rejects — errors are handled
 *   internally. Callers can await it to know when the action settled.
 *
 * (silent-failure-elimination-QQ5GXW50 WP01 / FR-001)
 */
import { push } from '@/composables/useToastQueue';

function toErrorString(err: unknown): string {
  if (!err) return 'An unexpected error occurred.';
  if (typeof err === 'string') return err;
  if (err instanceof Error) return err.message;
  return String(err);
}

/** Derive a user-facing message from a raw error. */
function friendlyMessage(err: unknown, label: string): string {
  const raw = toErrorString(err);
  // If the error is already short and actionable, surface it.
  // Otherwise fall back to a generic message with the action label.
  if (raw && raw.length < 200) {
    return `${label} failed: ${raw}`;
  }
  return `${label} failed. Please try again.`;
}

export interface AsyncActionOptions<T = void> {
  /** Called synchronously before the action to apply the optimistic state. */
  optimistic?: () => void;
  /** Called on rejection to revert the optimistic state. */
  revert?: () => void;
  /** The async action to perform. */
  action: () => Promise<T>;
  /** Short human label for the operation, used in the error toast message. */
  errorLabel: string;
  /**
   * Optional custom toast message on failure.
   * When absent, `friendlyMessage(err, errorLabel)` is used.
   */
  toastMessage?: (err: unknown) => string;
  /**
   * Optional undo function attached to the error toast.
   * Useful for security-significant toggles where the user may want to
   * acknowledge the failure state or manually re-attempt.
   */
  undoFn?: () => void | Promise<void>;
}

export interface AsyncActionResult<T = void> {
  /** The resolved value from `action`, or undefined if it rejected. */
  value: T | undefined;
  /** True when `action` rejected. */
  error: boolean;
  /** The error that caused the rejection, or null. */
  err: unknown;
}

/**
 * runAsyncAction is the stateless helper. Prefer useAsyncAction() inside
 * components and composables; call runAsyncAction directly from module-level
 * code (e.g. settings.ts).
 */
export async function runAsyncAction<T = void>(
  opts: AsyncActionOptions<T>,
): Promise<AsyncActionResult<T>> {
  opts.optimistic?.();
  try {
    const value = await opts.action();
    return { value, error: false, err: null };
  } catch (err: unknown) {
    opts.revert?.();
    const msg = opts.toastMessage
      ? opts.toastMessage(err)
      : friendlyMessage(err, opts.errorLabel);
    push(msg, { undoFn: opts.undoFn, durationMs: 7000 });
    return { value: undefined, error: true, err };
  }
}

/**
 * useAsyncAction returns a stable `run` function bound to the toast queue.
 * Equivalent to calling `runAsyncAction` directly — provided as a composable
 * hook for component-style usage and testability.
 */
export function useAsyncAction() {
  return { run: runAsyncAction };
}

/** Friendly message accessor exposed for tests. */
export { friendlyMessage };
