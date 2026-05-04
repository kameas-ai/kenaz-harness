package web_test

import (
	"reflect"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/workflows/web"
)

const sampleHTML = `<!DOCTYPE html>
<html>
<head><title>Test Page</title></head>
<body>
  <h1>Hello World</h1>
  <p class="desc">A description</p>
  <a href="https://example.com">Example</a>
  <a href="https://go.dev">Go</a>
  <ul>
    <li>one</li>
    <li>two</li>
    <li>three</li>
  </ul>
</body>
</html>`

func TestScrapeCSS_SingleExtractor(t *testing.T) {
	extractors := []web.Extractor{
		{Name: "title", Selector: "h1"},
	}
	out, err := web.ScrapeCSS(sampleHTML, extractors)
	if err != nil {
		t.Fatalf("ScrapeCSS: %v", err)
	}
	if out["title"] != "Hello World" {
		t.Errorf("title: got %q want %q", out["title"], "Hello World")
	}
}

func TestScrapeCSS_AttributeExtraction(t *testing.T) {
	extractors := []web.Extractor{
		{Name: "first_link", Selector: "a", Attr: "href"},
	}
	out, err := web.ScrapeCSS(sampleHTML, extractors)
	if err != nil {
		t.Fatalf("ScrapeCSS: %v", err)
	}
	if out["first_link"] != "https://example.com" {
		t.Errorf("first_link: got %q want %q", out["first_link"], "https://example.com")
	}
}

func TestScrapeCSS_MultipleResults(t *testing.T) {
	extractors := []web.Extractor{
		{Name: "links", Selector: "a", Attr: "href", Multiple: true},
	}
	out, err := web.ScrapeCSS(sampleHTML, extractors)
	if err != nil {
		t.Fatalf("ScrapeCSS: %v", err)
	}
	links, ok := out["links"].([]string)
	if !ok {
		t.Fatalf("links: expected []string, got %T", out["links"])
	}
	want := []string{"https://example.com", "https://go.dev"}
	if !reflect.DeepEqual(links, want) {
		t.Errorf("links: got %v want %v", links, want)
	}
}

func TestScrapeCSS_MultipleTextNodes(t *testing.T) {
	extractors := []web.Extractor{
		{Name: "items", Selector: "li", Multiple: true},
	}
	out, err := web.ScrapeCSS(sampleHTML, extractors)
	if err != nil {
		t.Fatalf("ScrapeCSS: %v", err)
	}
	items, ok := out["items"].([]string)
	if !ok {
		t.Fatalf("items: expected []string, got %T", out["items"])
	}
	if len(items) != 3 {
		t.Errorf("items count: got %d want 3", len(items))
	}
	if items[0] != "one" {
		t.Errorf("items[0]: got %q want %q", items[0], "one")
	}
}

func TestScrapeCSS_UnmatchedSelectorReturnsEmpty(t *testing.T) {
	extractors := []web.Extractor{
		{Name: "missing", Selector: ".does-not-exist"},
	}
	out, err := web.ScrapeCSS(sampleHTML, extractors)
	if err != nil {
		t.Fatalf("ScrapeCSS: %v", err)
	}
	if v, ok := out["missing"]; !ok {
		t.Error("expected key 'missing' in output")
	} else if v != "" {
		t.Errorf("missing: got %q want empty string", v)
	}
}

func TestScrapeCSS_MultipleExtractors(t *testing.T) {
	extractors := []web.Extractor{
		{Name: "title", Selector: "h1"},
		{Name: "desc", Selector: "p.desc"},
		{Name: "links", Selector: "a", Attr: "href", Multiple: true},
	}
	out, err := web.ScrapeCSS(sampleHTML, extractors)
	if err != nil {
		t.Fatalf("ScrapeCSS: %v", err)
	}
	if out["title"] != "Hello World" {
		t.Errorf("title: got %q want %q", out["title"], "Hello World")
	}
	if out["desc"] != "A description" {
		t.Errorf("desc: got %q want %q", out["desc"], "A description")
	}
	if links, ok := out["links"].([]string); !ok || len(links) != 2 {
		t.Errorf("links: unexpected %v", out["links"])
	}
}

func TestScrapeCSS_EmptyExtractorsList(t *testing.T) {
	out, err := web.ScrapeCSS(sampleHTML, nil)
	if err != nil {
		t.Fatalf("ScrapeCSS with nil extractors: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected empty map, got %v", out)
	}
}

func TestScrapeCSS_InvalidHTML_GracefulParse(t *testing.T) {
	// Go's html.Parse is lenient about invalid HTML; ScrapeCSS should
	// still return results rather than error on malformed input.
	broken := "<div><p>unclosed"
	extractors := []web.Extractor{
		{Name: "para", Selector: "p"},
	}
	out, err := web.ScrapeCSS(broken, extractors)
	if err != nil {
		t.Fatalf("ScrapeCSS on broken HTML: %v", err)
	}
	// goquery can parse unclosed tags; the paragraph text should be present.
	_ = out // just verifying no error
}
