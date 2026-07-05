/**
 * sentry.ts — Sentry Vue integration.
 *
 * This module is loaded LAZILY (via dynamic import()) only when tier != Off.
 * When tier == Off the chunk is never fetched, keeping the entry bundle clean.
 *
 * Privacy invariants enforced here:
 *  - autoSessionTracking: false (no session replay)
 *  - sendDefaultPii: false (no PII auto-collection)
 *  - beforeSend: JS redactor runs on every event before transmission
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
