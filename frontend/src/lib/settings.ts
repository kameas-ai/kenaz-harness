/**
 * settings — debounced (250 ms) write coalescing for the persisted UI
 * state shape (plan §5.5). The single JSON file at
 * $USER_CONFIG_DIR/kenaz-harness/settings.json is owned by the Go side
 * (core/rpc/settings.go); this module is the TS-side debounce wrapper.
 */

import type { HarnessClient } from './harnessClient';
import type { Settings } from './types';

const DEBOUNCE_MS = 250;

let pending: Settings | null = null;
let timer: ReturnType<typeof setTimeout> | null = null;

export function debouncedSave(client: HarnessClient, s: Settings): void {
  pending = s;
  if (timer) clearTimeout(timer);
  timer = setTimeout(() => {
    const out = pending;
    pending = null;
    timer = null;
    if (out) {
      void client.settings.set(out).catch(() => {});
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
