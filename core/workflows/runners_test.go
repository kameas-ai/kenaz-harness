package workflows

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// =============================================================================
// model_turn (WP03)
// =============================================================================

// fakeLLM is a minimal LLMStreamer that emits a sequence of text
// chunks then returns a final.
type fakeLLM struct {
	chunks []string
	err    error
}

func (f *fakeLLM) Stream(_ context.Context, req LLMRequest) (LLMStream, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &fakeLLMStream{chunks: f.chunks, prompt: req.Prompt}, nil
}

type fakeLLMStream struct {
	chunks []string
	prompt string
	once   sync.Once
	ch     chan LLMStreamEvent
}

func (s *fakeLLMStream) Events() <-chan LLMStreamEvent {
	s.once.Do(func() {
		s.ch = make(chan LLMStreamEvent, len(s.chunks)+1)
		for _, c := range s.chunks {
			s.ch <- LLMStreamEvent{Text: c}
		}
		close(s.ch)
	})
	return s.ch
}

func (s *fakeLLMStream) Final() (string, error) {
	return strings.Join(s.chunks, ""), nil
}

func TestModelTurn_DispatchesAndAccumulates(t *testing.T) {
	llm := &fakeLLM{chunks: []string{"hello, ", "world"}}
	wf := Workflow{
		ID: "x", Name: "x", Version: 1,
		Steps: []Step{{Name: "say", Kind: StepKindModelTurn, UserPrompt: "greet"}},
	}
	e := NewEngineWithDeps(Deps{LLM: llm, DefaultLLMProfile: "p1"})
	run, err := e.Run(context.Background(), wf, nil, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("status: %s err=%s", run.Status, run.Err)
	}
	if got := run.Steps[0].Output; got != "hello, world" {
		t.Errorf("output: got %q want %q", got, "hello, world")
	}
}

func TestModelTurn_ErrorsWithoutProfile(t *testing.T) {
	llm := &fakeLLM{chunks: []string{"x"}}
	wf := Workflow{
		ID: "x", Name: "x", Version: 1,
		Steps: []Step{{Name: "say", Kind: StepKindModelTurn, UserPrompt: "hi"}},
	}
	e := NewEngineWithDeps(Deps{LLM: llm}) // no profile
	run, err := e.Run(context.Background(), wf, nil, RunOptions{})
	if err == nil {
		t.Fatalf("expected error for missing profile, got status=%s", run.Status)
	}
	if !strings.Contains(err.Error(), "no profile") {
		t.Errorf("err: got %v want contains 'no profile'", err)
	}
}

