package sentry

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const reportSubDir = "crash-reports"

// localReport is the JSON shape written to disk.
type localReport struct {
	GeneratedAt   string       `json:"generated_at"`
	HarnessVersion string      `json:"harness_version"`
	OS            string       `json:"os"`
	GoVersion     string       `json:"go_version"`
	Arch          string       `json:"arch"`
	Breadcrumbs   []Breadcrumb `json:"breadcrumbs"`
	LastFive      []CacheEntry `json:"last_five"`
}

// GenerateLocalReport creates a redacted JSON crash report at
// <dataDir>/crash-reports/YYYY-MM-DD-HHMMSS.json.
//
// Returns the file path and byte count. This function never panics; it
// returns an error on filesystem failures.
//
// wire-up point 4 for sentry local-report (sentry-error-monitoring-01KX5R8G WP06).
func GenerateLocalReport(_ context.Context, dataDir string) (path string, n int64, err error) {
	dir := filepath.Join(dataDir, reportSubDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, fmt.Errorf("crash-reports mkdir: %w", err)
	}
	now := time.Now().UTC()
	filename := now.Format("2006-01-02-150405") + ".json"
	path = filepath.Join(dir, filename)

	// Collect redacted breadcrumbs.
	rawCrumbs := SnapshotBreadcrumbs()
	redactedCrumbs := make([]Breadcrumb, len(rawCrumbs))
	for i, b := range rawCrumbs {
		redactedCrumbs[i] = Breadcrumb{
			TS:      b.TS,
			Level:   b.Level,
			Message: RedactString(b.Message),
			Data:    RedactMap(b.Data),
		}
	}

	report := localReport{
		GeneratedAt:    now.Format(time.RFC3339Nano),
		HarnessVersion: harnessVersion(),
		OS:             runtime.GOOS,
		GoVersion:      runtime.Version(),
		Arch:           runtime.GOARCH,
		Breadcrumbs:    redactedCrumbs,
		LastFive:       GetLastFive(dataDir),
	}

	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", 0, fmt.Errorf("marshal report: %w", err)
	}

	// Final privacy pass: run the raw JSON bytes through the string redactor
	// to catch any secret that slipped through structured redaction.
	sanitized := RedactString(string(b))

	if err := os.WriteFile(path, []byte(sanitized), 0o600); err != nil {
		return "", 0, fmt.Errorf("write report: %w", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return path, int64(len(sanitized)), nil
	}
	return path, info.Size(), nil
}

// harnessVersion returns the build version string. Populated by main.Version
// via the package-level variable below; defaults to "dev".
var harnessVersionStr = "dev"

func harnessVersion() string { return harnessVersionStr }

// SetHarnessVersion is called from main.go to inject the build version.
func SetHarnessVersion(v string) {
	if v != "" {
		harnessVersionStr = v
	}
}
