// Package log — audit export pipeline.
//
// Export writes a filtered snapshot of the audit log to a file in the
// caller-specified format (csv, jsonl, or pdf). Files are written to
// <DataDir>/audit-exports/YYYY-MM-DD-HHMMSS.<ext>. The function
// returns the absolute path of the written file.
//
// Redaction: the export pipeline inherits the server-side redaction
// that runs before any Row reaches the store. Export does NOT re-apply
// an additional redaction pass; it relies on the fact that Payload bytes
// stored in the backend have already been processed by the redaction
// pipeline (privacy CI invariant #2).
//
// Secret-literal guard: the payload bytes are scanned for a set of
// known-bad literal patterns (API key prefixes, etc.) before the file
// is flushed. Any row containing a match is replaced with a sentinel
// byte sequence so the secret never lands in the output file.
package log

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ExportFormat is the file format for an audit export.
type ExportFormat string

const (
	ExportFormatCSV   ExportFormat = "csv"
	ExportFormatJSONL ExportFormat = "jsonl"
	ExportFormatPDF   ExportFormat = "pdf"
)

// ExportOptions carries options for an export run.
type ExportOptions struct {
	// DataDir is the root directory where audit-exports/ will be created.
	DataDir string
	// Filter narrows which rows are exported.
	Filter FilterQuery
	// Format is the output format.
	Format ExportFormat
	// HarnessVersion is embedded in PDF headers.
	HarnessVersion string
	// GitSHA is embedded in PDF headers.
	GitSHA string
	// ChainStatus is embedded in PDF headers ("verified" / "unverified").
	ChainStatus string
}

// Export writes the audit export file and returns its absolute path.
// The export directory is created if it does not exist.
func Export(ctx context.Context, backend Backend, opts ExportOptions) (string, error) {
	if opts.DataDir == "" {
		opts.DataDir = "."
	}
	dir := filepath.Join(opts.DataDir, "audit-exports")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("audit export: create dir %q: %w", dir, err)
	}

	ts := time.Now().UTC().Format("2006-01-02-150405")
	ext := string(opts.Format)
	outPath := filepath.Join(dir, fmt.Sprintf("%s.%s", ts, ext))

	rows, err := opts.Filter.ApplyToMemoryBackend(ctx, backend)
	if err != nil {
		return "", fmt.Errorf("audit export: query: %w", err)
	}

	switch opts.Format {
	case ExportFormatCSV:
		if err := exportCSV(outPath, rows); err != nil {
			return "", err
		}
	case ExportFormatJSONL:
		if err := exportJSONL(outPath, rows); err != nil {
			return "", err
		}
	case ExportFormatPDF:
		if err := exportPDF(outPath, rows, opts); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("audit export: unknown format %q", opts.Format)
	}

	return outPath, nil
}

// redactSecrets replaces known-bad literal patterns (e.g. API key prefixes)
// with a fixed sentinel. This is a last-resort guard; the canonical
// redaction pipeline should already have cleaned the payload.
func redactSecrets(b []byte) []byte {
	sentinel := []byte("[REDACTED]")
	// Known bad prefixes for common API key families.
	badPrefixes := [][]byte{
		[]byte("sk-ant-"),  // Anthropic
		[]byte("sk-"),      // OpenAI
		[]byte("AIzaSy"),   // Google AI
		[]byte("Bearer "),  // bare auth token prefix
	}
	out := b
	for _, prefix := range badPrefixes {
		if containsBytes(out, prefix) {
			out = sentinel
			break
		}
	}
	return out
}

func containsBytes(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return false
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		match := true
		for j, nb := range needle {
			if haystack[i+j] != nb {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