func TestModelTurn_FallbackStubWhenNoLLM(t *testing.T) {
	// NewEngine() registers modelTurnRunner with nil LLM — should
	// stub-echo the prompt so the chassis stays bootable.
	wf := Workflow{
		ID: "x", Name: "x", Version: 1,
		Steps: []Step{{Name: "say", Kind: StepKindModelTurn, UserPrompt: "hi"}},
	}
	run, err := NewEngine().Run(context.Background(), wf, nil, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(run.Steps[0].Output, "[model_turn stub]") {
		t.Errorf("expected stub fallback, got %q", run.Steps[0].Output)
	}
}

// =============================================================================
// tool_call (WP03)
// =============================================================================

type fakeTools struct {
	called map[string]map[string]any
	out    string
	err    error
	isErr  bool
}

func (f *fakeTools) Call(_ context.Context, name string, args map[string]any) (ToolResult, error) {
	if f.called == nil {
		f.called = map[string]map[string]any{}
	}
	f.called[name] = args
	if f.err != nil {
		return ToolResult{}, f.err
	}
	return ToolResult{Content: f.out, IsError: f.isErr}, nil
}

func TestToolCall_DispatchesAndExpandsArgs(t *testing.T) {
	tools := &fakeTools{out: "tool-result"}
	wf := Workflow{
		ID: "x", Name: "x", Version: 1,
		Inputs: []Input{{Name: "who", Kind: InputKindString}},
		Steps: []Step{
			{
				Name: "go", Kind: StepKindToolCall, ToolName: "echo",
				ToolArgs: map[string]any{"msg": "hi-${input.who}", "n": 5},
			},
		},
	}
	e := NewEngineWithDeps(Deps{Tools: tools})
	run, err := e.Run(context.Background(), wf, map[string]TypedValue{
		"who": {Type: ValueTypeText, Text: "ada"},
	}, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("status: %s err=%s", run.Status, run.Err)
	}
	args := tools.called["echo"]
	if args["msg"] != "hi-ada" {
		t.Errorf("args.msg: got %v want hi-ada", args["msg"])
	}
	if args["n"] != 5 {
		t.Errorf("args.n: got %v want 5", args["n"])
	}
	if run.Steps[0].Output != "tool-result" {
		t.Errorf("output: got %q", run.Steps[0].Output)
	}
}

func TestToolCall_ErrorsWithoutDep(t *testing.T) {
	wf := Workflow{
		ID: "x", Name: "x", Version: 1,
		Steps: []Step{{Name: "go", Kind: StepKindToolCall, ToolName: "echo"}},
	}
	_, err := NewEngine().Run(context.Background(), wf, nil, RunOptions{})
	if err == nil {
		t.Fatalf("expected dep-unavailable error")
	}
	if !errors.Is(err, errDepUnavailable) {
		t.Errorf("err: want errDepUnavailable, got %v", err)
	}
}

// =============================================================================
// http_request (WP04)
// =============================================================================

func TestHTTPRequest_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test") != "hello" {
			t.Errorf("missing header X-Test")
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "pong")
	}))
	defer srv.Close()

	wf := Workflow{
		ID: "x", Name: "x", Version: 1,
		Steps: []Step{
			{
				Name: "ping", Kind: StepKindHTTPRequest,
				Method: "GET", URL: srv.URL,
				Headers: map[string]string{"X-Test": "hello"},
			},
		},
	}
	run, err := NewEngine().Run(context.Background(), wf, nil, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("status: %s err=%s", run.Status, run.Err)
	}
	if !strings.Contains(run.Steps[0].Output, `"status":200`) {
		t.Errorf("output missing status:200: %q", run.Steps[0].Output)
	}
	if !strings.Contains(run.Steps[0].Output, "pong") {
		t.Errorf("output missing body: %q", run.Steps[0].Output)
	}
}

func TestHTTPRequest_RejectsOversizedResponse(t *testing.T) {
	big := strings.Repeat("x", httpResponseCap+10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, big)
	}))
	defer srv.Close()

	wf := Workflow{
		ID: "x", Name: "x", Version: 1,
		Steps: []Step{{Name: "huge", Kind: StepKindHTTPRequest, Method: "GET", URL: srv.URL}},
	}
	_, err := NewEngine().Run(context.Background(), wf, nil, RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "1MB cap") {
		t.Fatalf("expected oversize error, got %v", err)
	}
}

// =============================================================================
// mcp_call (WP04)
// =============================================================================

type fakeMCP struct {
	server, tool string
	args         map[string]any
	out          string
	err          error
}

func (f *fakeMCP) Call(_ context.Context, server, tool string, args map[string]any) (string, error) {
	f.server, f.tool, f.args = server, tool, args
	return f.out, f.err
}

func TestMCPCall_DispatchesViaPool(t *testing.T) {
	mcp := &fakeMCP{out: "mcp-result"}
	wf := Workflow{
		ID: "x", Name: "x", Version: 1,
		Steps: []Step{
			{
				Name: "go", Kind: StepKindMCPCall,
				Server: "fs", ToolName: "read_file",
				ToolArgs: map[string]any{"path": "/tmp/x"},
			},
		},
	}
	e := NewEngineWithDeps(Deps{MCP: mcp})
	run, err := e.Run(context.Background(), wf, nil, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("status: %s err=%s", run.Status, run.Err)
	}
	if mcp.server != "fs" || mcp.tool != "read_file" {
		t.Errorf("call: got %s.%s want fs.read_file", mcp.server, mcp.tool)
	}
	if run.Steps[0].Output != "mcp-result" {
		t.Errorf("output: got %q", run.Steps[0].Output)
	}
}

