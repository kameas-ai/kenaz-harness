package llm

import (
	"reflect"
	"sync"
	"testing"
)

func names(ts []ToolSpec) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}

// Three segments: hot and pinned are each sorted by name and form the
// stable prefix; activated keeps its given (last-used) order and never
// enters the prefix, even when it sorts first by name. A name in an
// earlier segment is not repeated.
func TestOrderTools_ThreeSegments(t *testing.T) {
	hot := []ToolSpec{{Name: "kenaz__read_file"}, {Name: "kenaz__bash"}}
	pinned := []ToolSpec{{Name: "git__status"}, {Name: "fs__write"}}
	activated := []ToolSpec{{Name: "outlook__send-mail"}, {Name: "aaa__first"}, {Name: "kenaz__bash"}}
	tools, stable := OrderTools(hot, pinned, activated)
	want := []string{"kenaz__bash", "kenaz__read_file", "fs__write", "git__status", "outlook__send-mail", "aaa__first"}
	if !reflect.DeepEqual(names(tools), want) || stable != 4 {
		t.Fatalf("OrderTools = %v stable=%d, want %v stable=4", names(tools), stable, want)
	}

	var req GenerationRequest
	req.SetTools(tools, stable)
	if idx := CacheMarkerToolIndex(req); idx != 3 || req.Tools[idx].Name != "git__status" {
		t.Errorf("marker index %d, want 3 (last pinned tool)", idx)
	}
	req.SetTools(OrderTools(nil, nil, activated))
	if CacheMarkerToolIndex(req) != -1 {
		t.Errorf("activated-only request must carry no tool marker (CacheStableTools=%d)", req.CacheStableTools)
	}
}

func TestOrderToolsFlat_SameSetSameOrder(t *testing.T) {
	a := []ToolSpec{{Name: "kenaz__grep"}, {Name: "outlook__send-mail"}, {Name: "kenaz__bash"}}
	b := []ToolSpec{{Name: "outlook__send-mail"}, {Name: "kenaz__bash"}, {Name: "kenaz__grep"}}
	got1, got2 := OrderToolsFlat(a), OrderToolsFlat(b)
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
		t.Errorf("OrderToolsFlat reordered its input in place: %v", a)
	}
	if OrderToolsFlat(nil) != nil {
		t.Errorf("OrderToolsFlat(nil) should stay nil")
	}
}

// One (profile, model) degrading leaves other models and other profiles
// marking.
func TestPromptCacheGuard_ScopedPerProfileAndModel(t *testing.T) {
	var g PromptCacheGuard
	g.Degrade("p-or", "anthropic/claude-x", CacheMarkSystemOnly)
	if g.Level("p-or", "anthropic/claude-x") != CacheMarkNone {
		t.Fatal("degraded key did not move")
	}
	if g.Level("p-or", "anthropic/claude-y") != CacheMarkAll || g.Level("p-direct", "anthropic/claude-x") != CacheMarkAll {
		t.Error("a rejection leaked to another model or profile")
	}
}

func TestPromptCacheLevel_String(t *testing.T) {
	if CacheMarkAll.String() != "system+tools" || CacheMarkSystemOnly.String() != "system" || CacheMarkNone.String() != "none" {
		t.Error("level names changed")
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
	if g.Level("p", "m") != CacheMarkAll {
		t.Fatalf("zero value = %v, want CacheMarkAll", g.Level("p", "m"))
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
			if lvl, changed := g.Degrade("p", "m", CacheMarkAll); changed {
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
	if changes != 1 || g.Level("p", "m") != CacheMarkSystemOnly {
		t.Fatalf("changes=%d level=%v, want 1 / system-only", changes, g.Level("p", "m"))
	}
	if lvl, changed := g.Degrade("p", "m", CacheMarkSystemOnly); !changed || lvl != CacheMarkNone {
		t.Fatalf("second degrade = %v/%v", lvl, changed)
	}
	if lvl, changed := g.Degrade("p", "m", CacheMarkAll); changed || lvl != CacheMarkNone {
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
