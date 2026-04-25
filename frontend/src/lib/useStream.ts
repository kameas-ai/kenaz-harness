/**
 * useStream — typed wrapper for Wails event subscriptions. The Go-side
 * streamBroker (WP11) emits one event per `(view-name):(event-kind)`
 * topic; this composable subscribes via window.runtime.EventsOn and
 * surfaces a buffered, rAF-throttled list of events for streaming
 * smoothness (FR-014, NFR-004, plan §8 R-5).
 */

import { onBeforeUnmount, ref, type Ref, shallowRef } from 'vue';

export interface UseStreamOptions {
  /** Maximum buffer size before old events are dropped. Default 1000. */
  maxBuffer?: number;
}

export interface UseStreamResult<T> {
  events: Ref<readonly T[]>;
  closed: Ref<boolean>;
  reason: Ref<string | null>;
  unsubscribe(): void;
}

/**
 * Subscribe to a Wails event topic. The closed-payload `<view>:stream-closed`
 * is emitted by the Go-side broker; consumers should subscribe to both
 * topics simultaneously if they care about close reason.
 */
export function useStream<T>(
  topic: string,
  closedTopic: string | null = null,
  opts: UseStreamOptions = {},
): UseStreamResult<T> {
  const max = opts.maxBuffer ?? 1000;
  const events = shallowRef<readonly T[]>([]);
  const closed = ref(false);
  const reason = ref<string | null>(null);

  let pending: T[] = [];
  let scheduled = false;

  function flush() {
    scheduled = false;
    if (pending.length === 0) return;
    const merged = events.value.concat(pending);
    pending = [];
    events.value = merged.length > max ? merged.slice(merged.length - max) : merged;
  }

  function schedule() {
    if (scheduled) return;
    scheduled = true;
    if (typeof requestAnimationFrame !== 'undefined') {
      requestAnimationFrame(flush);
    } else {
      setTimeout(flush, 16);
    }
  }

  let off: (() => void) | undefined;
  let offClosed: (() => void) | undefined;

  if (typeof window !== 'undefined' && window.runtime?.EventsOn) {
    off = window.runtime.EventsOn(topic, (payload) => {
      pending.push(payload as T);
      schedule();
    });
    if (closedTopic) {
      offClosed = window.runtime.EventsOn(closedTopic, (payload) => {
        closed.value = true;
        const p = payload as { reason?: string; message?: string };
        reason.value = p?.reason ?? null;
      });
    }
  }

  function unsubscribe() {
    if (off) off();
    if (offClosed) offClosed();
  }

  onBeforeUnmount(unsubscribe);

  return { events, closed, reason, unsubscribe };
}
