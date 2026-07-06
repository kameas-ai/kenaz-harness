/**
 * useEventStream — typed Vue composable wrapping a single Wails event
 * topic. Mirrors the Kenaz `runtime.EventsOn(topic, cb)` /
 * `runtime.EventsOff(topic)` lifecycle and auto-resubscribes when the
 * bridge reconnects (the Shell's connection state machine).
 *
 * Differs from `useStream` (lib/useStream.ts) which buffers an array of
 * events with rAF batching. `useEventStream` is intentionally low-level:
 * it forwards each payload to a caller-supplied handler so chat surfaces
 * can splice deltas into a streaming buffer without touching shallowRefs.
 *
 * Usage:
 *   const { latest, count, unsubscribe } = useEventStream<StreamChunk>(
 *     'llm:stream-chunk',
 *     (chunk) => { buffer.value += chunk.delta; },
 *   );
 *
 * Cleanup is automatic on component unmount; tests can call
 * `unsubscribe()` manually.
 */

import { onBeforeUnmount, ref, type Ref, watch } from 'vue';
import { useConnectionState } from './useConnectionState';
import { onServedEvent } from './useServedEvents';

export interface UseEventStreamResult<T> {
  /** Last payload received on the topic, or null. */
  latest: Ref<T | null>;
  /** Total event count since subscription. */
  count: Ref<number>;
  /** True while the underlying Wails subscription is wired. */
  active: Ref<boolean>;
  /** Manual cleanup (auto-called on unmount). */
  unsubscribe(): void;
}

export type StreamHandler<T> = (payload: T) => void;

interface RuntimeShape {
  EventsOn?: (topic: string, cb: (payload: unknown) => void) => () => void;
  EventsOff?: (topic: string) => void;
}

function getRuntime(): RuntimeShape | null {
  if (typeof window === 'undefined') return null;
  const r = (window as unknown as { runtime?: RuntimeShape }).runtime;
  return r ?? null;
}

export function useEventStream<T>(
  topic: string,
  handler?: StreamHandler<T>,
): UseEventStreamResult<T> {
  const latest = ref<T | null>(null) as Ref<T | null>;
  const count = ref(0);
  const active = ref(false);

  let off: (() => void) | undefined;
  const connection = useConnectionState();

  function attach() {
    if (off) return;
    const rt = getRuntime();
    if (rt?.EventsOn) {
      // Wails desktop path: wire the native event bridge.
      off = rt.EventsOn(topic, (payload) => {
        const typed = payload as T;
        latest.value = typed;
        count.value += 1;
        if (handler) handler(typed);
      });
    } else {
      // Served mode fallback: subscribe to the in-process served-event bus
      // that is populated by the WS frame handler in harnessClient.ts.
      // Callers receive events identically to the Wails path (FR-007).
      off = onServedEvent(topic, (payload) => {
        const typed = payload as T;
        latest.value = typed;
        count.value += 1;
        if (handler) handler(typed);
      });
    }
    active.value = true;
  }

  function detach() {
    if (off) {
      off();
      off = undefined;
    }
    active.value = false;
  }

  attach();

  // Auto-resubscribe on bridge reconnect: when connection flips back to
  // 'ready' we reattach. Wails-side EventsOff is a no-op for our purposes;
  // the broker emits the new subscription topic after reconnect.
  const stopWatch = watch(
    () => connection.value,
    (state, prev) => {
      if (state === 'ready' && prev !== 'ready') {
        detach();
        attach();
      }
      if (state === 'lost') {
        detach();
      }
    },
  );

  function unsubscribe() {
    stopWatch();
    detach();
  }

  onBeforeUnmount(unsubscribe);

  return { latest, count, active, unsubscribe };
}
