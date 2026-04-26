package websearch

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestExtractor_GoldenPage(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/readable_page.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	e := NewExtractor()
	text, err := e.Extract(context.Background(), body, "https://example.com/post")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if text == "" {
		t.Fatal("empty extraction")
	}
	if !strings.Contains(text, "privacy primitive") {
		t.Errorf("missing main-text content: %q", text)
	}
	// Nav / footer / sidebar boilerplate should be stripped by readability.
	bad := []string{"Subscribe", "© 2026 Local First Co.", "Home"}
	for _, s := range bad {
		if strings.Contains(text, s) {
			t.Errorf("expected boilerplate %q to be stripped, but it survived", s)
		}
	}
}

func TestExtractor_TruncatesAtCap(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/readable_page.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	e := NewExtractorWithCap(200)
	text, err := e.Extract(context.Background(), body, "https://example.com/post")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(text) > 200 {
		t.Errorf("extract over cap: %d bytes", len(text))
	}
	if !strings.HasSuffix(text, truncationMarker) {
		t.Errorf("missing truncation marker: %q", text)
	}
}

func TestExtractor_EmptyBody(t *testing.T) {
	t.Parallel()
	e := NewExtractor()
	_, err := e.Extract(context.Background(), nil, "https://example.com")
	if err == nil {
		t.Fatal("expected error on empty body")
	}
}

func TestExtractor_NonHTMLFallsBack(t *testing.T) {
	t.Parallel()
	// Plain text body — readability typically returns nothing or
	// minimal. We tolerate either real extraction or our raw-text
	// fallback path; the contract is "don't return an empty string
	// when there's salvageable content."
	in := []byte("Just a plain text document with no markup at all. " + strings.Repeat("More words. ", 80))
	e := NewExtractor()
	text, _ := e.Extract(context.Background(), in, "")
	if strings.TrimSpace(text) == "" {
		t.Errorf("expected non-empty text from plain content, got %q", text)
	}
}

func TestExtractor_StripTagsHelper(t *testing.T) {
	t.Parallel()
	got := stripTags("<p>hello <b>world</b></p> <script>x</script>")
	if got != "hello world x" {
		t.Errorf("stripTags = %q", got)
	}
}

func TestExtractor_RespectsContextCancel(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/readable_page.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e := NewExtractor()
	_, err = e.Extract(ctx, body, "https://example.com/post")
	if err == nil {
		t.Fatal("expected ctx-cancel error")
	}
}
