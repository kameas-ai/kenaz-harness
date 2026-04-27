package corpus

import (
	"strings"
	"testing"
)

func TestChunkLines_FixedWindow(t *testing.T) {
	t.Parallel()
	content := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"
	chunks := chunkLines(content, 4)
	if len(chunks) != 3 {
		t.Fatalf("len = %d, want 3", len(chunks))
	}
	if chunks[0].LineStart != 1 || chunks[0].LineEnd != 4 {
		t.Errorf("chunk0 lines = (%d, %d)", chunks[0].LineStart, chunks[0].LineEnd)
	}
	if chunks[1].LineStart != 5 || chunks[1].LineEnd != 8 {
		t.Errorf("chunk1 lines = (%d, %d)", chunks[1].LineStart, chunks[1].LineEnd)
	}
	if chunks[2].LineStart != 9 || chunks[2].LineEnd != 10 {
		t.Errorf("chunk2 lines = (%d, %d)", chunks[2].LineStart, chunks[2].LineEnd)
	}
	if chunks[0].Seq != 0 || chunks[2].Seq != 2 {
		t.Errorf("seq mismatch")
	}
}

func TestChunkLines_EmptyContent(t *testing.T) {
	t.Parallel()
	chunks := chunkLines("", 5)
	if len(chunks) != 0 {
		t.Errorf("len = %d, want 0", len(chunks))
	}
}

func TestChunkMarkdown_HeadingSplit(t *testing.T) {
	t.Parallel()
	content := "# A\n\nalpha\n\n# B\n\nbeta\n\n# C\n\ngamma\n"
	chunks := chunkMarkdown(content, DefaultChunkLines)
	if len(chunks) < 3 {
		t.Fatalf("len = %d, want >=3", len(chunks))
	}
	for i, c := range chunks {
		if !strings.Contains(c.Text, "#") && i > 0 {
			t.Errorf("chunk[%d] missing heading: %q", i, c.Text)
		}
	}
}

func TestChunkMarkdown_LongSectionSplits(t *testing.T) {
	t.Parallel()
	long := "# Title\n\n" + strings.Repeat("line\n", 200)
	chunks := chunkMarkdown(long, 50)
	if len(chunks) < 2 {
		t.Errorf("long section produced %d chunks, want >=2", len(chunks))
	}
}

func TestIsMarkdownHeading(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"# heading":     true,
		"## heading":    true,
		"### heading":   true,
		"#####  h":      true,
		"#":             false,
		"#noSpace":      false,
		"   # indented": true,
		"text":          false,
		"":              false,
		"####### too":   false,
	}
	for line, want := range cases {
		if got := isMarkdownHeading(line); got != want {
			t.Errorf("isMarkdownHeading(%q) = %v, want %v", line, got, want)
		}
	}
}

func TestSplitLines_NoTrailingEmpty(t *testing.T) {
	t.Parallel()
	got := splitLines("a\nb\n")
	if len(got) != 2 {
		t.Errorf("len = %d, want 2 (got %v)", len(got), got)
	}
}

func TestChunkContent_Routing(t *testing.T) {
	t.Parallel()
	mdContent := "# A\n\nbody1\n\n# B\n\nbody2\n"
	mdChunks := chunkContent(mdContent, ".md", 50)
	codeChunks := chunkContent(mdContent, ".go", 50)
	// Markdown should split on heading; code should treat as one window.
	if len(mdChunks) < 2 {
		t.Errorf("markdown chunks = %d, want >=2", len(mdChunks))
	}
	if len(codeChunks) != 1 {
		t.Errorf("code chunks = %d, want 1", len(codeChunks))
	}
}
