package sentry

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	coresentry "github.com/kameas-ai/kenaz-harness/core/sentry"
)

// Impl implements SentryAPI backed by core/sentry helpers.
type Impl struct {
	// DataDir is the harness data directory (<~/.harness>). Used to
	// construct the crash-report output path.
	DataDir string
}

var _ SentryAPI = (*Impl)(nil)

// GetLastFive returns up to 5 most-recently cached crash entries.
func (s *Impl) GetLastFive(_ context.Context) ([]CachedEntry, error) {
	raw := coresentry.GetLastFive(s.DataDir)
	out := make([]CachedEntry, len(raw))
	for i, e := range raw {
		out[i] = CachedEntry{
			ID:            e.ID,
			CapturedAt:    e.CapturedAt,
			Kind:          e.Kind,
			Summary:       e.Summary,
			SentryEventID: e.SentryEventID,
		}
	}
	return out, nil
}

// GenerateLocalReport builds a redacted JSON crash report.
//
// controls-and-readouts-that-tell-the-truth-01PMZ808 WP12 (FR-017):
// coresentry.AppendToCache had exactly one hit repo-wide (its own
// declaration) — the reader chain (CrashReportingPanel.vue ->
// Sentry_GetLastFive -> GetLastFive) was complete, so the panel showed
// "Recent crash events (last 0)" forever regardless of how many local
// reports had actually been generated. This is the crash path
// AppendToCache's own doc names as the intended caller.
func (s *Impl) GenerateLocalReport(ctx context.Context) (LocalReportResult, error) {
	path, n, err := coresentry.GenerateLocalReport(ctx, s.DataDir)
	if err != nil {
		return LocalReportResult{}, err
	}
	id := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	coresentry.AppendToCache(s.DataDir, coresentry.CacheEntry{
		ID:         id,
		CapturedAt: time.Now().UTC(),
		Kind:       "local_report",
		Summary:    "Local crash report generated",
	})
	return LocalReportResult{Path: path, ByteCount: n}, nil
}

// TestDSN validates and pings a Sentry DSN.
func (s *Impl) TestDSN(_ context.Context, dsn string) (DSNTestResult, error) {
	ok, errMsg := coresentry.TestDSN(dsn)
	return DSNTestResult{OK: ok, Error: errMsg}, nil
}
