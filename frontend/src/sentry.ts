/**
 * sentry.ts — Sentry Vue integration.
 *
 * This module is loaded LAZILY (via dynamic import()) only when tier != Off.
 * When tier == Off the chunk is never fetched, keeping the entry bundle clean.
 *
 * Privacy invariants enforced here:
 *  - sendDefaultPii: false (no PII auto-collection)
 *  - beforeSend: the JS redactor (./sentry-redactor) runs on every event
 *    the SDK builds, unconditionally, before beforeSend returns it to the
 *    SDK's own transport. It is real and it is exercised — see
 *    sentry-redactor.ts and __tests__/sentry.spec.ts.
 *  - No session tracking is configured. `autoSessionTracking` was removed
 *    from @sentry/vue's Options type by the SDK's own v10 major
 *    (verified: adding it back fails `vue-tsc --noEmit` with "does not
 *    exist in type"). This module previously claimed it was set to
 *    `false` here; nothing in the Sentry.init call below has ever set it,
 *    on any SDK version this repo has shipped — entry-points-and-crash-
 *    reporting-01PMZD13 UNIT-7.
 *
 * WHAT "BEFORE TRANSMISSION" DOES NOT MEAN ON DESKTOP (entry-points-and-
 * crash-reporting-01PMZD13 §1.6 C-4, E-001): the desktop production CSP
 * (frontend/vite.config.ts's PROD_CSP) is `connect-src 'none'`. This SDK
 * uses its default fetch/XHR transport (no `transport`/`tunnel` override
 * below), so on desktop `beforeSend` redacts an event the browser then
 * CANNOT SEND — the redaction is real, the transmission never happens.
 * initSentry still returns `true` in that case (init succeeded; sending
 * later fails silently, which is the SDK's own behaviour under a blocking
 * CSP, not something this module detects or reports). Whether the
 * renderer should be allowed to reach Sentry's ingest host at all is
 * E-001, an open escalation — see research/escalations.md. Served mode
 * has no Sentry block in main-served.ts at all (N-1), which is also part
 * of E-001 and not a separate decision.
 *
 * wire-up point 4 for sentry (sentry-error-monitoring-01KX5R8G WP04):
 *   frontend/src/main.ts calls initSentry() after reading appInfo().
 */

import type { App } from 'vue';

export type SentryTier = 'off' | 'anonymous' | 'identified';

/** Options passed from main.ts to initSentry(). */
export interface SentryInitOptions {
  tier: SentryTier;
  dsn: string;
  release?: string;
  gitsha?: string;
  /** Vue app instance — required to wire the Vue ErrorHandler. */
  app: App;
}

/**
 * initSentry initialises the @sentry/vue SDK.
 * Called only when tier != Off. Uses dynamic import() so the Sentry bundle
 * is not included in the entry chunk.
 *
 * Returns true on success, false on failure (always graceful).
 */
export async function initSentry(opts: SentryInitOptions): Promise<boolean> {
  if (opts.tier === 'off' || !opts.dsn) {
    return false;
  }
  try {
    // Dynamic import — deferred until this function is called.
    const [Sentry, { redactString, redactObject, redactStringDeep }] = await Promise.all([
      import('@sentry/vue'),
      import('./sentry-redactor'),
    ]);

    Sentry.init({
      app: opts.app,
      dsn: opts.dsn,
      release: opts.release,
      dist: opts.gitsha,

      // Privacy — no PII auto-collection.
      sendDefaultPii: false,

      // Redact all events before transmission.
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      beforeSend(event: any) {
        try {
          return redactSentryEvent(event as SentryEventLike, { redactString, redactObject, redactStringDeep }) as any;
        } catch {
          // If the redactor itself throws, drop the event rather than
          // transmitting potentially un-redacted content.
          return null;
        }
      },
    });

    return true;
  } catch (err) {
    console.warn('[sentry] init failed:', err);
    return false;
  }
}

/** Minimal type for a Sentry event. */
interface SentryEventLike {
  message?: string;
  exception?: {
    values?: Array<{
      value?: string;
      stacktrace?: {
        frames?: Array<{
          filename?: string;
          abs_path?: string;
          module?: string;
          vars?: Record<string, unknown>;
        }>;
      };
    }>;
  };
  breadcrumbs?: Array<{
    message?: string;
    data?: Record<string, unknown>;
  }>;
  extra?: Record<string, unknown>;
  tags?: Record<string, string>;
  contexts?: Record<string, Record<string, unknown>>;
}

/** Apply the JS redactor to a Sentry event in place. */
function redactSentryEvent(
  event: SentryEventLike,
  r: {
    redactString: (s: string) => string;
    redactObject: (o: Record<string, unknown>) => Record<string, unknown>;
    redactStringDeep: (s: string) => string;
  },
): SentryEventLike {
  if (event.message) {
    event.message = r.redactString(event.message);
  }
  if (event.extra) {
    event.extra = r.redactObject(event.extra);
  }
  if (event.tags) {
    for (const k of Object.keys(event.tags)) {
      event.tags[k] = r.redactString(event.tags[k]);
    }
  }
  if (event.contexts) {
    for (const ctxKey of Object.keys(event.contexts)) {
      event.contexts[ctxKey] = r.redactObject(event.contexts[ctxKey]);
    }
  }
  if (event.exception?.values) {
    for (const ex of event.exception.values) {
      if (ex.value) {
        ex.value = r.redactString(ex.value);
      }
      if (ex.stacktrace?.frames) {
        for (const frame of ex.stacktrace.frames) {
          if (frame.filename) frame.filename = r.redactString(frame.filename);
          if (frame.abs_path) frame.abs_path = r.redactString(frame.abs_path);
          if (frame.module) frame.module = r.redactString(frame.module);
          if (frame.vars) {
            frame.vars = r.redactObject(frame.vars as Record<string, unknown>);
          }
        }
      }
    }
  }
  if (event.breadcrumbs) {
    for (const b of event.breadcrumbs) {
      if (b.message) b.message = r.redactString(b.message);
      if (b.data) b.data = r.redactObject(b.data);
    }
  }
  return event;
}
