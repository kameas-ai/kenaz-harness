/**
 * eventLog — client-side error reporter routed through RPC. FR-018.
 * Surfaces from <ErrorBoundary> push captured errors here; downstream
 * mission `event-log-01KQ1A3M` consumes via Reader.
 */

interface ReportedError {
  message: string;
  stack?: string;
  componentName?: string;
  ts: string;
}

const buffer: ReportedError[] = [];

export function reportError(err: unknown, componentName?: string): void {
  const e = err instanceof Error ? err : new Error(String(err));
  const entry: ReportedError = {
    message: e.message,
    stack: e.stack,
    componentName,
    ts: new Date().toISOString(),
  };
  buffer.push(entry);
  // Cap the buffer so a runaway loop doesn't OOM.
  if (buffer.length > 100) buffer.shift();
}

export function recentErrors(): readonly ReportedError[] {
  return buffer;
}

export function clearErrors(): void {
  buffer.length = 0;
}
