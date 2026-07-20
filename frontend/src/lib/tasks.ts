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

// The Wails runtime injects window.go.rpc.Bindings at startup (the bound struct
// is "Bindings", NOT "API"). Mirror the accessor pattern used in harnessClient.ts
// so both modules resolve the same global. Tasks_* methods are declared on the
// Bindings struct and are generated into wailsjs/go/rpc/Bindings.
type WailsRPC = {
  Tasks_List: () => Promise<TaskRow[]>;
  Tasks_Get: (id: string) => Promise<TaskRow>;
  Tasks_Tail: (id: string, fromOffset: number) => Promise<LineRow[]>;
  Tasks_Abort: (id: string) => Promise<void>;
};

function rpc(): WailsRPC {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const b = (typeof window !== 'undefined' && (window as any).go?.rpc?.Bindings) || undefined;
  if (!b) {
    throw new Error(
      'window.go.rpc.Bindings is not available. The harness frontend must run inside Wails.',
    );
  }
  return b as WailsRPC;
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
