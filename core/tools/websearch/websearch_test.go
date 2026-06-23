package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type stubAggregator struct {
	hits []SearchHit
	err  error
}

func (s *stubAggregator) Search(ctx context.Context, query string, opts SearchOpts) ([]SearchHit, error) {
	if s.err != nil {
		return nil, s.err
	}
	return append([]SearchHit(nil), s.hits...), nil
}

type stubFetcher struct {
	bodies map[string][]byte
	errs   map[string]error
	delay  time.Duration
}

func (s *stubFetcher) Fetch(ctx context.Context, url string) ([]byte, string, error) {
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}
	if e, ok := s.errs[url]; ok {
		return nil, "", e
	}
	if b, ok := s.bodies[url]; ok {
		return b, "text/html", nil
	}
	return []byte("<html><body><p>fallback body</p></body></html>"), "text/html", nil
}

type stubExtractor struct{}

func (s *stubExtractor) Extract(ctx context.Context, body []byte, baseURL string) (string, error) {
	return "extracted: " + string(body), nil
}

func TestTool_NameDescriptionSchema(t *testing.T) {
	t.Parallel()
	tool := New(Options{
		Aggregator: &stubAggregator{},
		Fetcher:    &stubFetcher{},
		Extractor:  &stubExtractor{},
	})
	if tool.Name() != "kenaz__web_search" {
		t.Errorf("Name = %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Errorf("empty description")
	}
	schema := tool.InputSchema()
	var parsed map[string]any
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("InputSchema not valid JSON: %v", err)
	}
	if parsed["type"] != "object" {
		t.Errorf("schema type = %v", parsed["type"])
	}
	required, ok := parsed["required"].([]any)
	if !ok || len(required) != 1 || required[0] != "query" {
		t.Errorf("schema required = %v", parsed["required"])
	}
}

func TestTool_EndToEnd(t *testing.T) {
	t.Parallel()
	hits := []SearchHit{
		{Title: "Wikipedia", URL: "https://en.wikipedia.org/wiki/Foo", Snippet: "an article"},
		{Title: "GitHub", URL: "https://github.com/foo/bar", Snippet: "a repo"},
	}
	tool := New(Options{
		Aggregator: &stubAggregator{hits: hits},
		Fetcher:    &stubFetcher{},
		Extractor:  &stubExtractor{},
	})

	args := json.RawMessage(`{"query":"foo"}`)
	out, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var payload callPayload
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(payload.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(payload.Results))
	}
	if payload.SystemPromptAddition != SystemPromptAddition {
		t.Errorf("system prompt addition not surfaced: %q", payload.SystemPromptAddition)
	}
	for _, r := range payload.Results {
		if !strings.HasPrefix(r.ExtractedText, "<source ") {
			t.Errorf("extracted_text not <source>-wrapped: %q", r.ExtractedText)
		}
		if !strings.HasSuffix(r.ExtractedText, "</source>") {
			t.Errorf("extracted_text missing closing tag: %q", r.ExtractedText)
		}
	}
}

func TestTool_PerResultFetchFailureSkipped(t *testing.T) {
	t.Parallel()
	failURL := "https://bad.example/x"
	okURL := "https://good.example/y"
	tool := New(Options{
		Aggregator: &stubAggregator{hits: []SearchHit{
			{Title: "bad", URL: failURL},
			{Title: "good", URL: okURL},
		}},
		Fetcher: &stubFetcher{errs: map[string]error{failURL: errors.New("boom")}},
		Extractor: &stubExtractor{},
	})
	out, err := tool.Call(context.Background(), json.RawMessage(`{"query":"q"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var payload callPayload
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Results) != 1 {
		t.Fatalf("expected 1 result (failing fetch skipped), got %d", len(payload.Results))
	}
	if payload.Results[0].URL != okURL {
		t.Errorf("survivor = %q, want %q", payload.Results[0].URL, okURL)
	}
}

func TestTool_RespectsCallTimeout(t *testing.T) {
	t.Parallel()
	tool := New(Options{
		Aggregator: &stubAggregator{hits: []SearchHit{
			{Title: "slow", URL: "https://slow.example/"},
		}},
		Fetcher:     &stubFetcher{delay: 200 * time.Millisecond},
		Extractor:   &stubExtractor{},
		CallTimeout: 25 * time.Millisecond,
	})
	out, err := tool.Call(context.Background(), json.RawMessage(`{"query":"q"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	// Slow fetch should have been cancelled by the call timeout, so
	// the result is skipped and the JSON has zero results.
	var payload callPayload
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Results) != 0 {
		t.Errorf("expected 0 results after timeout, got %d", len(payload.Results))
	}
}

func TestTool_ParsesArgs(t *testing.T) {
	t.Parallel()
	var seen atomic.Pointer[SearchOpts]
	agg := aggregatorFunc(func(ctx context.Context, query string, opts SearchOpts) ([]SearchHit, error) {
		copy := opts
		seen.Store(&copy)
		return nil, nil
	})

	tool := New(Options{
		Aggregator: agg,
		Fetcher:    &stubFetcher{},
		Extractor:  &stubExtractor{},
	})
	args := json.RawMessage(`{"query":"q","max_results":3,"allowed_domains":["wiki.org"],"blocked_domains":["bad.com"]}`)
	if _, err := tool.Call(context.Background(), args); err != nil {
		t.Fatalf("Call: %v", err)
	}
	got := seen.Load()
	if got == nil {
		t.Fatal("aggregator never called")
	}
	if len(got.AllowedDomains) != 1 || got.AllowedDomains[0] != "wiki.org" {
		t.Errorf("allowed_domains = %v", got.AllowedDomains)
	}
	if len(got.BlockedDomains) != 1 || got.BlockedDomains[0] != "bad.com" {
		t.Errorf("blocked_domains = %v", got.BlockedDomains)
	}
}

func TestTool_RejectsEmptyQuery(t *testing.T) {
	t.Parallel()
	tool := New(Options{
		Aggregator: &stubAggregator{},
		Fetcher:    &stubFetcher{},
		Extractor:  &stubExtractor{},
	})
	_, err := tool.Call(context.Background(), json.RawMessage(`{"query":""}`))
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestTool_RejectsBadJSON(t *testing.T) {
	t.Parallel()
	tool := New(Options{
		Aggregator: &stubAggregator{},
		Fetcher:    &stubFetcher{},
		Extractor:  &stubExtractor{},
	})
	_, err := tool.Call(context.Background(), json.RawMessage(`not json`))
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestTool_CapsMaxResults(t *testing.T) {
	t.Parallel()
	hits := make([]SearchHit, 20)
	for i := range hits {
		hits[i] = SearchHit{Title: "t", URL: "https://example.com/" + string(rune('a'+i))}
	}
	tool := New(Options{
		Aggregator: &stubAggregator{hits: hits},
		Fetcher:    &stubFetcher{},
		Extractor:  &stubExtractor{},
	})
	out, err := tool.Call(context.Background(), json.RawMessage(`{"query":"q","max_results":50}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var payload callPayload
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Results) > maxMaxResults {
		t.Errorf("expected at most %d results, got %d", maxMaxResults, len(payload.Results))
	}
}

// aggregatorFunc adapts a func into the Aggregating interface for tests
// that need to inspect call args.
type aggregatorFunc func(ctx context.Context, query string, opts SearchOpts) ([]SearchHit, error)

func (f aggregatorFunc) Search(ctx context.Context, query string, opts SearchOpts) ([]SearchHit, error) {
	return f(ctx, query, opts)
}
