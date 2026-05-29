// Package fleet provides the view-scoped RPC surface for fleet telemetry
// consent settings (fleet-otel-archival-01NDFSEX11 WP07).
package fleet

import "context"

// FleetAPI is the view-scoped RPC surface for fleet telemetry consent.
type FleetAPI interface {
	// GetTelemetryConsent returns the stored consent level for this device:
	// "none" (default), "aggregate", or "full".
	GetTelemetryConsent(ctx context.Context) (string, error)

	// SetTelemetryConsent persists the given consent level. Returns an error
	// when the org tier is insufficient for the requested level (e.g.
	// aggregate requires pro+, full requires team+).
	SetTelemetryConsent(ctx context.Context, level string) error
}
