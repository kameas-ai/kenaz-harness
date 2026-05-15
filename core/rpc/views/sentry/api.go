// Package sentry provides the view-scoped RPC surface for crash-reporting
// settings and local crash report generation
// (sentry-error-monitoring-01KX5R8G WP05).
package sentry

import (
	"context"
	"time"
)

// CachedEntry is one entry in the Last-5 crash cache.
type CachedEntry struct {
	// ID is a generated ULID or UUID for UI key purposes.
	ID string `json:"id"`
	// CapturedAt is the UTC moment the crash was captured.
	CapturedAt time.Time `json:"capturedAt"`
	// Kind is "exception" or "message".
	Kind string `json:"kind"`
	// Summary is a short (≤120 char) redacted description.
	Summary string `json:"summary"`
	// SentryEventID is the ID returned by the Sentry SDK after capture,
	// empty when tier=Off.
	SentryEventID string `json:"sentryEventId,omitempty"`
}

// LocalReportResult carries the path to the generated crash report JSON file.
type LocalReportResult struct {
	// Path is the absolute path of the generated file.
	Path string `json:"path"`
	// ByteCount is the file size in bytes.
	ByteCount int64 `json:"byteCount"`
}

// TestDSNResult carries the outcome of a DSN connectivity check.
type TestDSNResult struct {
	// OK is true when the DSN parsed correctly and the ingestion endpoint
	// responded with HTTP 200.
	OK bool `json:"ok"`
	// Error is a human-readable description when OK == false.
	Error string `json:"error,omitempty"`
}

// SentryAPI is the view-scoped RPC surface.
type SentryAPI interface {
	// GetLastFive returns the most-recent 5 (or fewer) captured events
	// from the Last-5 cache. Oldest first.
	GetLastFive(ctx context.Context) ([]CachedEntry, error)

	// GenerateLocalReport builds a redacted JSON crash report at
	// <DataDir>/crash-reports/YYYY-MM-DD-HHMMSS.json. Returns the path
	// and byte count.
	GenerateLocalReport(ctx context.Context) (LocalReportResult, error)

	// TestDSN parses the given DSN string and issues a HEAD request to
	// the ingestion URL. Returns OK:true when the server responds 2xx.
	TestDSN(ctx context.Context, dsn string) (TestDSNResult, error)
}
