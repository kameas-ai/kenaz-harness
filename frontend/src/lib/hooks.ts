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
 * HOOK_KINDS is the exhaustive list of hook kinds for the kind picker.
 * Mirrors core/hooks.Kind* constants.
 */
export const HOOK_KINDS = ['builtin', 'shell', 'mcp'] as const;
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
