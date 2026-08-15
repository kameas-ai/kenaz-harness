package export

import (
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/session"
)

// renderMarkdown produces the Markdown export of a session.
// Returns (bytes, sidecars, error). sidecars carry attachment bytes
// for files that the caller should write to the sibling -artifacts/ dir.
func renderMarkdown(
	sess session.Record,
	msgs []session.Message,
	now time.Time,
) ([]byte, []ArtifactSidecar, error) {
	var sb strings.Builder
	var sidecars []ArtifactSidecar

	title := sess.Name
	if title == "" {
		title = sess.ID
	}

	// ── Meta header ─────────────────────────────────────────────────────
	sb.WriteString(fmt.Sprintf("# %s\n\n", escapeMarkdown(title)))
	sb.WriteString("<!-- kenaz-harness export -->\n")
	sb.WriteString(fmt.Sprintf("<!-- export_format_version: %d -->\n", ExportFormatVersion))
	sb.WriteString(fmt.Sprintf("<!-- exported_at: %s -->\n", now.UTC().Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("<!-- session_id: %s -->\n", sess.ID))
	sb.WriteString("\n---\n\n")

	artifactDir := sanitiseTitle(title) + "-" + now.UTC().Format("2006-01-02") + "-artifacts"

	// ── Turns ────────────────────────────────────────────────────────────
	//
	// The document is TURNS, not rows (WP05, see moves.go). Move rows
	// fold into the turn they belong to and never take a heading of their
	// own; a classic session has none, so `idx` still runs 1..len(msgs)
	// and the output is byte-identical to the pre-WP05 renderer.
	for idx, item := range projectExportItems(msgs) {
		m := item.Msg
		role := string(m.Role)
		heading := fmt.Sprintf("## Turn %d — %s", idx+1, titleCase(role))
		if item.TrailOnly {
			heading += " (trajectory only)"
		}
		sb.WriteString(heading + "\n\n")

		// Message body (preserve code fences).
		body := m.Content
		if body != "" {
			sb.WriteString(body)
			if !strings.HasSuffix(body, "\n") {
				sb.WriteByte('\n')
			}
			sb.WriteByte('\n')
		}

		// Tool calls as collapsible <details>.
		//
		// ARGUMENT VALUES ARE NOT EXPORTED. This used to dump
		// `tc.Arguments` as raw JSON; a secret nested inside an argument
		// object or an array sailed past the credential scanner and into
		// the document. The summary is names and value types only.
		for _, tc := range m.ToolCalls {
			sb.WriteString("<details>\n")
			sb.WriteString(fmt.Sprintf("<summary>Tool call: <code>%s</code></summary>\n\n", tc.Name))
			sb.WriteString("**Arguments (names and types):**\n\n```\n")
			sb.WriteString(argsSummaryOrNone(argsSummaryFromValues(tc.Arguments)))
			sb.WriteString("\n```\n\n")
			if tc.Result != "" {
				sb.WriteString("**Result:**\n\n```\n")
				sb.WriteString(capToolOutput(tc.Result))
				sb.WriteString("\n```\n\n")
			}
			sb.WriteString("</details>\n\n")
		}

		// The trajectory that produced this answer, in the same
		// disclosure the chat surface collapses it into.
		writeTrajectory(&sb, item.Trail)

		// Attachments (ContentBlocks with image/document type).
		for bIdx, block := range m.ContentBlocks {
			switch block.Type {
			case "image":
				if block.Source != nil && block.Source.Data != "" {
					imgBytes, err := base64.StdEncoding.DecodeString(block.Source.Data)
					if err == nil {
						ext := mediaTypeExt(block.Source.MediaType)
						relName := fmt.Sprintf("turn%d-img%d%s", idx+1, bIdx+1, ext)
						relPath := filepath.Join(artifactDir, relName)
						sidecars = append(sidecars, ArtifactSidecar{
							RelPath: relPath,
							Bytes:   imgBytes,
						})
						sb.WriteString(fmt.Sprintf("![attachment](%s)\n\n", relPath))
					}
				} else if block.Source != nil && block.Source.URI != "" {
					sb.WriteString(fmt.Sprintf("![attachment](%s)\n\n", block.Source.URI))
				}
			case "document":
				if block.Source != nil && block.Source.Data != "" {
					docBytes, err := base64.StdEncoding.DecodeString(block.Source.Data)
					if err == nil {
						ext := mediaTypeExt(block.Source.MediaType)
						relName := fmt.Sprintf("turn%d-doc%d%s", idx+1, bIdx+1, ext)
						relPath := filepath.Join(artifactDir, relName)
						sidecars = append(sidecars, ArtifactSidecar{
							RelPath: relPath,
							Bytes:   docBytes,
						})
						sb.WriteString(fmt.Sprintf("[Attached document](%s)\n\n", relPath))
					}
				}
			}
		}
	}

	return []byte(sb.String()), sidecars, nil
}

// argsSummaryOrNone keeps the fenced block non-empty for a tool invoked
// with no arguments, which reads better than a blank fence.
func argsSummaryOrNone(summary string) string {
	if summary == "" {
		return "(no arguments)"
	}
	return summary
}

// writeTrajectory renders one turn's intermediate moves inside a single
// <details> disclosure — the export's answer to FR-004's collapse. A
// reader scanning the conversation sees the answers; a reader auditing
// what the model did opens the fold.
func writeTrajectory(sb *strings.Builder, trail []session.Message) {
	if len(trail) == 0 {
		return
	}
	sb.WriteString("<details>\n")
	sb.WriteString(fmt.Sprintf("<summary>Trajectory — %d moves</summary>\n\n", len(trail)))
	for i, row := range trail {
		d := describeMove(row)
		n := i + 1
		switch d.Kind {
		case string(session.MoveKindToolCall):
			sb.WriteString(fmt.Sprintf("**%d. Tool call** `%s` — args: `%s`\n\n",
				n, d.ToolName, argsSummaryOrNone(d.ArgsSummary)))
		case string(session.MoveKindToolResult):
			status := "ok"
			if d.IsError {
				status = "error"
			}
			sb.WriteString(fmt.Sprintf("**%d. Tool result** `%s` — %s\n\n",
				n, d.ToolName, status))
			if d.Output != "" {
				sb.WriteString("```\n")
				sb.WriteString(d.Output)
				sb.WriteString("\n```\n\n")
			}
		default:
			sb.WriteString(fmt.Sprintf("**%d.** ", n))
			sb.WriteString(d.Text)
			sb.WriteString("\n\n")
		}
	}
	sb.WriteString("</details>\n\n")
}

// escapeMarkdown escapes characters that have special meaning in Markdown
// headings (backtick, pipe, brackets).
func escapeMarkdown(s string) string {
	s = strings.ReplaceAll(s, "`", "\\`")
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "[", "\\[")
	s = strings.ReplaceAll(s, "]", "\\]")
	return s
}

// titleCase capitalises the first letter of a word.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// mediaTypeExt converts a MIME media type to a file extension.
func mediaTypeExt(mediaType string) string {
	switch mediaType {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "application/pdf":
		return ".pdf"
	default:
		return ".bin"
	}
}
