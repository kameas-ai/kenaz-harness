package sentry

import (
	"context"

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
func (s *Impl) GenerateLocalReport(ctx context.Context) (LocalReportResult, error) {
	path, n, err := coresentry.GenerateLocalReport(ctx, s.DataDir)
	if err != nil {
		return LocalReportResult{}, err
	}
	return LocalReportResult{Path: path, ByteCount: n}, nil
}

// TestDSN validates and pings a Sentry DSN.
func (s *Impl) TestDSN(_ context.Context, dsn string) (DSNTestResult, error) {
	ok, errMsg := coresentry.TestDSN(dsn)
	return DSNTestResult{OK: ok, Error: errMsg}, nil
}
