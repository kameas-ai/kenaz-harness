/**
 * handoffErrorText — the share / inbox surfaces' error copy
 * (device-keys-handoff-01DEVKH01 WP06).
 *
 * The Go backend returns handoff failures whose message IS the human copy
 * (core/fleet HandoffError — "Your teammate's inbox is full…", never
 * "status 409"). Wails rejects with that string, sometimes wrapped as an
 * Error; this only unwraps it. A raw transport/status string that slipped
 * through is replaced with a generic line rather than shown verbatim.
 */
export function handoffErrorText(err: unknown): string {
  let msg = err instanceof Error ? err.message : String(err ?? '');
  msg = msg.replace(/^Error:\s*/, '').trim();
  if (!msg) return 'Something went wrong. Please try again.';
  if (/\bstatus \d{3}\b/i.test(msg)) {
    return 'Fleet returned an unexpected error. Please try again later.';
  }
  return msg;
}
