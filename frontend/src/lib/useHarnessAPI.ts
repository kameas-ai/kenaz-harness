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
  Project,
  ShellStatus,
  Denial,
  AuditEntry,
  AuditFilter,
  EventStreamEntry,
  MemoryChunk,
  MemoryListFilter,
  MemoryScopeKind,
  RecipeListing,
  RecipeState,
  RecipeStatus,
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
  /**
   * moveToProject sets the session's project membership (empty
   * projectId detaches it). Refreshes the list afterwards so the
   * rail re-groups the moved row.
   */
  moveToProject(sessionId: string, projectId: string): Promise<void>;
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

  async function moveToProject(sessionId: string, projectId: string) {
    await client.sessions.moveToProject(sessionId, projectId);
    await refresh();
  }

  return { list, loading, refresh, create, rename, remove, moveToProject };
}

/**
 * useProjects — reactive projects list + CRUD wrappers, mirroring
 * useSessions in shape so components can adopt either with the same
 * mental model.
 */
export interface UseProjectsResult {
  list: Ref<readonly Project[]>;
  loading: Ref<boolean>;
  refresh(): Promise<void>;
  create(name: string, description?: string): Promise<Project>;
  rename(id: string, name: string): Promise<void>;
  remove(id: string, deleteSessions: boolean): Promise<void>;
  addSession(projectId: string, sessionId: string): Promise<void>;
  removeSession(sessionId: string): Promise<void>;
}

