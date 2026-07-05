/**
 * useServedEvents — a lightweight in-process pub/sub bus for served mode.
 *
 * In desktop (Wails) mode, `useEventStream` receives events via
 * `window.runtime.EventsOn`. In served mode, that bridge is absent.
 * This module provides a thin shim that:
 *
 *  1. Lets `harnessClient.ts` dispatch events from WS frames into this bus.
 *  2. Lets `useEventStream` subscribe to this bus as a fallback when
 *     `window.runtime` is not available (FR-007: elicitation reconnect).
 *
 * The bus is module-level (process-scoped) and intentionally simple —
 * it is not reactive and carries no Vue dependency tracking. Handlers
 * receive payloads synchronously in dispatch order.
 *
 * Usage:
 *   // Publisher (harnessClient.ts WS frame handler):
 *   import { dispatchServedEvent } from './useServedEvents';
 *   dispatchServedEvent('elicit:pending', payload);
 *
 *   // Subscriber (useEventStream.ts):
 *   import { onServedEvent, offServedEvent } from './useServedEvents';
 *   const off = onServedEvent('elicit:pending', cb);
 *   // later:
 *   off(); // or offServedEvent(id)
 */

export type ServedEventHandler = (payload: unknown) => void;

/** Internal registry: topic → Map<id, handler> */
const _registry = new Map<string, Map<number, ServedEventHandler>>();
let _seq = 0;

/**
 * onServedEvent — subscribe to a topic.
 * Returns a cancel function that removes this handler.
 */
export function onServedEvent(
  topic: string,
  handler: ServedEventHandler,
): () => void {
  let handlers = _registry.get(topic);
  if (!handlers) {
    handlers = new Map();
    _registry.set(topic, handlers);
  }
  const id = ++_seq;
  handlers.set(id, handler);
  return () => {
    const h = _registry.get(topic);
    if (h) h.delete(id);
  };
}

/**
 * dispatchServedEvent — publish a payload to all registered handlers for
 * the given topic. Called from the WS frame handler in harnessClient.ts.
 */
export function dispatchServedEvent(topic: string, payload: unknown): void {
  const handlers = _registry.get(topic);
  if (!handlers) return;
  for (const handler of handlers.values()) {
    try {
      handler(payload);
    } catch {
      // Suppress handler errors so one bad subscriber can't break others.
    }
  }
}
