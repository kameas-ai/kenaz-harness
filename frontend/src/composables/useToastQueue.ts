/**
 * useToastQueue — minimal singleton toast queue for one-shot status
 * surfaces (e.g. "Saved as artifact: <title>" with Undo).
 *
 * Behaviour:
 *   - push(message, opts?) appends a toast and returns its id.
 *   - Toasts auto-dismiss after `durationMs` (default 5000).
 *   - Optional `undoFn` shows an "Undo" link; invoking it removes the
 *     toast immediately and runs the callback.
 *   - Optional `actions` array provides labeled action buttons beyond
 *     the Undo shortcut — e.g. "Merge now", "View details".
 *   - dismiss(id) removes a toast manually.
 *
 * Mounted by `ToastRoot.vue` near the App root. `CodeBlock.vue` (and
 * other emitters) push directly via this composable; the `md-toast`
 * `CustomEvent` previously dispatched on `document` is also relayed
 * into this queue by `ToastRoot.vue` so callers mounted outside the
 * Vue tree (the `createApp(...).mount(host)` path) keep working.
 *
 * WP02: extended with `actions` + `level` to support migrated bespoke
 * toast components (CostThresholdToast, MergeSuggestionToast, etc.).
 */
import { reactive, readonly } from 'vue';

/** An action button shown inside a toast alongside the dismiss button. */
export interface ToastAction {
  label: string;
  /** Called when the action button is clicked. */
  perform: () => void | Promise<void>;
  /** When true, clicking the action also dismisses the toast. Default true. */
  dismissAfter?: boolean;
}

export interface ToastEntry {
  id: number;
  message: string;
  undoFn?: () => void | Promise<void>;
  /** Additional labeled action buttons rendered after the message. */
  actions?: ToastAction[];
  /** ms until auto-dismiss; 0 disables auto-dismiss. */
  durationMs: number;
  /** Severity hint for styling: 'info' | 'warn' | 'error'. Default 'info'. */
  level?: 'info' | 'warn' | 'error';
}

interface ToastState {
  toasts: ToastEntry[];
}

const state = reactive<ToastState>({ toasts: [] });
const timers = new Map<number, ReturnType<typeof setTimeout>>();
let nextId = 1;

function clearTimer(id: number) {
  const t = timers.get(id);
  if (t) {
    clearTimeout(t);
    timers.delete(id);
  }
}

export function dismiss(id: number) {
  clearTimer(id);
  const idx = state.toasts.findIndex((t) => t.id === id);
  if (idx >= 0) state.toasts.splice(idx, 1);
}

export function push(
  message: string,
  opts: {
    undoFn?: () => void | Promise<void>;
    actions?: ToastAction[];
    durationMs?: number;
    level?: 'info' | 'warn' | 'error';
  } = {},
): number {
  const id = nextId++;
  const entry: ToastEntry = {
    id,
    message,
    undoFn: opts.undoFn,
    actions: opts.actions,
    durationMs: opts.durationMs ?? 5000,
    level: opts.level ?? 'info',
  };
  state.toasts.push(entry);
  if (entry.durationMs > 0) {
    const handle = setTimeout(() => dismiss(id), entry.durationMs);
    timers.set(id, handle);
  }
  return id;
}

/**
 * invokeAction calls the named action on a toast by label and optionally
 * dismisses it. Used by ToastRoot.vue to handle action button clicks.
 */
export async function invokeAction(id: number, label: string) {
  const entry = state.toasts.find((t) => t.id === id);
  if (!entry?.actions) return;
  const action = entry.actions.find((a) => a.label === label);
  if (!action) return;
  const shouldDismiss = action.dismissAfter !== false;
  if (shouldDismiss) dismiss(id);
  try {
    await action.perform();
  } catch {
    // Swallow — action failures are best-effort.
  }
}

export async function invokeUndo(id: number) {
  const entry = state.toasts.find((t) => t.id === id);
  if (!entry) return;
  const fn = entry.undoFn;
  dismiss(id);
  if (fn) {
    try {
      await fn();
    } catch {
      // Swallow — undo is best-effort.
    }
  }
}

/** Reset state — test-only helper. */
export function _resetToastQueue() {
  for (const id of Array.from(timers.keys())) clearTimer(id);
  state.toasts.length = 0;
  nextId = 1;
}

export function useToastQueue() {
  return {
    toasts: readonly(state.toasts),
    push,
    dismiss,
    invokeUndo,
    invokeAction,
  };
}
