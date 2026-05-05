/**
 * useStreamAbort.ts — composable that enforces the 5 MiB / 2-second cap
 * on artifact preview rendering (artifact-preview-binary-rendering-01KQ8TD5
 * WP06, FR-009).
 *
 * Usage:
 *   const { signal, tripBytes, tripTime, reason } = useStreamAbort({
 *     maxBytes: 5 * 1024 * 1024,
 *     timeoutMs: 2000,
 *   });
 *
 * Options may be plain numbers or getter functions (() => number) to allow
 * reactive configuration loaded asynchronously (e.g. from the settings RPC).
 *
 * - Call `tripBytes(n)` with the decoded byte count before handing bytes to a
 *   renderer. Returns true if the cap was exceeded.
 * - Call `tripTime()` to abort due to a timeout (e.g. from a stalled element).
 * - `reason` is a ref that is null until one of the trips fires.
 * - The composable wires `onBeforeUnmount` to revoke any object-URL passed in
 *   via `registerObjectUrl`.
 */

import { ref, onBeforeUnmount } from 'vue';

export type AbortReason = 'size' | 'time';

export interface StreamAbortOptions {
  /** Maximum decoded byte count allowed. Getter supported for reactive config. */
  maxBytes: number | (() => number);
  /** Timeout in milliseconds. Getter supported for reactive config. */
  timeoutMs: number | (() => number);
}

export interface StreamAbortHandle {
  signal: AbortSignal;
  /** Call with decoded byte count. Returns true if cap exceeded and abort was triggered. */
  tripBytes(n: number): boolean;
  /** Call when the renderer detects a timeout condition. Returns true if abort was triggered. */
  tripTime(): boolean;
  /** Reactive reason, null until tripped. */
  reason: ReturnType<typeof ref<AbortReason | null>>;
  /** Register an object URL to be revoked when the composable is unmounted or aborted. */
  registerObjectUrl(url: string): void;
}

export function useStreamAbort(opts: StreamAbortOptions): StreamAbortHandle {
  const controller = new AbortController();
  const reason = ref<AbortReason | null>(null);
  const objectUrls: string[] = [];

  function resolveMaxBytes(): number {
    return typeof opts.maxBytes === 'function' ? opts.maxBytes() : opts.maxBytes;
  }

  function revokeAll() {
    for (const u of objectUrls) {
      try {
        URL.revokeObjectURL(u);
      } catch {
        // ignore
      }
    }
    objectUrls.length = 0;
  }

  function abortWith(r: AbortReason) {
    if (reason.value !== null) return false; // already tripped
    reason.value = r;
    controller.abort();
    revokeAll();
    return true;
  }

  function tripBytes(n: number): boolean {
    if (n > resolveMaxBytes()) {
      return abortWith('size');
    }
    return false;
  }

  function tripTime(): boolean {
    return abortWith('time');
  }

  function registerObjectUrl(url: string): void {
    objectUrls.push(url);
  }

  onBeforeUnmount(() => {
    revokeAll();
  });

  return {
    signal: controller.signal,
    tripBytes,
    tripTime,
    reason,
    registerObjectUrl,
  };
}
