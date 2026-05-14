// Package export renders session transcripts to Markdown or JSON for
// local-disk export (session-export-01NDFSEX05 spec FR-001–FR-008).
//
// The public surface is pure: Render has no I/O side effects and is
// trivially unit-testable. The caller (core/rpc/views/sessions) is
// responsible for the Cedar gate, file-picker invocation, and audit
// emission.
package export

import (
	"fmt"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/session"
)

// Format is the export serialisation target.
type Format string

const (
	// FormatMarkdown renders a human-readable Markdown transcript.
	FormatMarkdown Format = "markdown"
	// FormatJSON renders a lossless, schema-versioned JSON payload.
	FormatJSON Format = "json"
)

// ExportFormatVersion is the stable schema version embedded in both
// formats. Increment when the schema shape changes in a breaking way.
const ExportFormatVersion = 1

// ArtifactSidecar describes an inline attachment the renderer extracted
// and wants written alongside the main export file.
type ArtifactSidecar struct {
	// RelPath is the path relative to the main export file's directory
	// (e.g. "my-session-2026-05-14-artifacts/image.png").
	RelPath string
	// Bytes is the raw attachment content.
	Bytes []byte
}

// Render serialises session + msgs into the chosen format.
// It applies RedactValue to every string field in every message before
// serialising so credential bytes never reach the output file.
//
// Returns: (mainContent, sidecars, error). sidecars is non-empty only
// when format == FormatMarkdown and messages carry attached file bytes.
// JSON embeds attachment bytes inline as base64 (no sidecars).
func Render(
	format Format,
	sess session.Record,
	msgs []session.Message,
	now time.Time,
) ([]byte, []ArtifactSidecar, error) {
	// Redact every string field in every message copy.
	redacted := redactMessages(msgs)

	switch format {
	case FormatMarkdown:
		b, sidecars, err := renderMarkdown(sess, redacted, now)
		if err != nil {
			return nil, nil, fmt.Errorf("export: markdown: %w", err)
		}
		return b, sidecars, nil
	case FormatJSON:
		b, err := renderJSON(sess, redacted, now)
		if err != nil {
			return nil, nil, fmt.Errorf("export: json: %w", err)
		}
		return b, nil, nil
	default:
		return nil, nil, fmt.Errorf("export: unknown format %q", format)
	}
}

// DefaultFilename returns the suggested filename for the export file.
// Title is sanitised for filesystem safety (spaces → hyphens, special
// chars stripped). date is formatted YYYY-MM-DD.
func DefaultFilename(title string, format Format, date time.Time) string {
	slug := sanitiseTitle(title)
	if slug == "" {
		slug = "session"
	}
	dateStr := date.UTC().Format("2006-01-02")
	var ext string
	switch format {
	case FormatMarkdown:
		ext = ".md"
	case FormatJSON:
		ext = ".json"
	default:
		ext = ".txt"
	}
	return slug + "-" + dateStr + ext
}
