/**
 * kenaz__fork_conversation tool-result parsing (model fork tool, WP02).
 *
 * The Go tool (core/tools/forkconversation, SuccessResult) returns JSON
 * carrying `branch_session_id` — the child session the user opens. The
 * transcript's ToolChip reads it through here to render an "Open branch"
 * link, so the fork the model created is one click away instead of only
 * findable in the branches sidebar.
 *
 * Both name spellings are accepted: the model-facing catalog name is
 * namespaced ("kenaz__fork_conversation") while the dispatch path strips
 * the "kenaz__" prefix (core/toolloop/builtins.go), and persisted
 * tool_call rows may carry either.
 */

export const FORK_TOOL_NAMES: ReadonlySet<string> = new Set([
  'kenaz__fork_conversation',
  'fork_conversation',
]);

export function isForkToolName(name: string): boolean {
  return FORK_TOOL_NAMES.has(name);
}

/** Session ids are opaque tokens; anything else is not linked. */
const SESSION_ID_RE = /^[A-Za-z0-9_-]{1,128}$/;

/**
 * Returns the branch session id from a fork tool result, or '' when the
 * output is not a successful fork (error envelope, truncated, empty, or
 * not JSON). A `handoff_not_seeded` result still carries the ids — the
 * branch exists — so it is linked too.
 */
export function forkBranchSessionId(output: string): string {
  if (!output) return '';
  let parsed: unknown;
  try {
    parsed = JSON.parse(output);
  } catch {
    return '';
  }
  if (!parsed || typeof parsed !== 'object') return '';
  const id = (parsed as Record<string, unknown>).branch_session_id;
  if (typeof id !== 'string' || !SESSION_ID_RE.test(id)) return '';
  return id;
}
