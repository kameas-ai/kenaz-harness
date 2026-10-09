package llm

import (
	"reflect"
	"sync"
	"testing"
)

func TestOrderTools_SameSetSameOrder(t *testing.T) {
	a := []ToolSpec{{Name: "kenaz__grep"}, {Name: "outlook__send-mail"}, {Name: "kenaz__bash"}}
	b := []ToolSpec{{Name: "outlook__send-mail"}, {Name: "kenaz__bash"}, {Name: "kenaz__grep"}}
	got1, got2 := OrderTools(a), OrderTools(b)
	if !reflect.DeepEqual(got1, got2) {
		t.Fatalf("same set, different order:\n%v\n%v", got1, got2)
	}
	want := []string{"kenaz__bash", "kenaz__grep", "outlook__send-mail"}
	for i, n := range want {
		if got1[i].Name != n {
			t.Fatalf("order = %v, want %v", got1, want)
		}
	}
	if a[0].Name != "kenaz__grep" {
		t.Errorf("OrderTools reordered its input in place: %v", a)
	}
	if OrderTools(nil) != nil {
		t.Errorf("OrderTools(nil) should stay nil")
	}
}

func TestCacheMarkerToolIndex(t *testing.T) {
	tools := []ToolSpec{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	cases := []struct {
		stable int
		tools  []ToolSpec
		want   int
	}{
		{0, tools, 2},   // default: every tool is stable
		{2, tools, 1},   // two stable tools: marker on the second
		{5, tools, 2},   // more than present: clamp to the last
		{-1, tools, -1}, // no stable segment
		{0, nil, -1},
	}
	for _, tc := range cases {
		if got := CacheMarkerToolIndex(GenerationRequest{Tools: tc.tools, CacheStableTools: tc.stable}); got != tc.want {
			t.Errorf("CacheStableTools=%d over %d tools: index %d, want %d", tc.stable, len(tc.tools), got, tc.want)
		}
	}
}

func TestSupportsPromptCache_CuratedTable(t *testing.T) {
	cases := []struct {
		kind, model string
		want        bool
	}{
		{"anthropic", "claude-sonnet-4-5", true},
		{"anthropic", "claude-3-5-haiku-20241022", true},
		{"openrouter", "anthropic/claude-sonnet-4.5", true},
		{"openrouter", "~anthropic/claude-sonnet-latest", true},
		{"openrouter", "Anthropic/Claude-Opus-4", true},
		{"openrouter", "openai/gpt-4o", false},
		{"openrouter", "z-ai/glm-5.2", false},
		{"openrouter", "meta/anthropic-lookalike", false},
		{"openai", "gpt-4o", false},
		{"bedrock", "anthropic.claude-3-haiku-20240307-v1:0", false},
		{"gemini", "gemini-2.5-pro", false},
	}
	for _, tc := range cases {
		if got := SupportsPromptCache(tc.kind, tc.model); got != tc.want {
			t.Errorf("SupportsPromptCache(%q, %q) = %v, want %v", tc.kind, tc.model, got, tc.want)
		}
	}
}

func TestFoldSystemSegments(t *testing.T) {
	r := GenerationRequest{System: "stable", SystemVolatile: "Current date: 2026-10-09."}
	f := FoldSystemSegments(r)
	if f.System != "stable\n\nCurrent date: 2026-10-09." || f.SystemVolatile != "" {
		t.Errorf("folded = %q / %q", f.System, f.SystemVolatile)
	}
	if r.SystemVolatile == "" {
		t.Errorf("FoldSystemSegments mutated its argument")
	}
	if got := FoldSystemSegments(GenerationRequest{SystemVolatile: "v"}); got.System != "v" {
		t.Errorf("volatile only: %q", got.System)
	}
	if got := FoldSystemSegments(GenerationRequest{System: "s"}); got.System != "s" {
		t.Errorf("stable only: %q", got.System)
	}
}

func TestPromptCacheGuard_DegradesOneWayOnce(t *testing.T) {
	var g PromptCacheGuard
	if g.Level() != CacheMarkAll {
		t.Fatalf("zero value = %v, want CacheMarkAll", g.Level())
	}
	// Many concurrent rejections of the same request: exactly one caller
	// moves the guard (and logs).
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		changes int
	)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if lvl, changed := g.Degrade(CacheMarkAll); changed {
				mu.Lock()
				changes++
				mu.Unlock()
				if lvl != CacheMarkSystemOnly {
					t.Errorf("first degrade landed on %v", lvl)
				}
			}
		}()
	}
	wg.Wait()
	if changes != 1 || g.Level() != CacheMarkSystemOnly {
		t.Fatalf("changes=%d level=%v, want 1 / system-only", changes, g.Level())
	}
	if lvl, changed := g.Degrade(CacheMarkSystemOnly); !changed || lvl != CacheMarkNone {
		t.Fatalf("second degrade = %v/%v", lvl, changed)
	}
	if lvl, changed := g.Degrade(CacheMarkAll); changed || lvl != CacheMarkNone {
		t.Fatalf("a stale degrade must not move the guard back: %v/%v", lvl, changed)
	}
}

func TestSentCacheLevel_NoToolsMeansSystemOnly(t *testing.T) {
	if got := SentCacheLevel(GenerationRequest{}, CacheMarkAll); got != CacheMarkSystemOnly {
		t.Errorf("no tools at CacheMarkAll sent %v", got)
	}
	if got := SentCacheLevel(GenerationRequest{Tools: []ToolSpec{{Name: "a"}}}, CacheMarkAll); got != CacheMarkAll {
		t.Errorf("with a tool sent %v", got)
	}
}

func TestIsCacheControlRejection(t *testing.T) {
	if !IsCacheControlRejection(400, []byte(`{"error":{"message":"tools.0.cache_control: Extra inputs are not permitted"}}`)) {
		t.Error("400 naming cache_control not recognised")
	}
	if IsCacheControlRejection(400, []byte(`{"error":"max_tokens too large"}`)) {
		t.Error("unrelated 400 recognised")
	}
	if IsCacheControlRejection(500, []byte(`cache_control`)) {
		t.Error("5xx recognised")
	}
}
