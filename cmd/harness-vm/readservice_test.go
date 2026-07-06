package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/memory"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/tools"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/workflows"
	"github.com/kameas-ai/kenaz-harness/core/units"
)

// The view interfaces are large (20+ methods); the fakes embed the interface
// and override only the read method readservice.go calls. Any other method is
// nil and would panic if invoked — which never happens in these tests.

type fakeSessions struct {
	sessions.SessionsAPI
	list []sessions.Session
	msgs []sessions.Message
}

func (f fakeSessions) List(context.Context) ([]sessions.Session, error) { return f.list, nil }
func (f fakeSessions) Get(_ context.Context, id string) (sessions.Session, error) {
	for _, s := range f.list {
		if s.ID == id {
			return s, nil
		}
	}
	return sessions.Session{}, nil
}
func (f fakeSessions) ListMessages(context.Context, string) ([]sessions.Message, error) {
	return f.msgs, nil
}

type fakeTools struct {
	tools.ToolsAPI
	list []tools.RecipeListing
}

func (f fakeTools) ListRecipes(context.Context) ([]tools.RecipeListing, error) { return f.list, nil }

type fakeMemory struct {
	memory.MemoryAPI
	list []memory.Chunk
}

func (f fakeMemory) ListChunks(context.Context, memory.ListFilter) ([]memory.Chunk, error) {
	return f.list, nil
}

type fakeWorkflows struct {
	workflows.WorkflowsAPI
	list []workflows.Summary
}

func (f fakeWorkflows) List(context.Context) ([]workflows.Summary, error) { return f.list, nil }

type fakeLLM struct {
	llm.LLMConnectorAPI
	list []llm.Provider
}

func (f fakeLLM) ListProviders(context.Context) ([]llm.Provider, error) { return f.list, nil }

type fakeReadAPI struct {
	sess  fakeSessions
	tool  fakeTools
	mem   fakeMemory
	wf    fakeWorkflows
	llm   fakeLLM
	units *units.Manager // nil → Units() returns nil (unavailable path)
}

func (f *fakeReadAPI) Sessions() sessions.SessionsAPI    { return f.sess }
func (f *fakeReadAPI) Tools() tools.ToolsAPI             { return f.tool }
func (f *fakeReadAPI) Memory() memory.MemoryAPI          { return f.mem }
func (f *fakeReadAPI) Workflows() workflows.WorkflowsAPI { return f.wf }
func (f *fakeReadAPI) LLMConnector() llm.LLMConnectorAPI { return f.llm }
func (f *fakeReadAPI) Units() *units.Manager             { return f.units }

func newTestReadService(api readAPI) *readService {
	return &readService{api: api, log: newTestLogger()}
}

// marshal serialises a response message the way connWriter.send does, so the
// privacy assertions inspect the exact bytes that cross the wire.
func marshal(t *testing.T, m msg) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestSessionsList(t *testing.T) {
	api := &fakeReadAPI{sess: fakeSessions{list: []sessions.Session{
		{ID: "s1", Name: "first", CreatedAt: "2026-06-01T00:00:00Z", Kind: "chat"},
		{ID: "s2", Name: "second", CreatedAt: "2026-06-02T00:00:00Z"},
	}}}
	resp := newTestReadService(api).handle(context.Background(), "sessions.list", msg{"req_id": "r1"})
	if resp["kind"] != "sessions.list.ok" {
		t.Fatalf("kind = %v", resp["kind"])
	}
	if resp["req_id"] != "r1" {
		t.Fatalf("req_id not echoed: %v", resp["req_id"])
	}
	out := resp["sessions"].([]wireSession)
	if len(out) != 2 || out[0].ID != "s1" || out[0].Name != "first" {
		t.Fatalf("sessions = %+v", out)
	}
}

func TestSessionsListMessagesTruncates(t *testing.T) {
	big := strings.Repeat("x", maxMessageBytes+500)
	api := &fakeReadAPI{sess: fakeSessions{msgs: []sessions.Message{
		{ID: "m1", Role: "user", Content: big, CreatedAt: "2026-06-01T00:00:00Z"},
	}}}
	resp := newTestReadService(api).handle(context.Background(), "sessions.list_messages", msg{"sessionID": "s1"})
	out := resp["messages"].([]wireMessage)
	if len(out) != 1 {
		t.Fatalf("len = %d", len(out))
	}
	if !out[0].Truncated {
		t.Fatalf("expected truncated")
	}
	if len(out[0].Content) > maxMessageBytes {
		t.Fatalf("content not capped: %d bytes", len(out[0].Content))
	}
}

func TestSessionsGetMissingID(t *testing.T) {
	resp := newTestReadService(&fakeReadAPI{}).handle(context.Background(), "sessions.get", msg{})
	if resp["kind"] != "sessions.get.error" || resp["code"] != "bad_request" {
		t.Fatalf("expected bad_request, got %+v", resp)
	}
}