export function useProjects(): UseProjectsResult {
  const client = useHarnessClient();
  const list = shallowRef<readonly Project[]>([]);
  const loading = ref(false);

  async function refresh() {
    loading.value = true;
    try {
      list.value = await client.projects.list();
    } catch {
      list.value = [];
    } finally {
      loading.value = false;
    }
  }

  async function create(name: string, description?: string) {
    const p = await client.projects.create(name, description);
    await refresh();
    return p;
  }

  async function rename(id: string, name: string) {
    await client.projects.rename(id, name);
    await refresh();
  }

  async function remove(id: string, deleteSessions: boolean) {
    await client.projects.remove(id, deleteSessions);
    await refresh();
  }

  async function addSession(projectId: string, sessionId: string) {
    await client.projects.addSession(projectId, sessionId);
  }

  async function removeSession(sessionId: string) {
    await client.projects.removeSession(sessionId);
  }

  return {
    list,
    loading,
    refresh,
    create,
    rename,
    remove,
    addSession,
    removeSession,
  };
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

/**
 * useMemory — reactive long-term-memory list + scope-aware mutations.
 *
 * The composable owns the active scope filter and re-fetches the chunk
 * list whenever it changes. Components that drive the /memory view can
 * consume `chunks` directly; the chat surface uses `remember` /
 * `promoteScope` for one-shot mutations.
 */
export type MemoryFilterPill = 'all' | MemoryScopeKind;

export interface UseMemoryResult {
  chunks: Ref<readonly MemoryChunk[]>;
  loading: Ref<boolean>;
  error: Ref<string | null>;
  filter: Ref<MemoryFilterPill>;
  refresh(): Promise<void>;
  setFilter(pill: MemoryFilterPill): Promise<void>;
  remember(
    sessionID: string,
    messageID: string,
    scope?: MemoryScopeKind,
  ): Promise<string>;
  promoteScope(
    chunkID: string,
    newScopeKind: MemoryScopeKind,
    newScopeID: string,
  ): Promise<string>;
  forget(id: string): Promise<void>;
}

export function useMemory(): UseMemoryResult {
  const client = useHarnessClient();
  const chunks = shallowRef<readonly MemoryChunk[]>([]);
  const loading = ref(false);
  const error = ref<string | null>(null);
  const filter = ref<MemoryFilterPill>('all');

  function buildFilter(pill: MemoryFilterPill): MemoryListFilter {
    if (pill === 'all') return {};
    return { scopeKind: pill };
  }

  async function refresh() {
    loading.value = true;
    error.value = null;
    try {
      chunks.value = await client.memory.listChunks(buildFilter(filter.value));
    } catch (err) {
      error.value = err instanceof Error ? err.message : String(err);
      chunks.value = [];
    } finally {
      loading.value = false;
    }
  }

  async function setFilter(pill: MemoryFilterPill) {
    if (filter.value === pill) return;
    filter.value = pill;
    await refresh();
  }

  async function remember(
    sessionID: string,
    messageID: string,
    scope?: MemoryScopeKind,
  ) {
    const id = await client.memory.rememberMessage(sessionID, messageID, scope);
    return id;
  }

  async function promoteScope(
    chunkID: string,
    newScopeKind: MemoryScopeKind,
    newScopeID: string,
  ) {
    const id = await client.memory.promoteScope(
      chunkID,
      newScopeKind,
      newScopeID,
    );
    await refresh();
    return id;
  }

  async function forget(id: string) {
    await client.memory.forget(id);
    await refresh();
  }

  return {
    chunks,
    loading,
    error,
    filter,
    refresh,
    setFilter,
    remember,
    promoteScope,
    forget,
  };
}

// ── MCP recipes (Tools panel) ─────────────────────────────────────────

const RECIPE_POLL_MS = 1000;
const TERMINAL_STATES: readonly RecipeState[] = ['stopped', 'running', 'failed'];

function isTerminal(state: RecipeState): boolean {
  return TERMINAL_STATES.includes(state);
}

export interface UseToolsRecipesResult {
  recipes: Ref<readonly RecipeListing[]>;
  loading: Ref<boolean>;
  error: Ref<string | null>;
  /** Map of recipe id → toggle-on timestamp. Drives the warming UX. */
  startedAt: Ref<Readonly<Record<string, number>>>;
  refresh(): Promise<void>;
  install(
    id: string,
    env: Record<string, string>,
    config?: Record<string, unknown>,
  ): Promise<RecipeStatus>;
  uninstall(id: string): Promise<void>;
  forgetKey(id: string, envName: string): Promise<void>;
  statusFor(id: string): RecipeStatus | undefined;
  /**
   * config returns the persisted per-install ConfigOption map for an
   * enabled recipe (delegates to client.tools.recipes.config). Empty
   * for not-enabled or no-config recipes.
   */
  config(id: string): Promise<Record<string, unknown>>;
}

/**
 * useToolsRecipes — reactive list of shipped MCP recipes + lifecycle
 * actions for the Tools panel.
 *
 * Polling lifecycle:
 *   - When at least one visible row is in a non-terminal state
 *     (`starting | restarting`), a 1 Hz poll fans out per-row
 *     `recipeStatus(id)` calls and merges them into the list.
 *   - When all rows are terminal (`stopped | running | failed`), the
 *     poll timer is cleared so the harness sits idle.
 *   - The lifecycle is restarted whenever `install` flips a row into
 *     a non-terminal state (the optimistic update sets `state` to
 *     `starting` immediately).
 *   - `onBeforeUnmount` clears the timer unconditionally.
 */
export function useToolsRecipes(): UseToolsRecipesResult {
  const client = useHarnessClient();
  const recipes = ref<readonly RecipeListing[]>([]);
  const loading = ref(false);
  const error = ref<string | null>(null);
  const startedAt = ref<Record<string, number>>({});

  let timer: ReturnType<typeof setInterval> | null = null;
  let mounted = true;

  function hasNonTerminal(list: readonly RecipeListing[]): boolean {
    return list.some((r) => !isTerminal(r.status.state));
  }

  function ensurePolling() {
    if (timer || !mounted) return;
    if (!hasNonTerminal(recipes.value)) return;
    timer = setInterval(() => {
      void pollOnce();
    }, RECIPE_POLL_MS);
  }

  function stopPolling() {
    if (timer) {
      clearInterval(timer);
      timer = null;
    }
  }

  async function pollOnce() {
    const targets = recipes.value
      .filter((r) => !isTerminal(r.status.state))
      .map((r) => r.recipe.id);
    if (targets.length === 0) {
      stopPolling();
      return;
    }
    const updates = await Promise.all(
      targets.map(async (id) => {
        try {
          const s = await client.tools.recipes.status(id);
          return [id, s] as const;
        } catch {
          return [id, null] as const;
        }
      }),
    );
    const byId = new Map<string, RecipeStatus>();
    for (const [id, status] of updates) {
      if (status) byId.set(id, status);
    }
    if (byId.size === 0) return;
    const next = recipes.value.map((row) => {
      const u = byId.get(row.recipe.id);
      if (!u) return row;
      // Clear startedAt once a row reaches a terminal state so the
      // warming indicator never lingers on a settled row.
      if (isTerminal(u.state) && startedAt.value[row.recipe.id]) {
        const cleared = { ...startedAt.value };
        delete cleared[row.recipe.id];
        startedAt.value = cleared;
      }
      return { ...row, status: u };
    });
    recipes.value = next;
    if (!hasNonTerminal(next)) {
      stopPolling();
    }
  }

  async function refresh() {
    loading.value = true;
    error.value = null;
    try {
      recipes.value = await client.tools.recipes.list();
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e);
      recipes.value = [];
    } finally {
      loading.value = false;
    }
    ensurePolling();
  }

  function mergeStatus(id: string, status: RecipeStatus): void {
    const next = recipes.value.map((row) =>
      row.recipe.id === id
        ? { ...row, status, enabled: status.enabled, keysPresent: status.keysPresent }
        : row,
    );
    recipes.value = next;
  }

  async function install(
    id: string,
    env: Record<string, string>,
    config?: Record<string, unknown>,
  ) {
    startedAt.value = { ...startedAt.value, [id]: Date.now() };
    try {
      const status = await client.tools.recipes.install(id, env, config);
      mergeStatus(id, status);
      ensurePolling();
      return status;
    } catch (e) {
      const cleared = { ...startedAt.value };
      delete cleared[id];
      startedAt.value = cleared;
      throw e;
    }
  }

  async function uninstall(id: string) {
    await client.tools.recipes.uninstall(id);
    const cleared = { ...startedAt.value };
    delete cleared[id];
    startedAt.value = cleared;
    await refresh();
  }

  async function forgetKey(id: string, envName: string) {
    await client.tools.recipes.forgetKey(id, envName);
    await refresh();
  }

  function statusFor(id: string): RecipeStatus | undefined {
    return recipes.value.find((r) => r.recipe.id === id)?.status;
  }

  async function config(id: string): Promise<Record<string, unknown>> {
    return client.tools.recipes.config(id);
  }

  onMounted(() => {
    void refresh();
  });

  onBeforeUnmount(() => {
    mounted = false;
    stopPolling();
  });

  return {
    recipes,
    loading,
    error,
    startedAt,
    refresh,
    install,
    uninstall,
    forgetKey,
    statusFor,
    config,
  };
}

/**
 * useShell — composable for OS-shell affordances. Currently a thin
 * pass-through to `client.shell.openInOSBrowser`; lives here so the
 * Tools panel can call it as a single hook rather than reaching into
 * `useHarnessClient` directly (FR-009 mirror).
 */
export interface UseShellResult {
  openInOSBrowser(path: string): Promise<void>;
}

export function useShell(): UseShellResult {
  const client = useHarnessClient();
  return {
    openInOSBrowser: (path) => client.shell.openInOSBrowser(path),
  };
}

// Re-export the client type for consumers that need it.
export type { HarnessClient };
