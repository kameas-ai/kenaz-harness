/**
 * sentrySdk.ts — the exact slice of @sentry/vue that sentry.ts uses.
 *
 * sentry.ts loads the SDK lazily. Importing the package itself dynamically
 * (`import('@sentry/vue')`) hands the code a module NAMESPACE object, and a
 * namespace that escapes into ordinary code cannot be tree-shaken: every
 * export of @sentry/vue — Session Replay, replay-canvas, the Feedback widget,
 * profiling and tracing helpers — was emitted into the desktop bundle even
 * though init() is the only thing ever called and none of those integrations
 * is configured (none of them is in the SDK's default integration set).
 * Measured at dad0605e: ~146 KB gzipped for the Sentry chunk.
 *
 * Re-exporting the named binding from a local module lets Rollup keep only
 * what init() reaches. Behaviour is unchanged: the same init function, with
 * the same default integrations, called with the same options.
 *
 * Add a name here only when sentry.ts starts using it.
 */
export { init } from '@sentry/vue';
