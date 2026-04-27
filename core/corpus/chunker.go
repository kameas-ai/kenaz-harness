package corpus

import (
	"strings"
)

// DefaultChunkLines is the line-based chunker's target window. 50 lines
// is the spec default — small enough to fit several chunks under the 16
// KiB token-budget cap, large enough that each chunk carries meaningful
// context.
const DefaultChunkLines = 50

// rawChunk is the chunker's internal output before SQL row materialization.
type rawChunk struct {
	Text      string
	LineStart int // 1-based inclusive
	LineEnd   int // 1-based inclusive
	Seq       int // 0-based ordering within file
}

// chunkContent applies the appropriate chunker for the file extension.
// Markdown gets heading-aware splitting; everything else uses a fixed
// line window. AST-aware chunking (tree-sitter) is explicitly deferred
// per the mission spec — line-based for code in v1.
func chunkContent(content, ext string, target int) []rawChunk {
	if target <= 0 {
		target = DefaultChunkLines
	}
	if isMarkdownExt(ext) {
		return chunkMarkdown(content, target)
	}
	return chunkLines(content, target)
}

// chunkLines splits content into fixed-line windows. The last window
// may be shorter than target. Empty content yields no chunks.
func chunkLines(content string, target int) []rawChunk {
	lines := splitLines(content)
	if len(lines) == 0 {
		return nil
	}
	out := make([]rawChunk, 0, (len(lines)+target-1)/target)
	seq := 0
	for i := 0; i < len(lines); i += target {
		end := i + target
		if end > len(lines) {
			end = len(lines)
		}
		text := strings.Join(lines[i:end], "\n")
		if strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, rawChunk{
			Text:      text,
			LineStart: i + 1,
			LineEnd:   end,
			Seq:       seq,
		})
		seq++
	}
	return out
}

// chunkMarkdown splits content at heading boundaries (`#`, `##`, `###`,
// etc.). Each chunk is the lines from one heading up to (but not
// including) the next heading. If a section runs longer than target *
// 2, it is split with chunkLines internally so a giant section still
// produces multiple chunks. A leading preamble (lines before any
// heading) becomes its own chunk.
func chunkMarkdown(content string, target int) []rawChunk {
	lines := splitLines(content)
	if len(lines) == 0 {
		return nil
	}
	type section struct {
		start int // 1-based
		end   int // 1-based inclusive
	}
	sections := make([]section, 0, 8)
	current := section{start: 1}
	for i, l := range lines {
		if isMarkdownHeading(l) && i > 0 {
			current.end = i // i is 0-based; section ends at line i (lines[i-1])
			sections = append(sections, current)
			current = section{start: i + 1}
		}
	}
	current.end = len(lines)
	sections = append(sections, current)

	out := make([]rawChunk, 0, len(sections))
	seq := 0
	for _, s := range sections {
		if s.end < s.start {
			continue
		}
		section := lines[s.start-1 : s.end]
		if strings.TrimSpace(strings.Join(section, "\n")) == "" {
			continue
		}
		// Long sections still get line-windowed so 16 KiB token budget holds.
		if len(section) > target*2 {
			for i := 0; i < len(section); i += target {
				end := i + target
				if end > len(section) {
					end = len(section)
				}
				slice := section[i:end]
				if strings.TrimSpace(strings.Join(slice, "\n")) == "" {
					continue
				}
				out = append(out, rawChunk{
					Text:      strings.Join(slice, "\n"),
					LineStart: s.start + i,
					LineEnd:   s.start + end - 1,
					Seq:       seq,
				})
				seq++
			}
			continue
		}
		out = append(out, rawChunk{
			Text:      strings.Join(section, "\n"),
			LineStart: s.start,
			LineEnd:   s.end,
			Seq:       seq,
		})
		seq++
	}
	return out
}

// splitLines is a stdlib-only line splitter that preserves byte
// positions (no trailing-empty-line surprise like strings.Split).
func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	// Normalize CRLF -> LF for downstream consumers.
	if strings.Contains(content, "\r\n") {
		content = strings.ReplaceAll(content, "\r\n", "\n")
	}
	// Trim trailing newline so we don't yield an empty trailing line.
	if strings.HasSuffix(content, "\n") {
		content = content[:len(content)-1]
	}
	return strings.Split(content, "\n")
}

// isMarkdownHeading returns true if line starts with one or more #
// followed by a space — the ATX heading form. Setext headings
// (underlines) are intentionally NOT detected; they're rare in
// engineering corpora and the cost/benefit isn't there for v1.
func isMarkdownHeading(line string) bool {
	s := strings.TrimLeft(line, " \t")
	if s == "" || s[0] != '#' {
		return false
	}
	level := 0
	for level < len(s) && s[level] == '#' {
		level++
	}
	if level == 0 || level > 6 {
		return false
	}
	if level == len(s) {
		return false
	}
	return s[level] == ' ' || s[level] == '\t'
}

// isMarkdownExt reports whether ext (lowercased, including the dot)
// names a markdown file.
func isMarkdownExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".md", ".markdown", ".mdx":
		return true
	}
	return false
}
