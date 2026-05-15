package custom

import (
	"fmt"
	"net/http"
)

// ApplyAuth mutates req's headers to carry the credential material
// according to scheme.
//
// Decision table:
//
//	bearer         → Authorization: Bearer <key>
//	api-key-header → <AuthHeader>: <key>  (default header: X-API-Key)
//	custom         → forwarded via the same header mechanism as api-key-header
//	                 (callers set authHeader explicitly)
//	none           → no-op; non-nil cred is logged as a warning (caller
//	                 must use ErrKeyAgainstNone to handle)
//
// Returns ErrInvalidConfig when cred is nil/empty for a scheme that requires
// credentials (bearer, api-key-header, custom).
func ApplyAuth(req *http.Request, scheme AuthScheme, cred []byte, authHeader string) error {
	switch scheme {
	case AuthSchemeBearer:
		if len(cred) == 0 {
			return &ErrInvalidConfig{Reason: "bearer auth requires a non-empty credential"}
		}
		req.Header.Set("Authorization", "Bearer "+string(cred))

	case AuthSchemeAPIKeyHeader:
		if len(cred) == 0 {
			return &ErrInvalidConfig{Reason: "api-key-header auth requires a non-empty credential"}
		}
		header := authHeader
		if header == "" {
			header = "X-API-Key"
		}
		req.Header.Set(header, string(cred))

	case AuthSchemeCustom:
		if len(cred) == 0 {
			return &ErrInvalidConfig{Reason: "custom auth requires a non-empty credential"}
		}
		header := authHeader
		if header == "" {
			return &ErrInvalidConfig{Reason: "custom auth requires auth_header to be set"}
		}
		req.Header.Set(header, string(cred))

	case AuthSchemeNone:
		// No credential material is set. Caller is responsible for
		// detecting the ErrKeyAgainstNone sentinel when cred is non-nil.
		// We do not error here: the request proceeds without credentials.

	default:
		return &ErrInvalidConfig{Reason: fmt.Sprintf("unknown auth_scheme %q", scheme)}
	}
	return nil
}

// Zero overwrites every byte of buf with zero. Call after credential
// material has been consumed to limit its lifetime in memory.
func Zero(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
}
