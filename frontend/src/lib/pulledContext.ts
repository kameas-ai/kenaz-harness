/**
 * pulledContext — the synthetic Contexts paths of teammates' shared Fleet
 * entries, "<team|org>/_fleet/<id>" (core/rpc/views/contexts
 * mergePulledEntries).
 *
 * A pulled entry is someone else's text. It is shown read-only, never
 * edited, re-shared, promoted or attached: the model reaches it only
 * through kenaz__read_context_file's framed `shared` argument and the
 * framed always-load block. The backend refuses all of these too; the UI
 * just doesn't offer them.
 */
import type { ContextNode } from '@/lib/types';

const PULLED_PATH = /^(team|org)\/_fleet(\/|$)/;

/** True when path is (or is under) a pulled team/org entry path. */
export function isPulledEntryPath(path: string | null | undefined): boolean {
  return typeof path === 'string' && PULLED_PATH.test(path);
}

/** Why a pulled entry cannot be shared, promoted or attached. */
export const PULLED_ENTRY_READ_ONLY_REASON =
  'Shared by a teammate through Kameas Fleet — read-only here. It cannot be re-shared, promoted or attached.';

/**
 * withoutPulledEntries returns a copy of the tree with every pulled entry
 * removed. A folder emptied by the removal (the virtual "team"/"org"
 * folders that only held pulled entries) is dropped as well.
 */
export function withoutPulledEntries(node: ContextNode): ContextNode {
  if (!node.children) return node;
  const children: ContextNode[] = [];
  for (const c of node.children) {
    if (isPulledEntryPath(c.path)) continue;
    const kept = withoutPulledEntries(c);
    const hadChildren = (c.children?.length ?? 0) > 0;
    if (kept.kind === 'folder' && hadChildren && (kept.children?.length ?? 0) === 0) continue;
    children.push(kept);
  }
  return { ...node, children };
}
