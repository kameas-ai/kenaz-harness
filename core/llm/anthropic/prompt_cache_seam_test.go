package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/wirecheck"
)

// Hot and pinned form the cached prefix; an activated tool that sorts
// first by name still comes after the marker.
func TestAnthropicAdapter_PromptCache_ThreeSegmentGolden(t *testing.T) {
	schema := json.RawMessage(`{"type":"object"}`)
	tool := func(n string) llm.ToolSpec { return llm.ToolSpec{Name: n, Description: n, InputSchema: schema} }
	req := minReq()
	req.System = "s"
	req.SetTools(llm.OrderTools(
		[]llm.ToolSpec{tool("kenaz__read_file"), tool("kenaz__bash")},
		[]llm.ToolSpec{tool("git__status")},
		[]llm.ToolSpec{tool("outlook__send-mail"), tool("aaa__first")},
	))
	body, err := buildRequestBodyAt(req, stdProf("claude-sonnet-4-5"), llm.CacheMarkAll)
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Tools json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}
	want := `[{"description":"kenaz__bash","input_schema":{"type":"object"},"name":"kenaz__bash"},` +
		`{"description":"kenaz__read_file","input_schema":{"type":"object"},"name":"kenaz__read_file"},` +
		`{"cache_control":{"type":"ephemeral"},"description":"git__status","input_schema":{"type":"object"},"name":"git__status"},` +
		`{"description":"outlook__send-mail","input_schema":{"type":"object"},"name":"outlook__send-mail"},` +
		`{"description":"aaa__first","input_schema":{"type":"object"},"name":"aaa__first"}]`
	if string(p.Tools) != want {
		t.Errorf("tools =\n%s\nwant\n%s", p.Tools, want)
	}
}

// Production shape: the body Stream sends for a Claude model at the
// adapter's own marking level.
func TestAnthropicAdapter_Tools_ToolsSerializedProduction(t *testing.T) {
	a := New()
	req := cacheReq("", cacheTools())
	req.CacheStableTools = 0
	body, err := buildRequestBodyAt(req, stdProf("claude-sonnet-4-5"), a.cacheLevel("p-ant", "claude-sonnet-4-5"))
	if err != nil {
		t.Fatal(err)
	}
	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
		{Pointer: "/tools", WantPresent: true, WantArrayLen: 3, WantArrayLenSet: true},
		{Pointer: "/tools/2/cache_control/type", WantString: "ephemeral"},
		{Pointer: "/system/0/cache_control/type", WantString: "ephemeral"},
		{Pointer: "/system/0/text", WantString: "You are the chat node."},
	})
}

// The JSON-output instruction is per-request shaping: it follows the
// per-call segment (which ends with the user's instructions), outside the
// cached prefix, and is the last system text whether or not anything is
// marked.
func TestAnthropicAdapter_JSONMode_InstructionLastAfterVolatile(t *testing.T) {
	req := cacheReq("## User instructions\n\nPrefer tables.", nil)
	req.JSONMode = &llm.JSONModeSpec{Enabled: true}
	const instr = "Respond with valid JSON only. Do not include any text before or after the JSON."

	marked, err := buildRequestBodyAt(req, stdProf("claude-sonnet-4-5"), llm.CacheMarkAll)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		System []map[string]any `json:"system"`
	}
	if err := json.Unmarshal(marked, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.System) != 2 || m.System[0]["text"] != "You are the chat node." ||
		m.System[1]["text"] != "## User instructions\n\nPrefer tables.\n\n"+instr {
		t.Errorf("marked system = %v", m.System)
	}

	plain := serialise(t, req, stdProf("claude-sonnet-4-5"))
	var u struct {
		System string `json:"system"`
	}
	if err := json.Unmarshal(plain, &u); err != nil {
		t.Fatal(err)
	}
	if u.System != "You are the chat node.\n\n## User instructions\n\nPrefer tables.\n\n"+instr {
		t.Errorf("unmarked system = %q", u.System)
	}
}

// Two concurrent cache_control rejections of the same model: one degrade
// step, one log line.
func TestAnthropicAdapter_PromptCache_ConcurrentRejectionsLogOnce(t *testing.T) {
	logs := captureLog(t)
	rs := &recordingServer{reject: func(body string) bool {
		i := strings.Index(body, `"tools":`)
		return i >= 0 && strings.Contains(body[i:], "cache_control")
	}}
	ts := httptest.NewServer(http.HandlerFunc(rs.handler))
	defer ts.Close()
	a := New(WithEndpoint(ts.URL), WithHTTPClient(ts.Client()))

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := a.Stream(context.Background(), cacheReq("v", cacheTools()), stdProf("claude-sonnet-4-5"), []byte("k"))
			if err != nil {
				t.Errorf("Stream: %v", err)
				return
			}
			for range s.Events() {
			}
			_, _ = s.Final()
		}()
	}
	wg.Wait()
	if n := strings.Count(logs.String(), `"msg":"llm.prompt_cache.unsupported"`); n != 1 {
		t.Errorf("llm.prompt_cache.unsupported logged %d times, want 1:\n%s", n, logs.String())
	}
	if got := a.cacheGuard.Level("p-ant", "claude-sonnet-4-5"); got != llm.CacheMarkSystemOnly {
		t.Errorf("guard = %v", got)
	}
}