// TestMemoryNeverLeaksContent is a privacy gate: memory.list_chunks must never
// emit the raw chunk Content, FilesRead, or FilesModified.
func TestMemoryNeverLeaksContent(t *testing.T) {
	const secret = "SECRET-CHUNK-BODY-do-not-leak"
	api := &fakeReadAPI{mem: fakeMemory{list: []memory.Chunk{{
		ID:            "c1",
		ScopeKind:     "session",
		ScopeID:       "s1",
		Title:         "harmless summary title",
		Content:       secret,
		FilesRead:     []string{"/home/user/.ssh/id_rsa"},
		FilesModified: []string{"/etc/shadow"},
		CreatedAt:     time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}}}}
	resp := newTestReadService(api).handle(context.Background(), "memory.list_chunks", msg{})
	raw := marshal(t, resp)
	if strings.Contains(raw, secret) {
		t.Fatalf("LEAK: chunk content in response: %s", raw)
	}
	if strings.Contains(raw, "id_rsa") || strings.Contains(raw, "shadow") {
		t.Fatalf("LEAK: file paths in response: %s", raw)
	}
	out := resp["chunks"].([]wireChunk)
	if out[0].Summary != "harmless summary title" {
		t.Fatalf("summary = %q", out[0].Summary)
	}
}

// TestProvidersNeverLeakCredentials is the credential privacy gate.
func TestProvidersNeverLeakCredentials(t *testing.T) {
	const locator = "keychain-entry-prod-anthropic-key-name"
	api := &fakeReadAPI{llm: fakeLLM{list: []llm.Provider{
		{
			ID:        "anthropic",
			Name:      "Anthropic",
			Kind:      "anthropic",
			Validated: true,
			Cred:      llm.CredentialReference{Kind: "keychain", Locator: locator},
		},
		{ID: "openai", Name: "OpenAI", Kind: "openai"},
	}}}
	resp := newTestReadService(api).handle(context.Background(), "providers.list", msg{})
	raw := marshal(t, resp)
	if strings.Contains(raw, locator) {
		t.Fatalf("LEAK: credential locator in response: %s", raw)
	}
	out := resp["providers"].([]wireProvider)
	if !out[0].Present || out[0].SourceTag != "keychain" {
		t.Fatalf("anthropic provider wrong: %+v", out[0])
	}
	if out[1].Present {
		t.Fatalf("openai should be present=false: %+v", out[1])
	}
}

func TestToolsList(t *testing.T) {
	api := &fakeReadAPI{tool: fakeTools{list: []tools.RecipeListing{
		{Recipe: recipes.Recipe{ID: "fs", DisplayName: "Filesystem"}, Enabled: true},
		{Recipe: recipes.Recipe{ID: "git"}, Enabled: false}, // no DisplayName → falls back to ID
	}}}
	resp := newTestReadService(api).handle(context.Background(), "tools.list", msg{})
	out := resp["tools"].([]wireTool)
	if out[0].Name != "Filesystem" || !out[0].Enabled || out[0].Kind != "mcp" {
		t.Fatalf("tool0 = %+v", out[0])
	}
	if out[1].Name != "git" {
		t.Fatalf("tool1 fallback name = %q", out[1].Name)
	}
}

func TestWorkflowsList(t *testing.T) {
	api := &fakeReadAPI{wf: fakeWorkflows{list: []workflows.Summary{
		{ID: "w1", Name: "nightly", StepCount: 3, Source: "user"},
	}}}
	resp := newTestReadService(api).handle(context.Background(), "workflows.list", msg{})
	out := resp["workflows"].([]wireWorkflow)
	if out[0].ID != "w1" || out[0].StepCount != 3 || out[0].Source != "user" {
		t.Fatalf("wf0 = %+v", out[0])
	}
}

// newTestUnits builds a units.Manager over an in-memory store seeded with the
// given units, exercising the real List/filter/order path the read RPCs hit.
func newTestUnits(t *testing.T, seed ...units.Unit) *units.Manager {
	t.Helper()
	mgr := units.NewManager(units.NewMemoryStore())
	for _, u := range seed {
		if _, err := mgr.Create(context.Background(), u); err != nil {
			t.Fatalf("seed unit: %v", err)
		}
	}
	return mgr
}

func TestUnitsList(t *testing.T) {
	mgr := newTestUnits(t,
		units.Unit{ID: "u1", Kind: units.KindDoc, Scope: units.ScopeSession, ScopeID: "s1",
			Classification: units.ClassPersonal, LoadPolicy: units.LoadOnDemand, Title: "notes", Body: "hello"},
		units.Unit{ID: "a1", Kind: units.KindArtifact, Scope: units.ScopeGlobal,
			Classification: units.ClassTeam, LoadPolicy: units.LoadAlways, Title: "patch", Body: "diff --git"},
	)
	api := &fakeReadAPI{units: mgr}
	resp := newTestReadService(api).handle(context.Background(), "units.list", msg{"req_id": "r1"})
	if resp["kind"] != "units.list.ok" {
		t.Fatalf("kind = %v", resp["kind"])
	}
	if resp["req_id"] != "r1" {
		t.Fatalf("req_id not echoed: %v", resp["req_id"])
	}
	out := resp["units"].([]wireUnit)
	if len(out) != 2 {
		t.Fatalf("expected 2 units, got %d: %+v", len(out), out)
	}
	// Every kind appears — units.list is unfiltered.
	kinds := map[string]bool{}
	for _, u := range out {
		kinds[u.Kind] = true
	}
	if !kinds["doc"] || !kinds["artifact"] {
		t.Fatalf("units.list should surface all kinds, got %v", kinds)
	}
}

