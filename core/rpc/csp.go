//go:build !serve

package rpc

import (
	"net/http"

	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// productionCSP is the strict policy from plan §4.3 invariant #1. It
// MUST be byte-identical to the production CSP injected into index.html
// at build time (frontend/vite.config.ts). scripts/ci/check-csp.sh greps
// the produced dist/index.html and asserts the policy is not weakened.
const productionCSP = "default-src 'none'; " +
	"connect-src 'none'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"font-src 'self'; " +
	"base-uri 'none'; " +
	"form-action 'none'; " +
	"frame-ancestors 'none'; " +
	"object-src 'none'"

// NewCSPMiddleware returns the Wails asset-server middleware that sets
// the Content-Security-Policy header on every static-asset response.
// This duplicates the <meta http-equiv> in index.html so the policy
// applies even on response paths the meta tag cannot reach.
func NewCSPMiddleware() assetserver.Middleware {
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

// ProductionCSP returns the strict CSP literal so tests can assert the
// header value without re-deriving it.
func ProductionCSP() string { return productionCSP }
