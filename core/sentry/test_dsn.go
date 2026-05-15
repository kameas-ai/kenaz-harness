package sentry

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TestDSN validates a Sentry DSN string and issues a HEAD request to the
// ingestion endpoint to verify reachability. Returns (true, "") on success
// or (false, errorMessage) on failure. Never panics.
func TestDSN(dsn string) (ok bool, errMsg string) {
	if dsn == "" {
		return false, "DSN is empty"
	}

	// Parse the DSN URL.
	u, err := url.Parse(dsn)
	if err != nil {
		return false, fmt.Sprintf("invalid DSN URL: %v", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return false, fmt.Sprintf("DSN scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return false, "DSN host is empty"
	}
	// Basic path check: Sentry DSNs end with a numeric project ID.
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) == 0 || parts[len(parts)-1] == "" {
		return false, "DSN path must end with a project ID"
	}

	// Build the ingestion URL (Sentry's /api/<project>/store/ envelope endpoint).
	projectID := parts[len(parts)-1]
	ingestURL := fmt.Sprintf("%s://%s/api/%s/store/", u.Scheme, u.Host, projectID)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Head(ingestURL) //nolint:noctx
	if err != nil {
		return false, fmt.Sprintf("network error: %v", err)
	}
	defer resp.Body.Close()

	// Sentry returns 405 Method Not Allowed for HEAD on the store endpoint —
	// which actually proves the endpoint is reachable and accepting connections.
	// Accept 2xx and 4xx as "reachable"; only hard-fail on 5xx or network errors.
	if resp.StatusCode >= 500 {
		return false, fmt.Sprintf("server error: HTTP %d", resp.StatusCode)
	}
	return true, ""
}
