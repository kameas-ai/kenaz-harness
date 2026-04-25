/**
 * useHarnessAPI — composables built on top of the typed harnessClient.
 * FR-009: components consume composables, never the client directly.
 *
 * `useShellStatus` mirrors Kenaz's KenazClient-style polling hook —
 * polls every 5 s while the window is focused; suspends on blur,
 * resumes on focus. WP12 / plan §4.1.
 */

import {
  ref,
  onMounted,
  onBeforeUnmount,
  type Ref,
  shallowRef,
  readonly,
} from 'vue';
import { useHarnessClient } from './harnessClientContext';
import type {
  Session,
  ShellStatus,
  Denial,
  AuditEntry,
  AuditFilter,
  EventStreamEntry,
} from './types';
import type { HarnessClient } from './harnessClient';

export { useHarnessClient };

const SHELL_POLL_MS = 5000;

const DEFAULT_STATUS: ShellStatus = {
  activeProvider: '—',
  trustTier: 'Local',
  harnessBuild: '0.0.0-dev',
  connection: 'connecting',
  eventRate: 0,
  policyApplied: true,
  redactionOn: true,
  localFirstOn: true,
};

/**
 * useShellStatus — KenazClient-style polling hook for ShellStatus.
 * Polls every 5 s while the window is focused. Returns a readonly ref.
 */
export function useShellStatus(): Readonly<Ref<ShellStatus>> {
  const client = useHarnessClient();
  const status = ref<ShellStatus>({ ...DEFAULT_STATUS });
  let timer: ReturnType<typeof setInterval> | null = null;
  let active = true;

  async function poll() {
    try {
      const next = await client.shellStatus();
      status.value = next;
    } catch {
      status.value = { ...status.value, connection: 'lost' };
    }
  }

  function start() {
    if (timer || !active) return;
    void poll();
    timer = setInterval(() => {
      void poll();
    }, SHELL_POLL_MS);
  }

  function stop() {
    if (timer) {
      clearInterval(timer);
      timer = null;
    }
  }

  function onFocus() {
    start();
  }

  function onBlur() {
    stop();
  }

  onMounted(() => {
    start();
    if (typeof window !== 'undefined') {
      window.addEventListener('focus', onFocus);
      window.addEventListener('blur', onBlur);
    }
  });

  onBeforeUnmount(() => {
    active = false;
    stop();
    if (typeof window !== 'undefined') {
      window.removeEventListener('focus', onFocus);
      window.removeEventListener('blur', onBlur);
    }
  });

  return readonly(status) as Readonly<Ref<ShellStatus>>;
}

/**
 * useSessions — reactive sessions list + CRUD wrappers.
 */
export interface UseSessionsResult {
  list: Ref<readonly Session[]>;
  loading: Ref<boolean>;
  refresh(): Promise<void>;
  create(name: string): Promise<Session>;
  rename(id: string, name: string): Promise<void>;
  remove(id: string): Promise<void>;
}

export function useSessions(): UseSessionsResult {
  const client = useHarnessClient();
  const list = shallowRef<readonly Session[]>([]);
  const loading = ref(false);

  async function refresh() {
    loading.value = true;
    try {
      list.value = await client.sessions.list();
    } catch {
      list.value = [];
    } finally {
      loading.value = false;
    }
  }

  async function create(name: string) {
    const s = await client.sessions.create(name);
    await refresh();
    return s;
  }

  async function rename(id: string, name: string) {
    await client.sessions.rename(id, name);
    await refresh();
  }

  async function remove(id: string) {
    try {
      await client.sessions.delete(id);
    } catch (err) {
      // Self-heal: if the backend says the row already doesn't exist,
      // the rail's view is stale — silently accept and let refresh()
      // sync the UI to reality. Re-throw any other error so the caller
      // can surface it.
      const msg = err instanceof Error ? err.message : String(err);
      const looksLikeNotFound = /not found|notfound|no such/i.test(msg);
      if (!looksLikeNotFound) {
        await refresh();
        throw err;
      }
    }
    await refresh();
  }

  return { list, loading, refresh, create, rename, remove };
}

/**
 * useChatStream — typed subscription for sessions:event topic.
 */
export interface UseStreamResult<T> {
  events: Ref<readonly T[]>;
  paused: Ref<boolean>;
  pause(): void;
  resume(): void;
  stop(): Promise<void>;
}

export function useChatStream(_sessionId: Ref<string>): UseStreamResult<EventStreamEntry> {
  // The full streaming implementation arrives with WP12's useStream
  // composable + WP11's streamBroker. This stub keeps the shape stable
  // for downstream consumers.
  const events = ref<readonly EventStreamEntry[]>([]);
  const paused = ref(false);
  return {
    events,
    paused,
    pause: () => {
      paused.value = true;
    },
    resume: () => {
      paused.value = false;
    },
    stop: async () => undefined,
  };
}

/**
 * useEventLogStream — typed live-stream for the audit view. Bridges the
 * Audit_StartStream RPC to the streamBroker `audit:event` topic and
 * exposes the buffered, pause-able feed. Server-side redaction has
 * already run; payloads here are safe to render verbatim.
 */
export function useEventLogStream(
  filter: Ref<AuditFilter>,
): UseStreamResult<AuditEntry> {
  const client = useHarnessClient();
  const events = ref<readonly AuditEntry[]>([]);
  const paused = ref(false);
  let pending: AuditEntry[] = [];
  let scheduled = false;
  let off: (() => void) | undefined;
  let offClosed: (() => void) | undefined;
  let subscriptionId: string | null = null;

  function flush() {
    scheduled = false;
    if (pending.length === 0 || paused.value) return;
    const merged = events.value.concat(pending);
    pending = [];
    const max = 1000;
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

  function attach() {
    if (typeof window === 'undefined' || !window.runtime?.EventsOn) return;
    off = window.runtime.EventsOn('audit:event', (payload) => {
      pending.push(payload as AuditEntry);
      schedule();
    });
    offClosed = window.runtime.EventsOn('audit:stream-closed', () => {
      // Auto-resubscribe is the consumer's responsibility; we just
      // surface the closed state via the existing reactive ref.
      paused.value = true;
    });
  }

  void (async () => {
    try {
      subscriptionId = await client.audit.startStream(filter.value);
      attach();
    } catch {
      // No-op: a missing broker (test path) leaves events empty.
    }
  })();

  return {
    events,
    paused,
    pause: () => {
      paused.value = true;
    },
    resume: () => {
      paused.value = false;
      schedule();
    },
    stop: async () => {
      if (off) off();
      if (offClosed) offClosed();
      if (subscriptionId) {
        try {
          await client.audit.stopStream(subscriptionId);
        } catch {
          // Already closed by broker — no-op.
        }
        subscriptionId = null;
      }
    },
  };
}

/**
 * usePolicyDecisions — entry point for downstream missions to receive
 * `Denial` objects from policy:event and route them to <DenialNotice>.
 */
export interface UsePolicyDecisionsResult {
  onDenied(cb: (d: Denial) => void): () => void;
}

const policyDeniedHandlers = new Set<(d: Denial) => void>();

export function usePolicyDecisions(): UsePolicyDecisionsResult {
  return {
    onDenied(cb) {
      policyDeniedHandlers.add(cb);
      return () => policyDeniedHandlers.delete(cb);
    },
  };
}

/** Test-only: synthesize a Denial for the registered handlers. */
export function _emitDenialForTest(d: Denial): void {
  for (const h of policyDeniedHandlers) h(d);
}

// Re-export the client type for consumers that need it.
export type { HarnessClient };
