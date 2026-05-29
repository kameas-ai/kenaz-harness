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
)

// configACKPayload is the JSON body sent to the fleet ACK endpoint.
type configACKPayload struct {
	// BundleID is the bundle_id being acknowledged.
	BundleID int64 `json:"bundle_id"`
	// Applied is true when all sections applied without error.
	Applied bool `json:"applied"`
	// ErrorMsg carries the first error from a partial-failure apply, or empty.
	ErrorMsg string `json:"error,omitempty"`
}

// PostConfigACK posts an ACK to /api/v1/configs/<id>/ack. applyErr may be
// nil (full success) or non-nil (partial apply failure). Either way the ACK
// is sent so the fleet server knows the bundle was received and processed.
//
// Returns nil on HTTP 200/204; returns an error on transport or server errors.
// Callers treat this as best-effort: errors are logged, not propagated beyond
// the poll loop.
func PostConfigACK(ctx context.Context, client *Client, bundleID int64, applyErr error) error {
	if client == nil || client.isNop {
		return ErrFleetDisabled
	}

	payload := configACKPayload{
		BundleID: bundleID,
		Applied:  applyErr == nil,
	}
	if applyErr != nil {
		payload.ErrorMsg = applyErr.Error()
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
