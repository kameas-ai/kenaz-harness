/**
 * folderBatch — the client-side orchestration behind the folder-level
 * share/promote dialog (knowledge-home-01DOGF0E FR-7, owner ruling D4
 * 2026-10-05: build the batch dialog).
 *
 * There is deliberately no backend batch RPC: a folder batch is a
 * sequential run of the existing per-entry `contexts.publish` /
 * `contexts.promote` bindings, with the same deterministic node ids the
 * single-file path uses. Sequential (not parallel) so the per-entry
 * progress list is truthful and a cancel stops cleanly between entries.
 */
import type { ContextNode } from '@/lib/types';

/**
 * contextNodeID — the stable fleet node id for a library path. Shared by
 * the single-file publish/promote path and the folder batch so the same
 * file always maps to the same fleet node (btoa of the path; the raw path
 * when btoa cannot encode it, e.g. non-Latin-1 names).
 */
export function contextNodeID(path: string): string {
  try {
    return btoa(path);
  } catch {
    return path;
  }
}

/** contextEntryTitle — the published title for a path: basename sans extension. */
export function contextEntryTitle(path: string): string {
  return path.replace(/.*\//, '').replace(/\.[^.]+$/, '');
}

/** collectFolderFiles — every file under `folder`, recursively, in tree order. */
export function collectFolderFiles(folder: ContextNode): ContextNode[] {
  if (folder.kind === 'file') return [folder];
  const out: ContextNode[] = [];
  for (const child of folder.children ?? []) out.push(...collectFolderFiles(child));
  return out;
}

export type FolderBatchMode = 'share' | 'promote';

/**
 * entryIneligibleReason — why one entry cannot be part of the batch, or
 * null when it can. Capability state is NOT checked here: that gate is
 * whole-dialog (it comes from the FleetSession-derived
 * `sharingDisabledReason` in ContextsView), never per entry.
 */
export function entryIneligibleReason(node: ContextNode, mode: FolderBatchMode): string | null {
  if (mode === 'share' && node.size === 0) return 'Empty file — nothing to share.';
  return null;
}

export type EntryStatus = 'pending' | 'running' | 'done' | 'failed' | 'skipped' | 'not_started';

export interface BatchEntry {
  path: string;
  /** Library path relative to the selected folder, for display. */
  relPath: string;
  ineligible: string | null;
  checked: boolean;
  status: EntryStatus;
  /** Result detail (effective layer, new classification) or failure reason. */
  message: string;
}

export function buildBatchEntries(folder: ContextNode, mode: FolderBatchMode): BatchEntry[] {
  const prefix = folder.path ? `${folder.path}/` : '';
  return collectFolderFiles(folder).map((n) => {
    const ineligible = entryIneligibleReason(n, mode);
    return {
      path: n.path,
      relPath: n.path.startsWith(prefix) ? n.path.slice(prefix.length) : n.path,
      ineligible,
      checked: ineligible === null,
      status: 'pending',
      message: '',
    };
  });
}

export interface BatchSummary {
  done: number;
  failed: number;
  skipped: number;
  notStarted: number;
}

export function summarize(entries: readonly BatchEntry[]): BatchSummary {
  const s: BatchSummary = { done: 0, failed: 0, skipped: 0, notStarted: 0 };
  for (const e of entries) {
    if (e.status === 'done') s.done++;
    else if (e.status === 'failed') s.failed++;
    else if (e.status === 'skipped') s.skipped++;
    else if (e.status === 'not_started') s.notStarted++;
  }
  return s;
}

/**
 * runBatch — run `op` over the checked entries one at a time. One entry
 * failing never aborts the rest. `shouldStop` is consulted before each
 * entry: the in-flight entry always finishes, entries after a stop are
 * marked `not_started`. `op` returns a success message, throws to fail the
 * entry, or returns `{ skipped: reason }` to skip it.
 */
export async function runBatch(
  entries: BatchEntry[],
  op: (entry: BatchEntry) => Promise<string | { skipped: string }>,
  shouldStop: () => boolean,
): Promise<void> {
  for (const e of entries) {
    if (!e.checked || e.ineligible !== null) continue;
    if (shouldStop()) {
      e.status = 'not_started';
      e.message = 'Not started — the batch was cancelled.';
      continue;
    }
    e.status = 'running';
    e.message = '';
    try {
      const r = await op(e);
      if (typeof r === 'string') {
        e.status = 'done';
        e.message = r;
      } else {
        e.status = 'skipped';
        e.message = r.skipped;
      }
    } catch (err) {
      e.status = 'failed';
      e.message = err instanceof Error ? err.message : String(err);
    }
  }
}
