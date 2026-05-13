/**
 * tasks.ts — typed wrappers for the background-task RPC surface.
 *
 * These functions call the Wails-bound Tasks_* methods wired in
 * core/rpc/views/tasks. The parameter and return shapes mirror
 * the Go-side core/rpc/views/tasks.API methods exactly.
 *
 * (background-task-monitor-01KZNP3C WP05)
 */

import type { TaskRow, LineRow } from './types';

// The Wails runtime injects window.go.rpc.API at startup.
// We access it via a typed proxy to avoid importing wailsjs types
// that generate during build (not available in vitest).
type WailsRPC = {
  Tasks_List: () => Promise<TaskRow[]>;
  Tasks_Get: (id: string) => Promise<TaskRow>;
  Tasks_Tail: (id: string, fromOffset: number) => Promise<LineRow[]>;
  Tasks_Abort: (id: string) => Promise<void>;
};

function rpc(): WailsRPC {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return (window as any).go?.rpc?.API as WailsRPC;
}

/** List all known background tasks, newest-first. */
export async function tasksList(): Promise<TaskRow[]> {
  return rpc().Tasks_List();
}

/** Get a single task by ID. */
export async function tasksGet(id: string): Promise<TaskRow> {
  return rpc().Tasks_Get(id);
}

/**
 * Return lines captured since fromOffset.
 * Pass 0 for the first call; pass the offset from the last line + 1 on
 * subsequent calls for efficient streaming.
 */
export async function tasksTail(id: string, fromOffset: number): Promise<LineRow[]> {
  return rpc().Tasks_Tail(id, fromOffset);
}

/** Send SIGTERM to the task's process and mark it cancelled. */
export async function tasksAbort(id: string): Promise<void> {
  return rpc().Tasks_Abort(id);
}

// ── Fake implementations for tests and Storybook ─────────────────────────────

/** A fake implementation for use in vitest / Storybook. */
export const fakeTasksClient = {
  list: (): TaskRow[] => [],
  get: (_id: string): TaskRow | undefined => undefined,
  tail: (_id: string, _fromOffset: number): LineRow[] => [],
  abort: (_id: string): void => undefined,
};
