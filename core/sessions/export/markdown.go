package export

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/session"
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
	sb.WriteString("<!-- kaneaz-harness export -->\n")
	sb.WriteString(fmt.Sprintf("<!-- export_format_version: %d -->\n", ExportFormatVersion))
	sb.WriteString(fmt.Sprintf("<!-- exported_at: %s -->\n", now.UTC().Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("<!-- session_id: %s -->\n", sess.ID))
	sb.WriteString("\n---\n\n")

	artifactDir := sanitiseTitle(title) + "-" + now.UTC().Format("2006-01-02") + "-artifacts"

	// ── Turns ────────────────────────────────────────────────────────────
	for idx, m := range msgs {
		role := string(m.Role)
		heading := fmt.Sprintf("## Turn %d — %s", idx+1, titleCase(role))
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
		for _, tc := range m.ToolCalls {
			sb.WriteString("<details>\n")
			sb.WriteString(fmt.Sprintf("<summary>Tool call: <code>%s</code></summary>\n\n", tc.Name))
			sb.WriteString("**Arguments:**\n\n```json\n")
			sb.WriteString(formatToolArgs(tc.Arguments))
			sb.WriteString("\n```\n\n")
			if tc.Result != "" {
				sb.WriteString("**Result:**\n\n```\n")
				sb.WriteString(tc.Result)
				sb.WriteString("\n```\n\n")
			}
			sb.WriteString("</details>\n\n")
		}

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

// formatToolArgs renders a map[string]any as indented JSON.
func formatToolArgs(args map[string]any) string {
	if len(args) == 0 {
		return "{}"
	}
	b, err := json.MarshalIndent(args, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
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