func TestMCPCall_PropagatesError(t *testing.T) {
	mcp := &fakeMCP{err: errors.New("server unavailable")}
	wf := Workflow{
		ID: "x", Name: "x", Version: 1,
		Steps: []Step{{Name: "go", Kind: StepKindMCPCall, Server: "fs", ToolName: "x"}},
	}
	e := NewEngineWithDeps(Deps{MCP: mcp})
	_, err := e.Run(context.Background(), wf, nil, RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "server unavailable") {
		t.Fatalf("expected propagated error, got %v", err)
	}
}

// =============================================================================
// read_artifact + write_artifact (WP05)
// =============================================================================

type fakeArtifacts struct {
	rows map[string]ArtifactView
	mu   sync.Mutex
	next int
	err  error
}

func newFakeArtifacts() *fakeArtifacts {
	return &fakeArtifacts{rows: map[string]ArtifactView{}}
}

func (f *fakeArtifacts) Read(_ context.Context, id string) (ArtifactView, error) {
	if f.err != nil {
		return ArtifactView{}, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.rows[id]
	if !ok {
		return ArtifactView{}, fmt.Errorf("not found: %s", id)
	}
	return v, nil
}

func (f *fakeArtifacts) Write(_ context.Context, in ArtifactWrite) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	id := fmt.Sprintf("art-%d", f.next)
	f.rows[id] = ArtifactView{ID: id, Title: in.Title, MimeType: in.MimeType, Content: in.Content}
	return id, nil
}

