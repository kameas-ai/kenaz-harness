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
	"strings"
	"unicode"
	"unicode/utf8"
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

// ACK wire caps, mirrored from kenaz-fleet PR #178 (service/config_ack.go
// validateACKDetail + handlers_config.go validACKMachineID). All caps are in
// BYTES. Enforced client-side by sanitising + TRUNCATING — an over-long or
// odd field must never cost the device its ACK (fleet 400s the whole ACK).
const (
	ackMaxMachineIDBytes = 128  // no control characters at all
	ackMaxErrorBytes     = 1024 // errors[] and items[].error; \n and \t allowed
	ackMaxErrors         = 50
	ackMaxItems          = 500
	ackMaxVersionBytes   = 64 // no control characters
)

// ackCatalogKinds is fleet's catalog kind set (items[].kind must be one of
// these, or empty).
var ackCatalogKinds = map[string]bool{
	MandatedKindSkill: true, MandatedKindWorkflow: true, MandatedKindPack: true, MandatedKindBundle: true,
}

// configACKPayload is the JSON body sent to POST /api/v1/configs/{id}/ack.
// Fleet's shape: {"applied", "machine_id", "error"?, "errors"?, "items"?};
// items and errors are additive-optional. bundle_id stays in the body for
// the older fleet configACKRequest, which declares it.
type configACKPayload struct {
	// BundleID is the bundle_id being acknowledged.
	BundleID int64 `json:"bundle_id"`
	// Applied is true when all sections applied without error.
	Applied bool `json:"applied"`
	// MachineID is this install's node id (the same id the enroll and the
	// config pull's ?machine= send) so fleet can tell devices apart.
	MachineID string `json:"machine_id,omitempty"`
	// ErrorMsg carries the first error from a partial-failure apply, or empty.
	// Kept for server-side backwards compatibility.
	ErrorMsg string `json:"error,omitempty"`
	// Errors carries all per-section apply errors (FR-012), ≤50 × ≤1024 chars.
	Errors []string `json:"errors,omitempty"`
	// Items is the per-mandated-item status (applied|refused|failed|removed),
	// ≤500 entries.
	Items []MandatedItemStatus `json:"items,omitempty"`
}

// ConfigACKReport is the per-device detail an ACK carries beyond the error
// list: the machine id and the mandated items' statuses.
type ConfigACKReport struct {
	MachineID string
	Items     []MandatedItemStatus
}

// PostConfigACK posts an ACK to /api/v1/configs/<id>/ack. applyErrs is the
// list of per-section errors from the apply pipeline (may be empty on full
// success). Either way the ACK is sent so the fleet server knows the bundle
// was received and processed.
//
// Returns nil on HTTP 200/204; returns an error on transport or server errors.
// Callers treat this as best-effort: errors are logged, not propagated beyond
// the poll loop.
func PostConfigACK(ctx context.Context, client *Client, bundleID int64, applyErrs []error, report ConfigACKReport) error {
	if client == nil || client.isNop {
		return ErrFleetDisabled
	}

	payload := buildConfigACKPayload(bundleID, applyErrs, report)

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

// buildConfigACKPayload assembles the ACK body, redacting paths and applying
// fleet's validation rules by sanitisation + truncation.
func buildConfigACKPayload(bundleID int64, applyErrs []error, report ConfigACKReport) configACKPayload {
	payload := configACKPayload{
		BundleID:  bundleID,
		Applied:   len(applyErrs) == 0,
		MachineID: ackText(report.MachineID, ackMaxMachineIDBytes, false),
	}
	if len(applyErrs) > 0 {
		payload.ErrorMsg = ackText(redactPaths(applyErrs[0].Error()), ackMaxErrorBytes, true)
		payload.Errors = make([]string, 0, len(applyErrs))
		for _, e := range applyErrs {
			if e == nil {
				continue
			}
			if len(payload.Errors) == ackMaxErrors {
				break
			}
			payload.Errors = append(payload.Errors, ackText(redactPaths(e.Error()), ackMaxErrorBytes, true))
		}
	}
	if n := len(report.Items); n > 0 {
		if n > ackMaxItems {
			n = ackMaxItems
		}
		payload.Items = make([]MandatedItemStatus, n)
		for i := 0; i < n; i++ {
			it := report.Items[i]
			if !IsWireUUID(it.CatalogID) {
				it.CatalogID = "" // fleet: catalog_id must be a UUID or absent
			}
			if !ackCatalogKinds[it.Kind] {
				it.Kind = ""
			}
			it.Version = ackText(it.Version, ackMaxVersionBytes, false)
			if it.Error != "" {
				it.Error = ackText(redactPaths(it.Error), ackMaxErrorBytes, true)
			}
			payload.Items[i] = it
		}
	}
	return payload
}

// ackText makes s acceptable to fleet's cleanACKText: valid UTF-8, no control
// characters (\n and \t kept when allowWS), at most maxBytes bytes —
// truncated on a rune boundary, never split mid-character.
func ackText(s string, maxBytes int, allowWS bool) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !(allowWS && (r == '\n' || r == '\t')) {
			return -1
		}
		return r
	}, s)
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
