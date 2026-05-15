// Package sentry provides privacy-preserving crash and error reporting for
// the kenaz harness. All events are redacted before transmission: secrets,
// personal data, home-dir paths, and long binding-arg strings are stripped
// or truncated. See [Redactor] for the full rule set.
//
// # Architecture
//
// The package is intentionally isolated so forking operators can remove it
// cleanly. Four wire-up points connect sentry to the rest of the harness:
//
//  1. main.go — calls [Init] at startup (after reading settings) and wraps
//     the chassis main with defer [RecoverMain].
//  2. core/logging/setup.go — chains [SlogHandler] into the slog pipeline
//     when tier != Off.
//  3. core/rpc/bindings.go — every binding method's first line is
//     defer [RecoverBinding](methodName).
//  4. core/rpc/views/sentry/impl.go — the Sentry_* RPC view delegates here.
//
// Remove core/sentry/ and stub those four sites to remove this package from
// a fork.
//
// # Tier model
//
// Three tiers govern what is sent:
//
//   - Off (default) — nothing is sent; a nopClient is active; local crash
//     reports can still be generated via [GenerateLocalReport].
//   - Anonymous — structured exception data + redacted breadcrumbs are sent
//     with no user identifier. DSN must be configured.
//   - Identified — same as Anonymous but the user's fleet identity is
//     attached as a Sentry user tag (requires fleet login).
//
// # Privacy invariant
//
// The redactor is a load-bearing privacy guarantee. Any unscrubbed secret in
// a Sentry payload is a P0 privacy bug. The integration test in
// integration_test.go plants known secret patterns and asserts they do NOT
// appear in the intercepted transport payload.
package sentry
