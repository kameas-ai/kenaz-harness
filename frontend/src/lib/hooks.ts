/**
 * hooks.ts — typed API client for the lifecycle-hook RPC surface.
 *
 * Mirrors the SlashcmdClient pattern in harnessClient.ts. Components
 * import `useHooksClient()` (or consume `client.hooks` from the
 * HarnessClient) rather than importing Wails bindings directly.
 *
 * hooks-event-surface-expansion-01KZNP3A WP07b.
 */
import type { Hook, BuiltinDescriptor, DryRunResult } from '@/lib/types';

/**
 * HooksClient — the view-scoped surface for lifecycle-hook CRUD and
 * dry-run dispatch.
 *
 * Backed by the Wails `Hooks_*` bindings in harnessClient.ts.
 * The fake stub in createFakeHarnessClient returns sensible defaults.
 */
export interface HooksClient {
  /** Return the full ordered hook list. */
  list(): Promise<Hook[]>;
  /** Return a single hook by ID. */
  get(id: string): Promise<Hook>;
  /** Append a new hook. Returns the stored hook (including any server-side defaults). */
  add(input: Hook): Promise<Hook>;
  /** Replace an existing hook (full replace by ID). */
  update(input: Hook): Promise<void>;
  /** Delete a hook by ID. */
  remove(id: string): Promise<void>;
  /** Return the list of available builtin descriptors for the kind=builtin picker. */
  availableBuiltins(): Promise<BuiltinDescriptor[]>;
  /**
   * Auto-install the two starter memory hooks (memory.retrieve +
   * memory.persist). Idempotent — safe to call multiple times.
   */
  installStarterMemory(): Promise<void>;
  /**
   * Remove the two starter memory hooks. Idempotent — safe to call when
   * the hooks are already absent.
   */
  removeStarterMemory(): Promise<void>;
  /**
   * Fire the hook identified by `hookID` against a synthetic JSON payload
   * (a string containing valid JSON, e.g. `'{"session_id":"test"}'`).
   *
   * Shell hooks are actually executed; builtin/mcp hooks return a
   * descriptive stub so the drawer can show something useful without
   * touching live data.
   *
   * Returns the per-hook HookOutput and a MergedOutput for the drawer's
   * decision summary.
   */
  dryRun(hookID: string, syntheticPayload: string): Promise<DryRunResult>;
}

/**
 * ALL_HOOK_EVENTS is the ordered union of v1 + v2 event names. Mirrors
 * core/hooks.AllEvents. Used to populate the event picker in HookEditor.
 */
export const ALL_HOOK_EVENTS = [
  // v1 chat-pipeline events
  'pre_send',
  'post_send',
  'pre_save_session',
  'post_assistant_turn_complete',
  // v2 tool-loop events
  'pre_tool_use',
  'post_tool_use',
  'post_tool_use_failure',
  // v2 user-interaction events
  'user_prompt_submit',
  // v2 session lifecycle events
  'session_start',
  'setup',
  // v2 sub-agent events
  'subagent_start',
  // v2 file-system events
  'cwd_changed',
  'file_changed',
  // v2 permission events
  'permission_request',
  'permission_denied',
  // v2 notification / task events
  'notification',
  'background_task_complete',
  'worktree_create',
] as const;

export type HookEventName = (typeof ALL_HOOK_EVENTS)[number];

/**
 * FIRING_HOOK_EVENTS is the declared subset of ALL_HOOK_EVENTS whose
 * events actually reach a production Fire/FireAsync/Run<X> call today.
 * This is the single source of truth for the event picker's `<option>`
 * list (HookEditor.vue renders this, not ALL_HOOK_EVENTS).
 *
 * ALL_HOOK_EVENTS and core/hooks.AllEvents are NOT edited to shrink —
 * `isKnownEvent` (core/hooks/hooks.go:267-274) still accepts every event
 * name, so a hook already saved against an event not listed here still
 * loads, still validates server-side, and still renders (with an inert
 * badge — see HookEditor.vue). This list only narrows what the picker
 * *offers* for new hooks.
 *
 * Seeded 2026-08-19 (trust-surfaces-that-fire-01PMZ202 WP08 / UNIT-7)
 * with the truthful set derived empirically by WP01: of the 18 events
 * ALL_HOOK_EVENTS listed, exactly one — pre_send — has a real production
 * call site (core/rpc/views/llm/impl.go:638, `a.hooks.RunPreSend(...)`).
 * post_send looked like a second one (a complete adapter chain, a live
 * builtin) but has zero call sites outside test files — see F31 in
 * research/execution-ledger.md. NOT ['pre_send', 'post_send'].
 *
 * Every producer WP appends its event(s) here in the same commit as its
 * fire site (WP09-WP21). scripts/ci/check-hook-event-fire-sites.sh (G-2)
 * enforces both directions: every entry here must have a real fire site,
 * and every AllEvents entry NOT here must have a dated, owner-named
 * justification in scripts/ci/allowlists/i17-eventless-hook-events.txt.
 *
 * Grown 2026-08-19 (trust-surfaces-that-fire-01PMZ202 WP09 / UNIT-8) by
 * the three v2 tool-loop events: pre_tool_use, post_tool_use,
 * post_tool_use_failure. Landed in the same commit as
 * agentgraph.Env.LifecycleHooks actually being populated
 * (core/rpc/views/agentgraph/env_deps.go + core/rpc/api.go's
 * newGraphManagerWithDeps) — before that commit, the fire sites in
 * core/agentgraph/tool_invocation.go always saw env.LifecycleHooks == nil
 * and no-opped.
 *
 */
