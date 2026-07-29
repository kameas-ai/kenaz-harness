//go:build serve

// csp_serve.go provides a Wails-free CSP surface for serve mode.
// NewCSPMiddleware is replaced with a standard net/http middleware so
// cmd/harness-served does not pull in github.com/wailsapp/wails.

package rpc

import "net/http"

// productionCSP is the strict CSP policy (identical value to csp.go,
// including the theme-script hash — see that file's comment).
const productionCSP = "default-src 'none'; " +
	"connect-src 'none'; " +
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
