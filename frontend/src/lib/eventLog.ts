/**
 * eventLog — client-side error reporter routed through RPC. FR-018.
 * Surfaces from <ErrorBoundary> push captured errors here (verified:
 * ErrorBoundary.vue:39 calls reportError) and every report forwards to
 * the backend's Diag_LogClientEvent binding, landing in
 * ~/.kenaz/harness.log alongside the fourteen logEvent call sites in
 * lib/useSession.ts and main-served.ts:176,183,193. That backend log is
 * the canonical, durable artifact — a single chronological trail when
 * debugging the send path end-to-end.
 *
 * Before engineer-truth-pass-01PMTP01 WP04 (finding B9a) this module
 * also kept a 100-entry in-memory ring (`buffer`) with two readers,
 * recentErrors() and clearErrors() — both write-only in practice: zero
 * non-test references tree-wide. The docstring here used to claim a
 * downstream mission ("event-log-01KQ1A3M") consumed the buffer via a
 * Reader; that mission shipped (kitty-specs/_archive/) and never built
 * one. Both functions and the buffer were deleted; nothing was reading
 * them, so there were no tests to delete alongside them.
 */

interface DiagBindings {
  Diag_LogClientEvent?: (
    level: string,
    message: string,
    attrs: Record<string, unknown>,
  ) => Promise<void>;
}

function backendLog(
  level: 'info' | 'warn' | 'error',
  message: string,
  attrs: Record<string, unknown>,
): void {
  const w = window as unknown as {
    go?: { rpc?: { Bindings?: DiagBindings } };
  };
  const fn = w.go?.rpc?.Bindings?.Diag_LogClientEvent;
  if (!fn) return;
  void fn(level, message, attrs).catch(() => undefined);
}

export function reportError(err: unknown, componentName?: string): void {
  const e = err instanceof Error ? err : new Error(String(err));
  // Mirror to console for dev visibility; ~/.kenaz/harness.log (via
  // backendLog below) is the canonical artifact that persists across
  // the session — see the module docstring.
  // eslint-disable-next-line no-console
  console.error('[ErrorBoundary]', componentName ?? '<surface>', e.message, e.stack);
  backendLog('error', 'error_boundary', {
    component: componentName ?? '<surface>',
    message: e.message,
    stack: e.stack ?? '',
  });
}

/** logEvent forwards a structured event to the backend log file. */
export function logEvent(
  level: 'info' | 'warn' | 'error',
  message: string,
  attrs: Record<string, unknown> = {},
): void {
  backendLog(level, message, attrs);
}
