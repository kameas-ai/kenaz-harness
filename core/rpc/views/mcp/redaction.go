package mcp

import (
	"net/url"
	"strings"
)

// RedactURL removes sensitive values from a URL's query string. Every
// query parameter value is replaced with "[REDACTED]" so API keys and
// tokens carried as ?key=… or ?token=… never appear in audit logs.
//
// The function is nil-safe: an empty or unparseable URL is returned
// unchanged.
func RedactURL(rawURL string) string {
	if rawURL == "" {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		// Unparseable URL — return as-is rather than leak or panic.
		return rawURL
	}
	q := u.Query()
	for key := range q {
		q.Set(key, "[REDACTED]")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// RedactAuthHeader strips the credential value from an Authorization
// header, preserving only the scheme prefix (e.g. "Bearer [REDACTED]"
// instead of "Bearer sk-abc123"). Returns the input unchanged when it
// does not follow the "Scheme value" pattern.
func RedactAuthHeader(header string) string {
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 {
		return "[REDACTED]"
	}
	return parts[0] + " [REDACTED]"
}

// RedactHeaders returns a copy of the headers map with the
// Authorization header value replaced by RedactAuthHeader. The map is
// nil-tolerant; if no Authorization key is present the map is returned
// as-is (not copied).
func RedactHeaders(headers map[string]string) map[string]string {
	const authKey = "Authorization"
	v, ok := headers[authKey]
	if !ok {
		return headers
	}
	out := make(map[string]string, len(headers))
	for k, val := range headers {
		out[k] = val
	}
	out[authKey] = RedactAuthHeader(v)
	return out
}