func TestReadArtifact_ReturnsBytesAndMime(t *testing.T) {
	art := newFakeArtifacts()
	art.rows["abc"] = ArtifactView{ID: "abc", Title: "notes.md", MimeType: "text/markdown", Content: []byte("# hi")}
	wf := Workflow{
		ID: "x", Name: "x", Version: 1,
		Steps: []Step{{Name: "load", Kind: StepKindReadArtifact, ArtifactIDRef: "abc"}},
	}
	e := NewEngineWithDeps(Deps{Artifacts: art})
	run, err := e.Run(context.Background(), wf, nil, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := run.Steps[0].Output
	if !strings.Contains(out, `"name":"notes.md"`) || !strings.Contains(out, `"mime":"text/markdown"`) {
		t.Errorf("output missing fields: %q", out)
	}
	if !strings.Contains(out, "# hi") {
		t.Errorf("output missing content: %q", out)
	}
}

func TestReadArtifact_NotFound(t *testing.T) {
	art := newFakeArtifacts()
	wf := Workflow{
		ID: "x", Name: "x", Version: 1,
		Steps: []Step{{Name: "load", Kind: StepKindReadArtifact, ArtifactIDRef: "missing"}},
	}
	e := NewEngineWithDeps(Deps{Artifacts: art})
	_, err := e.Run(context.Background(), wf, nil, RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found, got %v", err)
	}
}

func TestWriteArtifact_MintsID(t *testing.T) {
	art := newFakeArtifacts()
	wf := Workflow{
		ID: "x", Name: "x", Version: 1,
		Steps: []Step{
			{
				Name: "save", Kind: StepKindWriteArtifact,
				Title: "out.txt", MimeType: "text/plain",
				Content: "saved-body",
			},
		},
	}
	e := NewEngineWithDeps(Deps{Artifacts: art, SessionID: "sess-1"})
	run, err := e.Run(context.Background(), wf, nil, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := run.Steps[0].Output
	if !strings.Contains(out, `"id":"art-1"`) {
		t.Errorf("output missing id: %q", out)
	}
	if got := art.rows["art-1"].Content; string(got) != "saved-body" {
		t.Errorf("written content: got %q want saved-body", got)
	}
}

func TestWriteArtifact_ErrorsWithoutSession(t *testing.T) {
	art := newFakeArtifacts()
	wf := Workflow{
		ID: "x", Name: "x", Version: 1,
		Steps: []Step{
			{
				Name: "save", Kind: StepKindWriteArtifact,
				Title: "x", Content: "y",
			},
		},
	}
	e := NewEngineWithDeps(Deps{Artifacts: art}) // no SessionID
	_, err := e.Run(context.Background(), wf, nil, RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "SessionID") {
		t.Fatalf("expected SessionID error, got %v", err)
	}
}

// =============================================================================
// transform (WP05)
// =============================================================================

func TestTransform_RendersRefs(t *testing.T) {
	wf := Workflow{
		ID: "x", Name: "x", Version: 1,
		Inputs: []Input{{Name: "who", Kind: InputKindString}},
		Steps: []Step{
			{Name: "shout", Kind: StepKindTransform, Template: "HELLO ${input.who}!"},
		},
	}
	run, err := NewEngine().Run(context.Background(), wf, map[string]TypedValue{
		"who": {Type: ValueTypeText, Text: "ada"},
	}, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Steps[0].Output != "HELLO ada!" {
		t.Errorf("output: got %q want %q", run.Steps[0].Output, "HELLO ada!")
	}
}

func TestTransform_RejectsUnknownRef(t *testing.T) {
	// The loader normally catches this; bypass via direct step
	// run to confirm the runner itself errors cleanly.
	rc := &RunContext{Inputs: map[string]TypedValue{}, StepOutputs: map[string]TypedValue{}}
	_, err := transformRunner{}.Run(context.Background(),
		Step{Name: "x", Kind: StepKindTransform, Template: "hi ${input.missing}"}, rc)
	if err == nil {
		t.Fatalf("expected error for unknown ref")
	}
}

// =============================================================================
// conditional (WP05)
// =============================================================================

func TestConditional_TakesThenBranch(t *testing.T) {
	wf := Workflow{
		ID: "x", Name: "x", Version: 1,
		Inputs: []Input{{Name: "mode", Kind: InputKindString}},
		Steps: []Step{
			{
				Name: "pick", Kind: StepKindConditional,
				If: "${input.mode} == on", ThenStep: "yes", ElseStep: "no",
			},
			{Name: "yes", Kind: StepKindShell, Cmd: "echo", Args: []string{"yes-branch"}},
			{Name: "no", Kind: StepKindShell, Cmd: "echo", Args: []string{"no-branch"}},
		},
	}
	run, err := NewEngine().Run(context.Background(), wf, map[string]TypedValue{
		"mode": {Type: ValueTypeText, Text: "on"},
	}, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Steps[0].Output != "yes" {
		t.Errorf("conditional output: got %q want yes", run.Steps[0].Output)
	}
	if run.Steps[1].Status != "completed" || run.Steps[1].Output != "yes-branch" {
		t.Errorf("then step: got status=%s output=%q", run.Steps[1].Status, run.Steps[1].Output)
	}
	if run.Steps[2].Status != "skipped" {
		t.Errorf("else step: got status=%s want skipped", run.Steps[2].Status)
	}
}

func TestConditional_TakesElseBranch(t *testing.T) {
	wf := Workflow{
		ID: "x", Name: "x", Version: 1,
		Inputs: []Input{{Name: "mode", Kind: InputKindString}},
		Steps: []Step{
			{
				Name: "pick", Kind: StepKindConditional,
				If: "${input.mode} == on", ThenStep: "yes", ElseStep: "no",
			},
			{Name: "yes", Kind: StepKindShell, Cmd: "echo", Args: []string{"yes-branch"}},
			{Name: "no", Kind: StepKindShell, Cmd: "echo", Args: []string{"no-branch"}},
		},
	}
	run, err := NewEngine().Run(context.Background(), wf, map[string]TypedValue{
		"mode": {Type: ValueTypeText, Text: "off"},
	}, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Steps[0].Output != "no" {
		t.Errorf("conditional output: got %q want no", run.Steps[0].Output)
	}
	if run.Steps[1].Status != "skipped" {
		t.Errorf("then step: got %s want skipped", run.Steps[1].Status)
	}
	if run.Steps[2].Status != "completed" {
		t.Errorf("else step: got %s want completed", run.Steps[2].Status)
	}
}

func TestConditional_RejectsMissingIf(t *testing.T) {
	wf := Workflow{
		ID: "x", Name: "x", Version: 1,
		Steps: []Step{{Name: "pick", Kind: StepKindConditional, ThenStep: "y"}},
	}
	_, err := NewEngine().Run(context.Background(), wf, nil, RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "if") {
		t.Fatalf("expected validation error for missing if, got %v", err)
	}
}

// =============================================================================
// web_fetch (WP05)
// =============================================================================

func TestWebFetch_FetchesHTMLPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><h1>Hello</h1></html>")
	}))
	defer srv.Close()

	wf := Workflow{
		ID: "wf", Name: "wf", Version: 1,
		Steps: []Step{{Name: "fetch", Kind: StepKindWebFetch, URL: srv.URL + "/page"}},
	}
	run, err := NewEngine().Run(context.Background(), wf, nil, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("status: %s err=%s", run.Status, run.Err)
	}
	if !strings.Contains(run.Steps[0].Output, "html") {
		t.Errorf("output should contain kind=html: %s", run.Steps[0].Output)
	}
}

func TestWebFetch_BlockedByRobots(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "User-agent: *\nDisallow: /\n")
		default:
			fmt.Fprint(w, "nope")
		}
	}))
	defer srv.Close()

	wf := Workflow{
		ID: "wf", Name: "wf", Version: 1,
		Steps: []Step{{Name: "fetch", Kind: StepKindWebFetch, URL: srv.URL + "/secret"}},
	}
	run, err := NewEngine().Run(context.Background(), wf, nil, RunOptions{})
	if err == nil || run.Status != "failed" {
		t.Fatalf("expected failure for robots.txt block: status=%s", run.Status)
	}
	if !strings.Contains(err.Error(), "robots") {
		t.Errorf("error should mention robots: %v", err)
	}
}

