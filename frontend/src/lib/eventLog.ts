/**
 * eventLog — client-side error reporter routed through RPC. FR-018.
 * Surfaces from <ErrorBoundary> push captured errors here; downstream
 * mission `event-log-01KQ1A3M` consumes via Reader.
 *
 * Also forwards every report to the backend's Diag_LogClientEvent
 * binding so it lands in ~/.kenaz/harness.log alongside the Go-side
 * stream events. That gives a single chronological trail when
 * debugging the send path end-to-end.
 */

interface ReportedError {
  message: string;
  stack?: string;
  componentName?: string;
  ts: string;
}

const buffer: ReportedError[] = [];

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
  // Mirror to console for dev visibility; the in-memory buffer is the
  // canonical artifact that persists across the session.
  // eslint-disable-next-line no-console
  console.error('[ErrorBoundary]', componentName ?? '<surface>', e.message, e.stack);
  const entry: ReportedError = {
    message: e.message,
    stack: e.stack,
    componentName,
    ts: new Date().toISOString(),
  };
  buffer.push(entry);
  // Cap the buffer so a runaway loop doesn't OOM.
  if (buffer.length > 100) buffer.shift();
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

export function recentErrors(): readonly ReportedError[] {
  return buffer;
}

export function clearErrors(): void {
  buffer.length = 0;
}
