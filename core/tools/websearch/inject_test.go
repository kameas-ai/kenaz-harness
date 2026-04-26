package websearch

import (
	"strings"
	"testing"
)

func TestWrap_BasicShape(t *testing.T) {
	t.Parallel()
	hit := SearchHit{
		Title:   "Hello",
		URL:     "https://example.com/foo",
		Snippet: "snippet text",
	}
	w := Wrap(hit, "page text")
	if w.Title != "Hello" || w.URL != hit.URL || w.Snippet != hit.Snippet {
		t.Errorf("unexpected pass-through: %+v", w)
	}
	want := `<source url="https://example.com/foo" title="Hello">page text</source>`
	if w.Wrapped != want {
		t.Errorf("Wrapped = %q, want %q", w.Wrapped, want)
	}
}

func TestWrap_EscapesAttributeQuotes(t *testing.T) {
	t.Parallel()
	hit := SearchHit{
		// A title with double quotes + ampersand — must be escaped
		// so the wrapper can't be broken out of.
		Title: `Evil "title" & friends`,
		URL:   `https://example.com/?x="boom"&y=<z>`,
	}
	w := Wrap(hit, "body")

	// Outer < and > belong to the wrapper itself; the inner attribute
	// quotes must not be present un-escaped within the attribute
	// region.
	startTag := w.Wrapped[:strings.Index(w.Wrapped, ">")]
	if strings.Count(startTag, `"`) != 4 {
		// Exactly four ": two opening + two closing for url and title.
		t.Errorf("unexpected quote count in start tag: %q", startTag)
	}
	if strings.Contains(startTag, `"title"`) {
		t.Errorf("unescaped quote leaked into start tag: %q", startTag)
	}
	if !strings.Contains(startTag, "&amp;") {
		t.Errorf("ampersand not escaped: %q", startTag)
	}
}

func TestSystemPromptAdditionStable(t *testing.T) {
	t.Parallel()
	if !strings.Contains(SystemPromptAddition, "<source>") {
		t.Errorf("system prompt addition missing <source> mention: %q", SystemPromptAddition)
	}
	if !strings.Contains(SystemPromptAddition, "untrusted") {
		t.Errorf("system prompt addition missing 'untrusted': %q", SystemPromptAddition)
	}
}
