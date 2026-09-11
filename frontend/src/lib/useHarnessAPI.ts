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
import { useEventStream } from './useEventStream';
import { adaptHealthEntry, type WireHealthEntry } from './harnessClient';
import type {
  Session,
  Project,
  ShellStatus,
  AuditEntry,
  AuditFilter,
  RecipeListing,
  RecipeState,
  RecipeStatus,
  HealthEntry,
  Artifact,
  ArtifactFilter,
  ArtifactScope,
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

/** Debounce delay (ms) for broker-driven list refresh. Non-negotiable:
 * prevents refresh storms when a busy chat run emits multiple events
 * in quick succession (e.g. create → auto-title → usage). */
const SESSION_LIST_CHANGED_DEBOUNCE_MS = 150;

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

  // Broker-driven invalidation: subscribe to session.list_changed so the
  // LeftRail updates when auto-titling, suggest-title, branch-creation, or
  // any other external write mutates the sessions list. Debounced by
  // SESSION_LIST_CHANGED_DEBOUNCE_MS to prevent refresh storms.
  let debounceTimer: ReturnType<typeof setTimeout> | null = null;
  let offListChanged: (() => void) | undefined;

  function scheduleRefresh() {
    if (debounceTimer !== null) {
      clearTimeout(debounceTimer);
    }
    debounceTimer = setTimeout(() => {
      debounceTimer = null;
      void refresh();
    }, SESSION_LIST_CHANGED_DEBOUNCE_MS);
  }

  onMounted(() => {
    if (typeof window !== 'undefined' && window.runtime?.EventsOn) {
      offListChanged = window.runtime.EventsOn('session.list_changed', scheduleRefresh);
    }
  });

  onBeforeUnmount(() => {
    if (offListChanged) {
      offListChanged();
      offListChanged = undefined;
    }
    if (debounceTimer !== null) {
      clearTimeout(debounceTimer);
      debounceTimer = null;
    }
  });

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

// UseStreamResult is the shape every typed live-stream composable in this
// module returns. useEventLogStream (below) is the real implementation —
// engineer-truth-pass-01PMTP01 WP04 (finding B7) deleted the other one,
// useChatStream: a permanent stub (events always [], stop() a no-op) with
// zero non-test references tree-wide — `git grep -n useChatStream --
// frontend/src` matched only its own definition. Its doc comment claimed
// "downstream consumers" that never existed; the real streaming
// implementation shipped as lib/useSession.ts + lib/useEventStream.ts
// under different names. Zero test readers — nothing to delete alongside
// it.
export interface UseStreamResult<T> {
  events: Ref<readonly T[]>;
  paused: Ref<boolean>;
  pause(): void;
  resume(): void;
  stop(): Promise<void>;
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

// useMemory / UseMemoryResult / MemoryFilterPill were deleted here
// (engineer-truth-pass-01PMTP01 WP04, finding B8) — a fully-implemented
// composable (chunks list + remember/promoteScope/forget mutations) with
// zero consumers. `views/memory/MemoryView.vue` never imported it; it
// calls `client.memory.*` directly at 13 distinct methods (listChunks,
// forget, pin, prunePreview, runPruneNow, promoteScope,
// narrativeFailedCount, narrativeFailedList, retryFailedNarrative,
// markImportant, getChunkProvenance, lastRetrieval, embeddingProbe) —
// a strict superset of the 5 useMemory wrapped, and documents calling the
// client directly as intent. The one method useMemory wrapped that
// MemoryView doesn't call, rememberMessage, is called directly at
// views/sessions/SessionsView.vue:1053 — it stays on the client. Live
// substitute: views/memory/MemoryView.vue. Zero test readers — nothing
// to delete alongside it.

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

  // mergeHealthEntry — connector-lifecycle-truth-01PMZ303 UNIT-8 (ruling
  // A-2): applies a `mcp:health-changed` PUSH event onto the matching
  // row's status, independent of the 1 Hz poll above.
  //
  // This is NOT redundant with the poll. `hasNonTerminal` (top of file)
  // treats `running` as terminal and stops polling once every row
  // reaches it — correct for stdio, where a supervised process doesn't
  // silently die without a restart event this composable would also
  // see. It is the wrong assumption for a remote (http/sse) connector's
  // live-probed health (UNIT-7): the probe can trip AFTER the row has
  // gone terminal and polling has stopped, and nothing would re-check
  // it. Before this unit, `MCP_SubscribeHealthChanges` had a Subscribe
  // call and zero publishers and zero frontend callers, so this gap was
  // real: a dead remote server's row stayed "running" until the next
  // full `refresh()` (a manual navigation, not automatic). This handler
  // is what makes the transition — "a server that dies stops reporting
  // live" — observable without one.
  //
  // HealthEntry (transport-agnostic) is a narrower shape than
  // RecipeStatus (stdio-process-centric — pid, keysPresent, resource/
  // prompt counts); only the overlapping fields are overwritten, so a
  // push event never blanks out fields it doesn't carry.
  // The event carries the raw wire shape (snake_case JSON tags off
  // `mcp.HealthEntry`, not the camelCase HealthEntry the rest of the
  // app sees) — useEventStream forwards payloads verbatim, with no
  // adaptation of its own (it is transport plumbing, not a client
  // method). adaptHealthEntry is the SAME function healthSnapshot()
  // uses, so a push event and a polled snapshot land in identical
  // shape.
  function handleHealthChanged(raw: WireHealthEntry): void {
    mergeHealthEntry(adaptHealthEntry(raw));
  }

  function mergeHealthEntry(entry: HealthEntry): void {
    const next = recipes.value.map((row) => {
      if (row.recipe.id !== entry.id) return row;
      const status: RecipeStatus = {
        ...row.status,
        state: entry.state,
        lastError: entry.lastError,
        restartAttempts: entry.restartAttempts,
        stderrTail: entry.stderrTail ?? row.status.stderrTail,
        toolCount: entry.toolCount,
        serverName: entry.serverName ?? row.status.serverName,
        serverVersion: entry.serverVersion ?? row.status.serverVersion,
        protocolVersion: entry.protocolVersion ?? row.status.protocolVersion,
      };
      return { ...row, status };
    });
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

  // connector-lifecycle-truth-01PMZ303 UNIT-8: register for the live
  // push signal once per composable instance. subscribeHealthChanges()
  // starts the backend broker forwarding PublishHealthChange calls onto
  // the fixed `mcp:health-changed` Wails topic; useEventStream is what
  // actually listens for the payloads (see mergeHealthEntry above for
  // why this is not just a rename of the existing poll).
  const healthStream = useEventStream<WireHealthEntry>(
    'mcp:health-changed',
    handleHealthChanged,
  );
  let healthSubID: string | null = null;

  onMounted(() => {
    void refresh();
    client.mcp
      .subscribeHealthChanges()
      .then((id) => {
        if (mounted) healthSubID = id;
      })
      .catch(() => {
        // Best-effort: a failed subscribe leaves the row on the 1 Hz
        // poll's coverage (stdio unaffected; a remote connector that
        // dies after going terminal simply won't update until the next
        // manual refresh — the pre-UNIT-8 behaviour, not a regression).
      });
  });

  onBeforeUnmount(() => {
    mounted = false;
    stopPolling();
    healthStream.unsubscribe();
    if (healthSubID) {
      void client.mcp.stopStream(healthSubID);
    }
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

/**
 * useArtifacts — reactive list + CRUD wrappers for the Artifacts table.
 *
 * The composable owns the active filter and re-fetches whenever it
 * changes. Components that drive the /artifacts global view consume
 * `list` directly; the chat surface uses `saveFromMessage` for the
 * right-click "Save as artifact" affordance.
 */
export interface UseArtifactsResult {
  list: Ref<readonly Artifact[]>;
  loading: Ref<boolean>;
  error: Ref<string | null>;
  filter: Ref<ArtifactFilter>;
  refresh(): Promise<void>;
  setFilter(next: ArtifactFilter): Promise<void>;
  promote(
    id: string,
    scopeKind: ArtifactScope,
    scopeId: string,
  ): Promise<Artifact>;
  remove(id: string): Promise<void>;
  saveFromMessage(
    sessionId: string,
    messageId: string,
    title: string,
    rangeStart?: number,
    rangeEnd?: number,
  ): Promise<Artifact>;
}

export function useArtifacts(
  initialFilter: ArtifactFilter = {},
): UseArtifactsResult {
  const client = useHarnessClient();
  const list = shallowRef<readonly Artifact[]>([]);
  const loading = ref(false);
  const error = ref<string | null>(null);
  const filter = ref<ArtifactFilter>({ ...initialFilter });

  async function refresh() {
    loading.value = true;
    error.value = null;
    try {
      list.value = await client.artifacts.list(filter.value);
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e);
      list.value = [];
    } finally {
      loading.value = false;
    }
  }

  async function setFilter(next: ArtifactFilter) {
    filter.value = { ...next };
    await refresh();
  }

  async function promote(
    id: string,
    scopeKind: ArtifactScope,
    scopeId: string,
  ): Promise<Artifact> {
    const updated = await client.artifacts.promote(id, scopeKind, scopeId);
    await refresh();
    return updated;
  }

  async function remove(id: string) {
    await client.artifacts.remove(id);
    await refresh();
  }

  async function saveFromMessage(
    sessionId: string,
    messageId: string,
    title: string,
    rangeStart?: number,
    rangeEnd?: number,
  ): Promise<Artifact> {
    const created = await client.sessions.saveAsArtifact(
      sessionId,
      messageId,
      title,
      rangeStart,
      rangeEnd,
    );
    await refresh();
    return created;
  }

  return {
    list,
    loading,
    error,
    filter,
    refresh,
    setFilter,
    promote,
    remove,
    saveFromMessage,
  };
}

// Re-export the client type for consumers that need it.
export type { HarnessClient };
