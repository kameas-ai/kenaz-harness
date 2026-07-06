/**
 * settings — debounced (250 ms) write coalescing for the persisted UI
 * state shape (plan §5.5). The single JSON file at
 * $USER_CONFIG_DIR/kenaz-harness/settings.json is owned by the Go side
 * (core/rpc/settings.go); this module is the TS-side debounce wrapper.
 *
 * (silent-failure-elimination-QQ5GXW50 WP02 / FR-002)
 * debouncedSave routes writes through the optimistic-write failure pathway:
 * a failed settings write surfaces an error toast and does NOT silently
 * report success while leaving persisted state inconsistent.  The caller
 * must supply the current (pre-change) Settings snapshot as `revertTo`
 * when an optimistic revert is desired on failure; omitting it means the
 * caller manages its own revert.
 */

import type { HarnessClient } from './harnessClient';
import type { Settings } from './types';
import { runAsyncAction } from '@/composables/useAsyncAction';

const DEBOUNCE_MS = 250;

let pending: Settings | null = null;
let pendingRevert: (() => void) | null = null;
let timer: ReturnType<typeof setTimeout> | null = null;

/**
 * debouncedSave coalesces rapid settings writes.  On flush, a failed write
 * surfaces an error toast via the shared optimistic-write failure pathway
 * (FR-002) instead of swallowing the error silently.
 *
 * @param client    HarnessClient with a settings.set binding.
 * @param s         The Settings value to persist.
 * @param revertFn  Optional callback invoked if the write fails.  Use this
 *                  to snap the UI back to the previously-persisted value.
 */
export function debouncedSave(
  client: HarnessClient,
  s: Settings,
  revertFn?: () => void,
): void {
  pending = s;
  pendingRevert = revertFn ?? null;
  if (timer) clearTimeout(timer);
  timer = setTimeout(() => {
    const out = pending;
    const revert = pendingRevert;
    pending = null;
    pendingRevert = null;
    timer = null;
    if (out) {
      void runAsyncAction({
        revert: revert ?? undefined,
        action: () => client.settings.set(out),
        errorLabel: 'Save settings',
      });
    }
  }, DEBOUNCE_MS);
}

/** Test helper: flush any pending save synchronously. */
export function _flushForTest(): Settings | null {
  if (timer) clearTimeout(timer);
  const out = pending;
  pending = null;
  timer = null;
  return out;
}
