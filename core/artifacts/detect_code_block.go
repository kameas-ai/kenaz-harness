package artifacts

import (
	"path/filepath"
	"strings"
)

// CodeBlockDetectorConfig carries the tunables the code-block walker
// reads. Defaults are mirrored from CaptureConfig — DetectCodeBlocks
// is a pure function that takes its config explicitly so callers can
// hold it stable across a single message walk.
type CodeBlockDetectorConfig struct {
	// MinLines is the lower bound for line count. A block with a
	// title hint and ≥ MinLines lines is captured. Default 10.
	MinLines int
	// MinBytes is the lower bound for byte count. A block with a
	// title hint and ≥ MinBytes bytes is captured. Default 200.
	MinBytes int
}

// DefaultCodeBlockConfig returns the spec's default thresholds.
func DefaultCodeBlockConfig() CodeBlockDetectorConfig {
	return CodeBlockDetectorConfig{MinLines: 10, MinBytes: 200}
}

// DetectCodeBlocks walks the assistant message text for fenced
// markdown blocks and emits a CaptureCandidate for every block that
// (a) carries a filename hint and (b) clears MinLines OR MinBytes.
//
// The fenced-block scanner is a hand-rolled line walker — markdown
// fences are simple enough that a 60-line scanner beats pulling in a
// third-party parser (charter C-001: stdlib only in core/).
//
// Title-hint extraction priority:
//  1. Attr-style after the lang tag: ` ```html title="page.html" `.
//  2. First-line comment: `// file: page.html`, `# file: page.html`,
//     `<!-- file: page.html -->`, `/* file: page.html */`.
//
// Without a title hint a block is dropped — we don't speculate. The
// filename is sanitized for display (path separators → '_') so a
// hostile model can't smuggle path-traversal artifacts into the UI's
// download surface.
func DetectCodeBlocks(messageID, text string, cfg CodeBlockDetectorConfig) []CaptureCandidate {
	if cfg.MinLines <= 0 {
		cfg.MinLines = 10
	}
	if cfg.MinBytes <= 0 {
		cfg.MinBytes = 200
	}

	blocks := scanFencedBlocks(text)
	out := make([]CaptureCandidate, 0, len(blocks))
	for i, b := range blocks {
		filename := extractTitleHint(b.langTag, b.body)
		if filename == "" {
			continue
		}
		lines := countLines(b.body)
		bytes := len(b.body)
		if lines < cfg.MinLines && bytes < cfg.MinBytes {
			continue
		}
		safeName := sanitizeFilename(filename)
		out = append(out, CaptureCandidate{
			Title:    safeName,
			MimeType: inferMimeFromFilename(filename),
			Bytes:    []byte(b.body),
			Source:   SourceCodeBlock,
			SourceRef: ArtifactSourceRef{
				MessageID:      messageID,
				CodeBlockIndex: i,
				Filename:       safeName,
			},
		})
	}
	return out
}

// fencedBlock is the intermediate shape the scanner produces. langTag
// is the raw text after the opening fence ("html title=\"x.html\""
// for `` ```html title="x.html" ``); body is the content between the
// fences with no trailing newline.
type fencedBlock struct {
	langTag string
	body    string
}

// scanFencedBlocks extracts every ```-delimited block from text.
// State machine: outside-block reads lines until one starts with
// "```"; inside-block reads lines until one starts with "```".
// Tildes (~~~) are not honored — we follow the conservative subset
// that keeps the noise floor down.
func scanFencedBlocks(text string) []fencedBlock {
	out := make([]fencedBlock, 0, 4)
	lines := strings.Split(text, "\n")

	i := 0
	for i < len(lines) {
		line := lines[i]
		if !isFenceOpener(line) {
			i++
			continue
		}
		// Open fence: capture lang tag, advance.
		langTag := strings.TrimSpace(strings.TrimLeft(line, "` "))
		i++
		bodyStart := i
		for i < len(lines) {
			if isFenceCloser(lines[i]) {
				break
			}
			i++
		}
		// i now points either to the closing fence or past EOF.
		if i >= len(lines) {
			// Unterminated fence — drop the block. Spec edge case 3:
			// partial blocks are not captured.
			break
		}
		body := strings.Join(lines[bodyStart:i], "\n")
		out = append(out, fencedBlock{langTag: langTag, body: body})
		i++ // step past the closing fence
	}
	return out
}

// isFenceOpener reports whether line begins a fenced block (3+
// backticks, possibly preceded by whitespace).
func isFenceOpener(line string) bool {
	return hasBacktickFence(strings.TrimLeft(line, " \t"))
}