export const FIRING_HOOK_EVENTS = [
  'pre_send',
  'pre_tool_use',
  'post_tool_use',
  'post_tool_use_failure',
] as const;

/**
 * EVENT_FAMILY maps each event to a display category for the UI grouping.
 */
export const EVENT_FAMILY: Record<HookEventName, string> = {
  pre_send: 'chat',
  post_send: 'chat',
  pre_save_session: 'chat',
  post_assistant_turn_complete: 'chat',
  pre_tool_use: 'tool',
  post_tool_use: 'tool',
  post_tool_use_failure: 'tool',
  user_prompt_submit: 'user',
  session_start: 'session',
  setup: 'session',
  subagent_start: 'session',
  cwd_changed: 'fs',
  file_changed: 'fs',
  permission_request: 'permission',
  permission_denied: 'permission',
  notification: 'task',
  background_task_complete: 'task',
  worktree_create: 'task',
};

/**
 * HOOK_KINDS is the list of hook kinds offered by the kind picker.
 *
 * 'mcp' is intentionally NOT here (trust-surfaces-that-fire-01PMZ202 WP08 /
 * UNIT-7, A-6): the MCP hook kind is stub-only in the backend today. A
 * saved kind=mcp hook must still load and dispatch through Go's
 * hooks.KindMCP — that machinery is untouched — this only stops the
 * picker from offering a kind that would be a no-op stub if a user chose
 * it new. core/hooks.Kind* constants (including KindMCP) are unchanged.
 */
export const HOOK_KINDS = ['builtin', 'shell'] as const;
export type HookKindName = (typeof HOOK_KINDS)[number];

/**
 * SYNTHETIC_PAYLOADS provides a per-event sample payload for the
 * HookDryRunDrawer's default text. Users can edit these freely.
 */
export const SYNTHETIC_PAYLOADS: Record<HookEventName, string> = {
  pre_send: JSON.stringify(
    { session_id: 'test-session', model: 'claude-3-5-sonnet', kind: 'local',
      user_text: 'Hello', messages: [{ role: 'user', content: 'Hello' }] },
    null, 2,
  ),
  post_send: JSON.stringify(
    { session_id: 'test-session', model: 'claude-3-5-sonnet', kind: 'local',
      user_turn: 'Hello', assistant_turn: 'Hi there!', finish_reason: 'stop' },
    null, 2,
  ),
  pre_save_session: JSON.stringify(
    { session_id: 'test-session', project_id: '' },
    null, 2,
  ),
  post_assistant_turn_complete: JSON.stringify(
    { session_id: 'test-session', model: 'claude-3-5-sonnet', finish_reason: 'stop' },
    null, 2,
  ),
  pre_tool_use: JSON.stringify(
    { session_id: 'test-session', tool_name: 'bash', tool_input: { command: 'ls -la' } },
    null, 2,
  ),
  post_tool_use: JSON.stringify(
    { session_id: 'test-session', tool_name: 'bash',
      tool_result: { stdout: '', stderr: '', exit_code: 0 } },
    null, 2,
  ),
  post_tool_use_failure: JSON.stringify(
    { session_id: 'test-session', tool_name: 'bash', error: 'command not found' },
    null, 2,
  ),
  user_prompt_submit: JSON.stringify(
    { session_id: 'test-session', text: 'What files are in this directory?' },
    null, 2,
  ),
  session_start: JSON.stringify(
    { session_id: 'test-session', project_id: '', cwd: '/home/user', model: 'claude-3-5-sonnet' },
    null, 2,
  ),
  setup: JSON.stringify(
    { session_id: 'test-session', project_id: '' },
    null, 2,
  ),
  subagent_start: JSON.stringify(
    { branch_id: 'br-test', parent_session_id: 'test-session',
      profile_id: '', prompt: 'Analyse this code' },
    null, 2,
  ),
  cwd_changed: JSON.stringify(
    { session_id: 'test-session', cwd: '/home/user/project' },
    null, 2,
  ),
  file_changed: JSON.stringify(
    { session_id: 'test-session',
      paths: ['/home/user/project/main.go'],
      events: [{ path: '/home/user/project/main.go', op: 'write' }] },
    null, 2,
  ),
  permission_request: JSON.stringify(
    { session_id: 'test-session', action: 'bash', resource: 'ls -la /tmp',
      reason: 'List temp files' },
    null, 2,
  ),
  permission_denied: JSON.stringify(
    { session_id: 'test-session', action: 'bash', resource: 'rm -rf /',
      deny_reason: 'blocked by policy' },
    null, 2,
  ),
  notification: JSON.stringify(
    { session_id: 'test-session', title: 'Task complete',
      body: 'The background task has finished.' },
    null, 2,
  ),
  background_task_complete: JSON.stringify(
    { session_id: 'test-session', task_id: 'bg-task-1', result: 'ok' },
    null, 2,
  ),
  worktree_create: JSON.stringify(
    { session_id: 'test-session', worktree_path: '/home/user/worktrees/feature' },
    null, 2,
  ),
};
