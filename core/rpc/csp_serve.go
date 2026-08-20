//go:build serve

// csp_serve.go provides a Wails-free CSP surface for serve mode.
// NewCSPMiddleware is replaced with a standard net/http middleware so
// cmd/harness-served does not pull in github.com/wailsapp/wails.
//
// STATUS (entry-points-and-crash-reporting-01PMZD13 UNIT-8): NewCSPMiddleware
// below has ZERO production call sites — RAN: grep for
// `.NewCSPMiddleware(` and `NewCSPMiddleware()` across core/, cmd/ and
// main.go finds only its own declaration, csp.go's desktop counterpart
// (called from main.go:232, the Wails asset-server path), and this file's
// test. core/serve's actual served-mode CSP is enforced a different way —
// core/serve/server.go's servedCSP constant is substituted directly into
// the response HTML body's __CSP_PLACEHOLDER__ meta tag
// (server.go:461), not set as an HTTP response header by any middleware.
// So this file's middleware is dead code, not a second enforcement path
// that happens to run — wiring it as a HEADER alongside the existing
// META TAG enforcement is a real option (browsers apply the more
// restrictive of the two when both are present) but is out of this
// unit's scope: it would be a genuine behaviour change to
// core/serve/server.go's request handling, not a correctness fix to a
// wrong constant.
//
// What UNIT-8 DOES fix: before this unit, productionCSP here was a
// byte-for-byte copy of csp.go's DESKTOP policy (connect-src 'none') in a
// build tag whose only real product is SERVED mode — wrong for the
// policy this file's own doc comment says it exists to serve. If this
// binary, or anything else built with -tags serve, ever wires
// NewCSPMiddleware in, connect-src 'none' would have blocked the
// browser's own /rpc and /ws calls. Corrected to the served policy,
// which core/serve/server.go's own comment says is authoritative
// (server.go:95-96), and covered by the same parity test UNIT-5 added
// for the meta-tag definition (core/serve/csp_parity_test.go) so all
// three definitions (vite.config.ts's SERVED_CSP, server.go's servedCSP,
// this file's productionCSP) stay in sync.

package rpc

import "net/http"

// productionCSP is the strict CSP policy for serve mode (see the STATUS
// comment above for why this is the served policy, not csp.go's desktop
// one, and for the caveat that nothing calls NewCSPMiddleware below yet).
const productionCSP = "default-src 'none'; " +
	"connect-src 'self'; " +
	"script-src 'self' 'sha256-S9VfhoaWcxszZps4jluBpniHVTyGsrOIZlLNj5x7ekE='; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"font-src 'self'; " +
	"base-uri 'none'; " +
	"form-action 'none'; " +
	"frame-ancestors 'none'; " +
	"object-src 'none'"

// ProductionCSP returns the strict CSP literal so tests can assert the
// header value without re-deriving it.
func ProductionCSP() string { return productionCSP }

// NewCSPMiddleware returns a standard net/http middleware (not Wails assetserver
// middleware) that sets the Content-Security-Policy header on every response.
// Signature differs from the desktop path — this variant is only used by
// serve-mode code that expects a plain func(http.Handler) http.Handler.
func NewCSPMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", productionCSP)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "no-referrer")
			next.ServeHTTP(w, r)
		})
	}
}