// isFenceCloser is identical for our purposes — closing fences carry
// no language tag in practice but we're permissive on input.
func isFenceCloser(line string) bool {
	return hasBacktickFence(strings.TrimLeft(line, " \t"))
}

func hasBacktickFence(s string) bool {
	if len(s) < 3 {
		return false
	}
	return s[0] == '`' && s[1] == '`' && s[2] == '`'
}

// extractTitleHint returns the filename hint encoded for a block, or
// "" when none is present. Attr-style takes priority over comment
// hints — if both are present, attr wins.
func extractTitleHint(langTag, body string) string {
	if name := extractAttrTitle(langTag); name != "" {
		return name
	}
	return extractFirstLineCommentHint(body)
}

// extractAttrTitle pulls `title="filename"` from the lang tag. Single
// quotes are also accepted. Tag whitespace is permissive.
func extractAttrTitle(langTag string) string {
	idx := strings.Index(langTag, "title=")
	if idx < 0 {
		return ""
	}
	rest := langTag[idx+len("title="):]
	if rest == "" {
		return ""
	}
	q := rest[0]
	if q != '"' && q != '\'' {
		return ""
	}
	end := strings.IndexByte(rest[1:], q)
	if end < 0 {
		return ""
	}
	return rest[1 : 1+end]
}

// extractFirstLineCommentHint walks the first non-empty line of body
// and matches the four supported comment shapes:
//
//	// file: foo.go
//	# file: foo.go
//	<!-- file: foo.go -->
//	/* file: foo.go */
func extractFirstLineCommentHint(body string) string {
	first := firstNonEmptyLine(body)
	if first == "" {
		return ""
	}
	first = strings.TrimSpace(first)

	// // file: ...
	if strings.HasPrefix(first, "//") {
		return parseFileColon(strings.TrimPrefix(first, "//"))
	}
	// # file: ...
	if strings.HasPrefix(first, "#") {
		return parseFileColon(strings.TrimPrefix(first, "#"))
	}
	// <!-- file: ... -->
	if strings.HasPrefix(first, "<!--") {
		s := strings.TrimPrefix(first, "<!--")
		s = strings.TrimSuffix(s, "-->")
		return parseFileColon(s)
	}
	// /* file: ... */
	if strings.HasPrefix(first, "/*") {
		s := strings.TrimPrefix(first, "/*")
		s = strings.TrimSuffix(s, "*/")
		return parseFileColon(s)
	}
	return ""
}

// parseFileColon recognizes "file: <name>" (case-insensitive on
// "file") and returns <name>. Surrounding whitespace is trimmed.
func parseFileColon(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(strings.ToLower(s), "file:") {
		return ""
	}
	s = strings.TrimSpace(s[len("file:"):])
	// Stop at first whitespace — filenames don't have spaces in our
	// supported subset.
	if idx := strings.IndexAny(s, " \t"); idx >= 0 {
		s = s[:idx]
	}
	return s
}

// firstNonEmptyLine returns the first line in body that contains any
// non-whitespace character.
func firstNonEmptyLine(body string) string {
	for _, ln := range strings.Split(body, "\n") {
		if strings.TrimSpace(ln) != "" {
			return ln
		}
	}
	return ""
}

// countLines reports the line count of body. An empty body has 0
// lines; otherwise the count is one more than the number of '\n'
// characters when the body does not end in '\n', else equal.
func countLines(body string) int {
	if body == "" {
		return 0
	}
	n := strings.Count(body, "\n")
	if !strings.HasSuffix(body, "\n") {
		n++
	}
	return n
}

// inferMimeFromFilename maps the file extension to an IANA MIME type.
// Unknown extensions fall through to text/plain — the spec calls out
// the explicit subset; expanding it is a follow-up.
func inferMimeFromFilename(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".html", ".htm":
		return "text/html"
	case ".go":
		return "text/x-go"
	case ".py":
		return "text/x-python"
	case ".md":
		return "text/markdown"
	case ".json":
		return "application/json"
	case ".css":
		return "text/css"
	case ".js":
		return "application/javascript"
	case ".ts":
		return "application/typescript"
	case ".yaml", ".yml":
		return "application/yaml"
	case ".txt":
		return "text/plain"
	}
	return "text/plain"
}

// sanitizeFilename replaces path separators with underscores so a
// hostile title hint can't escape the artifact list display. The
// hint is metadata only — never used in filesystem operations — but
// the UI surface should never render `../` paths verbatim.
func sanitizeFilename(name string) string {
	r := strings.NewReplacer("/", "_", "\\", "_")
	return r.Replace(name)
}
