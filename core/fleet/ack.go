// Package fleet — ack.go
//
// PostConfigACK sends a POST to /api/v1/configs/<bundle_id>/ack after a
// bundle has been applied. The ACK payload includes whether the apply
// succeeded. Best-effort: callers log errors but do NOT retry.
//
// (fleet-config-pull-01NDFSEX10 WP05)
package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
)

// pathLike matches absolute filesystem paths that may appear in error strings
// (e.g. "/home/user/.config/kenaz/..." or "C:\Users\..." on Windows). These
// are redacted before leaving the device so that host identities and directory
// layouts are not sent to the fleet server.
var pathLike = regexp.MustCompile(`(?i)([a-z]:\\[^:\s"']+|/[^\s"']{4,})`)

// redactPaths replaces absolute filesystem paths in s with "<path>" so that
// ACK error strings do not carry host-specific information to the fleet server.
func redactPaths(s string) string {
	return pathLike.ReplaceAllString(s, "<path>")
}

// configACKPayload is the JSON body sent to the fleet ACK endpoint.
type configACKPayload struct {
	// BundleID is the bundle_id being acknowledged.
	BundleID int64 `json:"bundle_id"`
	// Applied is true when all sections applied without error.
	Applied bool `json:"applied"`
	// ErrorMsg carries the first error from a partial-failure apply, or empty.
	// Kept for server-side backwards compatibility.
	ErrorMsg string `json:"error,omitempty"`
	// Errors carries all per-section apply errors (FR-012). Clients that speak
	// the v1 wire format ignore unknown fields, so this extends cleanly.
	Errors []string `json:"errors,omitempty"`
}

// PostConfigACK posts an ACK to /api/v1/configs/<id>/ack. applyErrs is the
// list of per-section errors from the apply pipeline (may be empty on full
// success). Either way the ACK is sent so the fleet server knows the bundle
// was received and processed.
//
// Returns nil on HTTP 200/204; returns an error on transport or server errors.
// Callers treat this as best-effort: errors are logged, not propagated beyond
// the poll loop.
func PostConfigACK(ctx context.Context, client *Client, bundleID int64, applyErrs []error) error {
	if client == nil || client.isNop {
		return ErrFleetDisabled
	}

	payload := configACKPayload{
		BundleID: bundleID,
		Applied:  len(applyErrs) == 0,
	}
	if len(applyErrs) > 0 {
		payload.ErrorMsg = redactPaths(applyErrs[0].Error())
		payload.Errors = make([]string, 0, len(applyErrs))
		for _, e := range applyErrs {
			if e != nil {
				payload.Errors = append(payload.Errors, redactPaths(e.Error()))
			}
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("fleet: ack marshal: %w", err)
	}

	path := fmt.Sprintf("/api/v1/configs/%d/ack", bundleID)
	resp, err := client.Post(ctx, path, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("fleet: ack POST: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("fleet: ack status %d", resp.StatusCode)
	}
	return nil
}
