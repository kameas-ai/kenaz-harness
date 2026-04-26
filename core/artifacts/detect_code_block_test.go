package artifacts_test

import (
	"strings"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/artifacts"
)

// repeatLines emits n distinct lines so blocks meet the line
// threshold without all-blank-line shortcuts.
func repeatLines(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString("line ")
		b.WriteString("xxxxx\n")
	}
	return b.String()
}

func TestDetectCodeBlocks_AttrTitle_Captures(t *testing.T) {
	t.Parallel()
	body := repeatLines(12)
	msg := "before\n" +
		"```html title=\"page.html\"\n" +
		body +
		"```\n" +
		"after\n"
	got := artifacts.DetectCodeBlocks("m1", msg, artifacts.DefaultCodeBlockConfig())
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Title != "page.html" {
		t.Errorf("Title = %q", got[0].Title)
	}
	if got[0].MimeType != "text/html" {
		t.Errorf("MimeType = %q", got[0].MimeType)
	}
	if got[0].SourceRef.MessageID != "m1" || got[0].SourceRef.CodeBlockIndex != 0 {
		t.Errorf("SourceRef = %+v", got[0].SourceRef)
	}
	if got[0].SourceRef.Filename != "page.html" {
		t.Errorf("SourceRef.Filename = %q", got[0].SourceRef.Filename)
	}
}

func TestDetectCodeBlocks_FirstLineCommentSlashSlash_Captures(t *testing.T) {
	t.Parallel()
	body := "// file: foo.go\n" + repeatLines(11)
	msg := "```go\n" + body + "```\n"
	got := artifacts.DetectCodeBlocks("m1", msg, artifacts.DefaultCodeBlockConfig())
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Title != "foo.go" {
		t.Errorf("Title = %q", got[0].Title)
	}
	if got[0].MimeType != "text/x-go" {
		t.Errorf("MimeType = %q", got[0].MimeType)
	}
}

func TestDetectCodeBlocks_FirstLineCommentHash_Captures(t *testing.T) {
	t.Parallel()
	body := "# file: data.py\n" + repeatLines(11)
	msg := "```python\n" + body + "```\n"
	got := artifacts.DetectCodeBlocks("m1", msg, artifacts.DefaultCodeBlockConfig())
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Title != "data.py" {
		t.Errorf("Title = %q", got[0].Title)
	}
	if got[0].MimeType != "text/x-python" {
		t.Errorf("MimeType = %q", got[0].MimeType)
	}
}

func TestDetectCodeBlocks_FirstLineCommentHTML_Captures(t *testing.T) {
	t.Parallel()
	body := "<!-- file: page.html -->\n" + repeatLines(11)
	msg := "```\n" + body + "```\n"
	got := artifacts.DetectCodeBlocks("m1", msg, artifacts.DefaultCodeBlockConfig())
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Title != "page.html" {
		t.Errorf("Title = %q", got[0].Title)
	}
}

func TestDetectCodeBlocks_FirstLineCommentSlashStar_Captures(t *testing.T) {
	t.Parallel()
	body := "/* file: app.css */\n" + repeatLines(11)
	msg := "```css\n" + body + "```\n"
	got := artifacts.DetectCodeBlocks("m1", msg, artifacts.DefaultCodeBlockConfig())
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Title != "app.css" || got[0].MimeType != "text/css" {
		t.Errorf("Title/MimeType = %q / %q", got[0].Title, got[0].MimeType)
	}
}

func TestDetectCodeBlocks_NoTitleHint_DropsBlock(t *testing.T) {
	t.Parallel()
	body := repeatLines(20) // way over thresholds
	msg := "```go\n" + body + "```\n"
	got := artifacts.DetectCodeBlocks("m1", msg, artifacts.DefaultCodeBlockConfig())
	if len(got) != 0 {
		t.Errorf("len = %d, want 0 (no title hint)", len(got))
	}
}

func TestDetectCodeBlocks_TitleButTooSmall_DropsBlock(t *testing.T) {
	t.Parallel()
	// 5 lines, ~50 bytes — well under both thresholds.
	body := "1\n2\n3\n4\n5\n"
	msg := "```html title=\"x.html\"\n" + body + "```\n"
	got := artifacts.DetectCodeBlocks("m1", msg, artifacts.DefaultCodeBlockConfig())
	if len(got) != 0 {
		t.Errorf("len = %d, want 0 (under thresholds)", len(got))
	}
}

func TestDetectCodeBlocks_TitleAndOverByteThreshold_Captures(t *testing.T) {
	t.Parallel()
	// 6 lines (under 10) but 250 bytes (over 200) — captured via the OR.
	body := strings.Repeat("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n", 6)
	if len(body) < 200 {
		t.Fatalf("test setup: body too short (%d)", len(body))
	}
	msg := "```\n" + "// file: blob.txt\n" + body + "```\n"
	got := artifacts.DetectCodeBlocks("m1", msg, artifacts.DefaultCodeBlockConfig())
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Title != "blob.txt" {
		t.Errorf("Title = %q", got[0].Title)
	}
}

func TestDetectCodeBlocks_MultipleBlocks_AssignsCodeBlockIndex(t *testing.T) {
	t.Parallel()
	body := repeatLines(12)
	msg := "intro\n" +
		"```html title=\"a.html\"\n" + body + "```\n" +
		"middle\n" +
		"```html title=\"b.html\"\n" + body + "```\n"
	got := artifacts.DetectCodeBlocks("m1", msg, artifacts.DefaultCodeBlockConfig())
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].SourceRef.CodeBlockIndex != 0 {
		t.Errorf("first index = %d", got[0].SourceRef.CodeBlockIndex)
	}
	if got[1].SourceRef.CodeBlockIndex != 1 {
		t.Errorf("second index = %d", got[1].SourceRef.CodeBlockIndex)
	}
	if got[0].Title != "a.html" || got[1].Title != "b.html" {
		t.Errorf("titles = %q / %q", got[0].Title, got[1].Title)
	}
}

func TestDetectCodeBlocks_PathTraversalSanitized(t *testing.T) {
	t.Parallel()
	body := repeatLines(12)
	msg := "```html title=\"../../etc/passwd\"\n" + body + "```\n"
	got := artifacts.DetectCodeBlocks("m1", msg, artifacts.DefaultCodeBlockConfig())
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if strings.Contains(got[0].Title, "/") || strings.Contains(got[0].Title, "\\") {
		t.Errorf("Title still has path sep: %q", got[0].Title)
	}
	if got[0].Title != ".._.._etc_passwd" {
		t.Errorf("Title = %q, want sanitized", got[0].Title)
	}
}

func TestDetectCodeBlocks_UnterminatedFence_Dropped(t *testing.T) {
	t.Parallel()
	body := repeatLines(12)
	msg := "```html title=\"x.html\"\n" + body + "(no closing fence)\n"
	got := artifacts.DetectCodeBlocks("m1", msg, artifacts.DefaultCodeBlockConfig())
	if len(got) != 0 {
		t.Errorf("len = %d, want 0 (unterminated)", len(got))
	}
}

func TestDetectCodeBlocks_RespectsCustomConfig(t *testing.T) {
	t.Parallel()
	// 5-line block with a title — default config rejects, custom
	// config (MinLines=3) accepts.
	body := "1\n2\n3\n4\n5\n"
	msg := "```html title=\"x.html\"\n" + body + "```\n"
	got := artifacts.DetectCodeBlocks("m1", msg, artifacts.CodeBlockDetectorConfig{MinLines: 3, MinBytes: 1000})
	if len(got) != 1 {
		t.Errorf("custom-config len = %d, want 1", len(got))
	}
}
