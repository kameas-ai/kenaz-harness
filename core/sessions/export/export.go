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

	"github.com/kameas-ai/kenaz-harness/core/session"
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
//
// 1 → 2 (model-moves-transcript-01PMCH01 WP05, adversarial review):
// `tool_calls[].arguments` was REMOVED from the JSON export and the
// markdown `**Arguments:**` raw-JSON block replaced by a names-and-types
// summary, because a credential nested in an argument object or an array
// walked straight past `RedactValue` into the exported file. Most of
// WP05 is additive — `kind`, `turn_span_id`, `moves`, `trajectory_only`
// are all `omitempty` and a v1 reader ignores them — but a removal is
// not additive, and the rule above is the rule. A v1 reader that walked
// `arguments` now finds nothing there and has no way to tell that from
// "this call had no arguments"; the version says which world it is in.
const ExportFormatVersion = 2

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
// It applies RedactValue to every string field the renderers can reach —
// in the messages AND in the session row — before serialising, so
// credential bytes never reach the output file.
//
// The session row is scanned here rather than by the caller because the
// caller cannot: `redactMessages` is named for what it walks, and until
// 2026-08-16 nothing at all scanned `sess.Name` or `sess.SystemPrompt`,
// both of which the renderers print.
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
	// Redact every string field in every message copy, and in the
	// session row the renderers print alongside them.
	redacted := redactMessages(msgs)
	sess = redactRecord(sess)

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
// Title is scanned for credentials and then sanitised for filesystem
// safety (spaces → hyphens, special chars stripped). date is formatted
// YYYY-MM-DD.
//
// The scan is not belt-and-braces: the only caller passes the raw
// `session.Record.Name`, so before 2026-08-16 a key pasted into a session
// title was proposed to the OS save dialog as the FILENAME — the one
// piece of an export that survives being opened in a screenshot.
func DefaultFilename(title string, format Format, date time.Time) string {
	title, _ = RedactValue(title)
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