func TestWebFetch_CedarGateDenies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html>ok</html>")
	}))
	defer srv.Close()

	wf := Workflow{
		ID: "wf", Name: "wf", Version: 1,
		Steps: []Step{{Name: "fetch", Kind: StepKindWebFetch, URL: srv.URL + "/page"}},
	}
	e := NewEngineWithDeps(Deps{NetAuthz: &denyAllAuthz{}})
	run, err := e.Run(context.Background(), wf, nil, RunOptions{})
	if err == nil || run.Status != "failed" {
		t.Fatalf("expected policy denial: status=%s err=%v", run.Status, err)
	}
	if !strings.Contains(err.Error(), "policy denied") {
		t.Errorf("error should mention policy denied: %v", err)
	}
}

func TestWebFetch_RejectsEmptyURL(t *testing.T) {
	wf := Workflow{
		ID: "wf", Name: "wf", Version: 1,
		Steps: []Step{{Name: "fetch", Kind: StepKindWebFetch, URL: ""}},
	}
	_, err := NewEngine().Run(context.Background(), wf, nil, RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "url") {
		t.Fatalf("expected url-required error, got %v", err)
	}
}

// =============================================================================
// web_scrape (WP05)
// =============================================================================

func TestWebScrape_CSSExtraction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><h1>Hello World</h1><a href="https://x.com">Link</a></body></html>`)
	}))
	defer srv.Close()

	wf := Workflow{
		ID: "wf", Name: "wf", Version: 1,
		Steps: []Step{{
			Name: "scrape", Kind: StepKindWebScrape,
			URL:  srv.URL + "/page",
			Mode: "css",
			Extractors: []any{
				map[string]any{"name": "title", "selector": "h1"},
				map[string]any{"name": "links", "selector": "a", "attr": "href", "multiple": true},
			},
		}},
	}
	run, err := NewEngine().Run(context.Background(), wf, nil, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("status: %s err=%s", run.Status, run.Err)
	}
	if !strings.Contains(run.Steps[0].Output, "Hello World") {
		t.Errorf("output should contain extracted title: %s", run.Steps[0].Output)
	}
}

func TestWebScrape_LLMMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><h1>Breaking News</h1></body></html>`)
	}))
	defer srv.Close()

	llm := &fakeLLM{chunks: []string{"headline: Breaking News"}}
	wf := Workflow{
		ID: "wf", Name: "wf", Version: 1,
		Steps: []Step{{
			Name:             "scrape",
			Kind:             StepKindWebScrape,
			URL:              srv.URL + "/page",
			Mode:             "llm",
			ExtractWithModel: "p1",
			ExtractPrompt:    "Extract the headline article title",
		}},
	}
	e := NewEngineWithDeps(Deps{LLM: llm, DefaultLLMProfile: "p1"})
	run, err := e.Run(context.Background(), wf, nil, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("status: %s err=%s", run.Status, run.Err)
	}
	if !strings.Contains(run.Steps[0].Output, "Breaking News") {
		t.Errorf("output should contain LLM extraction: %s", run.Steps[0].Output)
	}
}