func TestArtifactsListFiltersToArtifactKind(t *testing.T) {
	mgr := newTestUnits(t,
		units.Unit{ID: "u1", Kind: units.KindDoc, Scope: units.ScopeGlobal,
			Classification: units.ClassPersonal, LoadPolicy: units.LoadOnDemand, Title: "doc"},
		units.Unit{ID: "a1", Kind: units.KindArtifact, Scope: units.ScopeGlobal,
			Classification: units.ClassTeam, LoadPolicy: units.LoadAlways, Title: "art", Body: "code"},
		units.Unit{ID: "a2", Kind: units.KindArtifact, Scope: units.ScopeGlobal,
			Classification: units.ClassOrg, LoadPolicy: units.LoadAlways, Title: "art2", Body: "more code"},
	)
	api := &fakeReadAPI{units: mgr}
	resp := newTestReadService(api).handle(context.Background(), "artifacts.list", msg{})
	if resp["kind"] != "artifacts.list.ok" {
		t.Fatalf("kind = %v", resp["kind"])
	}
	out := resp["artifacts"].([]wireUnit)
	if len(out) != 2 {
		t.Fatalf("expected 2 artifacts (doc filtered out), got %d: %+v", len(out), out)
	}
	for _, u := range out {
		if u.Kind != "artifact" {
			t.Fatalf("artifacts.list leaked non-artifact kind %q", u.Kind)
		}
	}
}

// TestUnitsListBodyPreviewTruncates asserts the body preview never exceeds the
// read-service cap — no full/unbounded unit body crosses the wire.
func TestUnitsListBodyPreviewTruncates(t *testing.T) {
	big := strings.Repeat("Z", maxUnitBodyPreviewBy+2048)
	mgr := newTestUnits(t,
		units.Unit{ID: "u1", Kind: units.KindArtifact, Scope: units.ScopeGlobal,
			Classification: units.ClassTeam, LoadPolicy: units.LoadAlways, Title: "big", Body: big},
	)
	api := &fakeReadAPI{units: mgr}
	resp := newTestReadService(api).handle(context.Background(), "units.list", msg{})
	out := resp["units"].([]wireUnit)
	if len(out) != 1 {
		t.Fatalf("len = %d", len(out))
	}
	if !out[0].Truncated {
		t.Fatalf("expected truncated body preview")
	}
	if len(out[0].BodyPreview) > maxUnitBodyPreviewBy {
		t.Fatalf("body preview not capped: %d bytes", len(out[0].BodyPreview))
	}
	// The full body must NOT appear anywhere in the serialized response.
	if strings.Contains(marshal(t, resp), big) {
		t.Fatalf("LEAK: full unit body crossed the wire")
	}
}

func TestUnitsListUnavailableWhenNilManager(t *testing.T) {
	api := &fakeReadAPI{} // units == nil
	for _, kind := range []string{"units.list", "artifacts.list"} {
		resp := newTestReadService(api).handle(context.Background(), kind, msg{"req_id": "z"})
		if resp["kind"] != kind+".error" || resp["code"] != "unavailable" {
			t.Fatalf("%s: expected unavailable, got %+v", kind, resp)
		}
		if resp["req_id"] != "z" {
			t.Fatalf("%s: req_id not echoed", kind)
		}
	}
}

func TestReadServiceUnavailable(t *testing.T) {
	rs := &readService{log: newTestLogger()} // nil api
	resp := rs.handle(context.Background(), "sessions.list", msg{"req_id": "x"})
	if resp["kind"] != "sessions.list.error" || resp["code"] != "unavailable" {
		t.Fatalf("expected unavailable, got %+v", resp)
	}
	if resp["req_id"] != "x" {
		t.Fatalf("req_id not echoed on error")
	}
}

func TestIsReadKind(t *testing.T) {
	for _, k := range []string{"sessions.list", "sessions.get", "sessions.list_messages",
		"tools.list", "memory.list_chunks", "providers.list", "workflows.list",
		"units.list", "artifacts.list"} {
		if !isReadKind(k) {
			t.Errorf("isReadKind(%q) = false", k)
		}
	}
	for _, k := range []string{"task.start", "task.cancel", "auth", "sessions.delete", ""} {
		if isReadKind(k) {
			t.Errorf("isReadKind(%q) = true", k)
		}
	}
}