func TestWebScrape_InvalidMode(t *testing.T) {
	wf := Workflow{
		ID: "wf", Name: "wf", Version: 1,
		Steps: []Step{{
			Name: "scrape", Kind: StepKindWebScrape,
			URL:  "https://example.com",
			Mode: "xpath", // unsupported
		}},
	}
	_, err := NewEngine().Run(context.Background(), wf, nil, RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("expected mode validation error, got %v", err)
	}
}

// denyAllAuthz is a NetworkAuthorizer that always denies.
type denyAllAuthz struct{}

func (denyAllAuthz) Authorize(_ context.Context, _, _ string) error {
	return fmt.Errorf("policy denied: network access not permitted in test")
}

// =============================================================================
// Downstream skip behavior (workflows-finalization-01NWFX01 WP03)
// =============================================================================

// TestLinearRunner_FailedStepSkipsDownstream verifies that when a linear-mode
// step fails, all remaining steps are recorded as "skipped" with an upstream
// reason, rather than silently disappearing from the transcript.
func TestLinearRunner_FailedStepSkipsDownstream(t *testing.T) {
	t.Parallel()
	mcp := &fakeMCP{err: errors.New("MCP server \"slack\" is not installed or not authorized — install it from Tools")}
	wf := Workflow{
		ID: "downstream-skip", Name: "Downstream Skip", Version: 1,
		Steps: []Step{
			{Name: "fetch_slack", Kind: StepKindMCPCall, Server: "slack", ToolName: "list_messages"},
			{Name: "write_summary", Kind: StepKindModelTurn, UserPrompt: "summarize", Profile: "default"},
			{Name: "notify_step", Kind: StepKindNotify, NotifyTitle: "Done", NotifyBody: "done", Surface: []string{"os"}},
		},
	}
	e := NewEngineWithDeps(Deps{MCP: mcp})
	run, err := e.Run(context.Background(), wf, nil, RunOptions{})
	if err == nil {
		t.Fatal("expected error from failed mcp_call")
	}
	if run.Status != "failed" {
		t.Errorf("run.Status=%q want failed", run.Status)
	}
	// All 3 steps must appear in the transcript.
	if len(run.Steps) != 3 {
		t.Fatalf("run.Steps=%d want 3 (failed + 2 skipped)", len(run.Steps))
	}
	// Step 0 must be failed.
	if run.Steps[0].Status != "failed" {
		t.Errorf("step[0] status=%q want failed", run.Steps[0].Status)
	}
	// Steps 1 and 2 must be skipped with an upstream reason.
	for i := 1; i <= 2; i++ {
		sr := run.Steps[i]
		if sr.Status != "skipped" {
			t.Errorf("step[%d] (%s) status=%q want skipped", i, sr.Name, sr.Status)
		}
		if !strings.Contains(sr.Err, "upstream step") {
			t.Errorf("step[%d] skip reason=%q does not mention upstream step", i, sr.Err)
		}
	}
}

