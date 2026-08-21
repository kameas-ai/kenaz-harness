package rpc

// model-authored-graphs-01PMGA01 UNIT-7 — the two MCP tools + the audit
// kind. These tests drive the REAL production wire end to end:
// api.dispatchPool.Call(harnessmcp.ServerName, ...) -> the attached
// harness-self server -> handleDraftAgentGraph/handleMaterializeRun ->
// graphAuthorAdapter (core/rpc/harness_wiring.go) -> the SAME
// Manager.saveGraph/StartRun/MaterializeRun path UNIT-2..UNIT-6 already
// gate and test directly. Nothing here re-derives the manager-level
// gate; it proves the TOOL surface reaches it and does not leak or
// weaken anything on the way.
//
// Per spec.md §9 / harness-self-attach-01PMHS01 §7 rule 2: AC-004's
// refusal is asserted by calling the tool directly, bypassing the
// session tool LISTING — api.dispatchPool.Call does not itself consult
// a.toolPermsResolver (that containment lives in the separate
// chat.kernelToolAdapter dispatch path, proven elsewhere by
// TestHarnessSelfAttach_AC002_ChatSessionWriteToolDenied and the
// TestHarnessContainment_* suite) — so these tests do not need an
// onboarding session to reach the handler; they need the graph.author
// Cedar gate's own consent dial, which is orthogonal.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	harnessmcp "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/audit"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// callHarnessToolRaw dispatches through the real attached pool WITHOUT
// failing on IsError — unlike callHarnessTool (harness_self_attach_test.go),
// which is only appropriate for expected-success calls. Refusal
// (validation-failed / policy-denied) responses from
// harness_write_draft_agent_graph are NOT IsError — they are ordinary
// ToolResult{OK:false, ...} bodies (see handleDraftAgentGraph's doc
// comment) — so tests asserting a refusal must inspect the body
// themselves.
func callHarnessToolRaw(t *testing.T, api *API, sessionID, tool string, args json.RawMessage) transport.ToolsCallResult {
	t.Helper()
	ctx := context.Background()
	if sessionID != "" {
		ctx = toolloop.WithSessionID(ctx, sessionID)
	}
	raw, err := api.dispatchPool.Call(ctx, harnessmcp.ServerName, tool, args)
	if err != nil {
		t.Fatalf("Call(%s): %v — the server is not reachable through the attached pool", tool, err)
	}
	var result transport.ToolsCallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("Call(%s): decode ToolsCallResult: %v (raw=%s)", tool, err, raw)
	}
	return result
}

// decodeToolResult unwraps the MCP {type:"text", text:"<json>"} content
// block toolserver.JSONText produces (server.go's success path marshals
// the handler's return value, then wraps the marshalled STRING in a
// text block — the content bytes are not the ToolResult JSON directly)
// and decodes the inner text as a harness.ToolResult.
func decodeToolResult(t *testing.T, r transport.ToolsCallResult) harnessmcp.ToolResult {
	t.Helper()
	if len(r.Content) == 0 {
		t.Fatal("ToolsCallResult has no content")
	}
	var block struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(r.Content[0], &block); err != nil {
		t.Fatalf("decode content block: %v (raw=%s)", err, r.Content[0])
	}
	var tr harnessmcp.ToolResult
	if err := json.Unmarshal([]byte(block.Text), &tr); err != nil {
		t.Fatalf("decode ToolResult from content text: %v (text=%s)", err, block.Text)
	}
	return tr
}

func graphLibraryPath(dataDir, id string) string {
	return filepath.Join(dataDir, "agent_graph", "library", id+".yaml")
}

// TestGraphAuthoringTool_AC001_DraftsRealGraph is AC-001 through the
// tool surface: with authoring enabled, harness_write_draft_agent_graph
// persists a real, valid graph — asserted on the filesystem and the
// ordinary library listing/load path, not on the tool's own OK flag.
//
// Mutation (per tasks.md UNIT-7): make the adapter return success
// without calling graphview.API.SaveGraph. This test cannot directly
// plant that mutation (it is a production-code change), but it is
// structurally impossible to pass here without SaveGraph actually
// running: graphAuthorAdapter.DraftAgentGraph's only success return
// path is the saveErr == nil branch immediately after the a.api.SaveGraph
// call, and this test asserts the FILE and the LISTING, which only
// SaveGraph's real write path produces.
func TestGraphAuthoringTool_AC001_DraftsRealGraph(t *testing.T) {
	api := cedarWiringAPI(t, "")
	dataDir := api.core.DataDir()
	store := api.settingsImpl.Store()
	if err := store.SaveGraphAuthoringEnabled(true); err != nil {
		t.Fatalf("SaveGraphAuthoringEnabled(true): %v", err)
	}

	id := "zz_unit7_ac001_probe"
	yaml := graphAuthoringYAML(id)
	args, _ := json.Marshal(map[string]string{"id": id, "yaml": yaml})

	res := callHarnessToolRaw(t, api, "sess-ac001", harnessmcp.ToolDraftAgentGraph, args)
	if res.IsError {
		t.Fatalf("draft call reported IsError=true: %s", contentText(res))
	}
	tr := decodeToolResult(t, res)
	if !tr.OK {
		t.Fatalf("ToolResult.OK = false, want true: %+v", tr)
	}

	// The filesystem, not just the tool's own claim.
	if _, err := os.Stat(graphLibraryPath(dataDir, id)); err != nil {
		t.Fatalf("graph %q not persisted to disk: %v", id, err)
	}

	// The ordinary library listing.
	rows, err := api.Graph().ListGraphs(context.Background(), "user")
	if err != nil {
		t.Fatalf("ListGraphs: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.ID == id && r.Scope == "user" {
			found = true
			if r.SpecProvenance != coreag.SpecProvenanceModelAuthored {
				t.Errorf("listed specProvenance = %q, want %q", r.SpecProvenance, coreag.SpecProvenanceModelAuthored)
			}
		}
	}
	if !found {
		t.Fatalf("drafted graph %q not listed with scope=user", id)
	}

	// The ordinary load path, and it validates.
	loaded, err := api.Graph().LoadGraph(context.Background(), id)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	g, err := coreag.LoadYAML([]byte(loaded.YAML))
	if err != nil {
		t.Fatalf("persisted YAML does not parse: %v", err)
	}
	if err := coreag.Validate(g); err != nil {
		t.Fatalf("persisted YAML does not validate: %v", err)
	}
	if g.SpecProvenance != coreag.SpecProvenanceModelAuthored {
		t.Errorf("persisted spec_provenance = %q, want %q — stamping did not survive the tool round trip", g.SpecProvenance, coreag.SpecProvenanceModelAuthored)
	}
}

// TestGraphAuthoringTool_AC004_SecurityCriterion is AC-004 re-run
// through the tool surface (tasks.md UNIT-7): a fresh profile with no
// authoring setting refuses a perfectly valid draft through the TOOL,
// and nothing is written. Then the same dial, flipped through the real
// settings store, makes the identical call succeed.
func TestGraphAuthoringTool_AC004_SecurityCriterion(t *testing.T) {
	api := cedarWiringAPI(t, "")
	dataDir := api.core.DataDir()

	id := "zz_unit7_ac004_probe"
	yaml := graphAuthoringYAML(id)
	args, _ := json.Marshal(map[string]string{"id": id, "yaml": yaml})

	res := callHarnessToolRaw(t, api, "sess-ac004", harnessmcp.ToolDraftAgentGraph, args)
	if res.IsError {
		t.Fatalf("expected a classified refusal (OK:false), got IsError=true: %s", contentText(res))
	}
	tr := decodeToolResult(t, res)
	if tr.OK {
		t.Fatal("draft succeeded on a fresh profile with no consent setting — graph.author is not gated through the tool")
	}
	if tr.Message == "" {
		t.Error("refusal message is empty — the model gets no explanation")
	}
	if _, err := os.Stat(graphLibraryPath(dataDir, id)); err == nil {
		t.Fatalf("graph %q WAS persisted despite a denied graph.author evaluation", id)
	} else if !os.IsNotExist(err) {
		t.Fatalf("Stat: %v", err)
	}

	// Flip the dial through the real settings store.
	store := api.settingsImpl.Store()
	if err := store.SaveGraphAuthoringEnabled(true); err != nil {
		t.Fatalf("SaveGraphAuthoringEnabled(true): %v", err)
	}

	res2 := callHarnessToolRaw(t, api, "sess-ac004", harnessmcp.ToolDraftAgentGraph, args)
	if res2.IsError {
		t.Fatalf("draft call reported IsError=true after enabling authoring: %s", contentText(res2))
	}
	tr2 := decodeToolResult(t, res2)
	if !tr2.OK {
		t.Fatalf("draft still refused after enabling authoring: %+v", tr2)
	}
	if _, err := os.Stat(graphLibraryPath(dataDir, id)); err != nil {
		t.Fatalf("graph %q not persisted after enabling authoring: %v", id, err)
	}
}

// TestGraphAuthoringTool_ValidationFailureCarriesIssues is FR-002
// through the tool: YAML that parses but fails coreag.Validate returns
// a non-empty, structured Issues list and writes nothing.
func TestGraphAuthoringTool_ValidationFailureCarriesIssues(t *testing.T) {
	api := cedarWiringAPI(t, "")
	dataDir := api.core.DataDir()
	if err := api.settingsImpl.Store().SaveGraphAuthoringEnabled(true); err != nil {
		t.Fatalf("SaveGraphAuthoringEnabled(true): %v", err)
	}

	id := "zz_unit7_invalid_probe"
	// Edge to a node id that does not exist — parses, fails Validate.
	badYAML := `spec_version: "1"
id: ` + id + `
entrypoints: [a]
nodes:
  - id: a
    kind: plan
    attrs:
      verbosity: terse
edges:
  - from: {node: a, port: out}
    to: {node: nonexistent, port: in}
`
	args, _ := json.Marshal(map[string]string{"id": id, "yaml": badYAML})
	res := callHarnessToolRaw(t, api, "sess-invalid", harnessmcp.ToolDraftAgentGraph, args)
	if res.IsError {
		t.Fatalf("expected a classified refusal, got IsError=true: %s", contentText(res))
	}
	tr := decodeToolResult(t, res)
	if tr.OK {
		t.Fatal("invalid draft was accepted")
	}
	dataMap, ok := tr.Data.(map[string]any)
	if !ok {
		t.Fatalf("Data = %#v (%T), want map[string]any carrying issues", tr.Data, tr.Data)
	}
	issues, ok := dataMap["issues"]
	if !ok {
		t.Fatalf("Data has no \"issues\" key: %#v", dataMap)
	}
	issuesSlice, ok := issues.([]any)
	if !ok || len(issuesSlice) == 0 {
		t.Fatalf("issues = %#v, want a non-empty list", issues)
	}
	if _, err := os.Stat(graphLibraryPath(dataDir, id)); err == nil {
		t.Fatal("invalid graph was persisted to disk")
	}
}

// TestGraphAuthoringTool_CreateOnly is the E-008 create-only contract:
// an id that already exists (as a user graph) is refused rather than
// overwritten, and the existing file's content is untouched.
func TestGraphAuthoringTool_CreateOnly(t *testing.T) {
	api := cedarWiringAPI(t, "")
	dataDir := api.core.DataDir()
	if err := api.settingsImpl.Store().SaveGraphAuthoringEnabled(true); err != nil {
		t.Fatalf("SaveGraphAuthoringEnabled(true): %v", err)
	}

	id := "zz_unit7_create_only_probe"
	firstYAML := graphAuthoringYAML(id)
	args, _ := json.Marshal(map[string]string{"id": id, "yaml": firstYAML})
	res := callHarnessToolRaw(t, api, "sess-create-only", harnessmcp.ToolDraftAgentGraph, args)
	if res.IsError || !decodeToolResult(t, res).OK {
		t.Fatalf("first draft failed: IsError=%v", res.IsError)
	}
	before, err := os.ReadFile(graphLibraryPath(dataDir, id))
	if err != nil {
		t.Fatalf("read persisted file: %v", err)
	}

	// Second attempt at the SAME id, different body.
	secondYAML := `spec_version: "1"
id: ` + id + `
entrypoints: [b]
nodes:
  - id: b
    kind: plan
    attrs:
      verbosity: verbose
`
	args2, _ := json.Marshal(map[string]string{"id": id, "yaml": secondYAML})
	res2 := callHarnessToolRaw(t, api, "sess-create-only", harnessmcp.ToolDraftAgentGraph, args2)
	if res2.IsError {
		t.Fatalf("expected a classified refusal, got IsError=true: %s", contentText(res2))
	}
	tr2 := decodeToolResult(t, res2)
	if tr2.OK {
		t.Fatal("second draft at an existing id succeeded — create-only was not enforced")
	}
	after, err := os.ReadFile(graphLibraryPath(dataDir, id))
	if err != nil {
		t.Fatalf("read persisted file after collision attempt: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("the existing file's content changed despite a create-only collision — it was overwritten")
	}
}

// zz_ac010_fakeLLM answers with one tool call carrying a distinctive
// argument VALUE, so AC-010 can assert the value never reaches the
// materialized YAML while the argument KEY and tool name do.
type zzAC010FakeLLM struct{ called bool }

func (f *zzAC010FakeLLM) Generate(_ context.Context, _ coreag.LLMRequest) (coreag.LLMResponse, error) {
	if f.called {
		return coreag.LLMResponse{Content: "done", FinishReason: "stop"}, nil
	}
	f.called = true
	argsJSON, _ := json.Marshal(map[string]any{"path": "zz-ac010-distinctive-secret-9f8e7d6c5b4a"})
	return coreag.LLMResponse{
		Content:      "calling a tool",
		FinishReason: "tool_use",
		ToolCalls: []coreag.ToolCallRequest{
			{ID: "toolu_zzac010", Name: "kenaz__read_file", Arguments: string(argsJSON)},
		},
	}, nil
}

type zzAC010FakeTools struct{}

func (zzAC010FakeTools) Has(string) bool { return true }
func (zzAC010FakeTools) Call(_ context.Context, call coreag.ToolCall) (coreag.ToolResult, error) {
	return coreag.ToolResult{Content: "file contents (also must not leak, though FR-011 only promises key/value args are redacted)"}, nil
}

// ac010Graph is the smallest topology that produces one real
// EventToolCall through the kernel: a transform feeding a model node
// feeding tool_dispatch. No loop node — a single pass is sufficient to
// exercise the redaction path AC-010 cares about. MaxConcurrent (not
// ParallelDispatch) per spec.md §0.7 (X-6): this mission must not
// assert parallel tool dispatch as observed or fixture behaviour.
func ac010Graph(id string) coreag.Graph {
	return coreag.Graph{
		SpecVersion: coreag.SpecVersion,
		ID:          id,
		Name:        "AC-010 probe",
		Entrypoints: []string{"ask_user"},
		Nodes: []coreag.Node{
			{ID: "ask_user", Kind: coreag.NodeKindTransform, Title: "User turn", Attrs: coreag.TransformAttrs{Name: "concat"}},
			{
				ID: "assistant_turn", Kind: coreag.NodeKindModel, Title: "Assistant turn",
				Attrs: coreag.ModelAttrs{Model: "test-model", MaxTokens: 64},
				Outputs: []coreag.Port{
					{Name: "response", Type: coreag.PortTypeMessages},
					{Name: "assistant", Type: coreag.PortTypeAny},
					{Name: "tool_calls", Type: coreag.PortTypeAny},
					{Name: "finish_reason", Type: coreag.PortTypeText},
				},
			},
			{
				ID: "tool_dispatch", Kind: coreag.NodeKindToolDispatch, Title: "Dispatch tool calls",
				Attrs: coreag.ToolDispatchAttrs{MaxConcurrent: 1},
			},
		},
		Edges: []coreag.Edge{
			{From: coreag.EndpointRef{Node: "ask_user", Port: "out"}, To: coreag.EndpointRef{Node: "assistant_turn", Port: "messages"}},
			{From: coreag.EndpointRef{Node: "assistant_turn", Port: "tool_calls"}, To: coreag.EndpointRef{Node: "tool_dispatch", Port: "tool_calls"}},
		},
	}
}

// TestGraphAuthoringTool_AC010_MaterializeRunRedactsArguments is AC-010:
// the read tool projects a real run through the SAME redaction
// materialize.go already provides — the tool call's argument VALUE
// never appears in the returned YAML, while the tool NAME and the
// argument KEY do. Read tools are ungated (harness_read_default.cedar
// permits any session), so this test does not need the authoring dial.
func TestGraphAuthoringTool_AC010_MaterializeRunRedactsArguments(t *testing.T) {
	api := cedarWiringAPI(t, "")

	runID := "zz-unit7-ac010-run"
	g := ac010Graph("zz_unit7_ac010_graph")
	if err := coreag.Validate(g); err != nil {
		t.Fatalf("fixture graph does not validate: %v", err)
	}
	llm := &zzAC010FakeLLM{}
	env := &coreag.Env{RunID: runID, Graph: &g, LLM: llm, Tools: zzAC010FakeTools{}}
	if err := api.graphMgr.Kernel().Run(context.Background(), env); err != nil {
		t.Fatalf("kernel run: %v", err)
	}
	api.graphMgr.TrackExternalRun(runID, g)

	args, _ := json.Marshal(map[string]string{"run_id": runID})
	res := callHarnessTool(t, api, harnessmcp.ToolMaterializeRun, args)
	tr := decodeToolResult(t, res)
	if !tr.OK {
		t.Fatalf("materialize call not OK: %+v", tr)
	}
	body, _ := json.Marshal(tr.Data)
	var got harnessmcp.GraphMaterializeResult
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode GraphMaterializeResult: %v (body=%s)", err, body)
	}

	if !strings.Contains(got.YAML, "kenaz__read_file") {
		t.Errorf("materialized YAML does not contain the tool name kenaz__read_file:\n%s", got.YAML)
	}
	if !strings.Contains(got.YAML, "path") {
		t.Errorf("materialized YAML does not contain the argument KEY \"path\":\n%s", got.YAML)
	}
	if strings.Contains(got.YAML, "zz-ac010-distinctive-secret-9f8e7d6c5b4a") {
		t.Errorf("materialized YAML LEAKED the distinctive argument VALUE:\n%s", got.YAML)
	}
}

// TestGraphAuthoringTool_AC010_DegradedFallbackProvenanceSurfaces is the
// second half of AC-010: when the resolved spec is not tracked (the
// TrackExternalRun/started-run tiers both miss — the eviction/restart
// case, C-006), the tool's response carries the degraded
// "library_fallback" provenance rather than silently presenting a
// tier-3 projection as faithful.
func TestGraphAuthoringTool_AC010_DegradedFallbackProvenanceSurfaces(t *testing.T) {
	api := cedarWiringAPI(t, "")
	if err := api.settingsImpl.Store().SaveGraphAuthoringEnabled(true); err != nil {
		t.Fatalf("SaveGraphAuthoringEnabled(true): %v", err)
	}

	// Save a real user graph first (via the manager directly — this is
	// fixture setup, not the property under test), so LoadGraphSpec's
	// tier-3 fallback in runSpecFor has something to load. Transform-
	// only (not the "plan"-kind graphAuthoringYAML fixture): tier-3's
	// projection re-derives each fired node's attrs from this SAME
	// spec by node id, so the library definition and the run's actual
	// topology must share node ids/kinds for the projection to
	// validate — a "plan" node would need a real LLM to fire, which
	// this test must not depend on.
	libID := "zz_unit7_ac010_fallback_lib"
	libYAML := `spec_version: "1"
id: ` + libID + `
entrypoints: [first]
nodes:
  - id: first
    kind: transform
    attrs:
      name: concat
`
	if err := api.Graph().SaveGraph(context.Background(), graphview.GraphSpec{ID: libID, YAML: libYAML}, "user"); err != nil {
		t.Fatalf("seed library graph: %v", err)
	}

	// The RUN itself shares the library id and topology exactly, so
	// runSpecFor's tier-3 fallback (which re-loads the library file
	// rather than replaying what actually executed) produces a
	// projection that validates.
	runID := "zz-unit7-ac010-fallback-run"
	runGraph := coreag.Graph{
		SpecVersion: coreag.SpecVersion,
		ID:          libID,
		Name:        "AC-010 fallback run body",
		Entrypoints: []string{"first"},
		Nodes: []coreag.Node{
			{ID: "first", Kind: coreag.NodeKindTransform, Title: "First", Attrs: coreag.TransformAttrs{Name: "concat"}},
		},
	}
	env := &coreag.Env{RunID: runID, Graph: &runGraph}
	if err := api.graphMgr.Kernel().Run(context.Background(), env); err != nil {
		t.Fatalf("kernel run: %v", err)
	}
	// Deliberately NOT calling TrackExternalRun — this run must be
	// found ONLY through runSpecFor's tier-3 library fallback.

	args, _ := json.Marshal(map[string]string{"run_id": runID})
	res := callHarnessTool(t, api, harnessmcp.ToolMaterializeRun, args)
	tr := decodeToolResult(t, res)
	if !tr.OK {
		t.Fatalf("materialize call not OK: %+v", tr)
	}
	body, _ := json.Marshal(tr.Data)
	var got harnessmcp.GraphMaterializeResult
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode GraphMaterializeResult: %v", err)
	}
	if got.SpecProvenance != coreag.SpecProvenanceLibraryFallback {
		t.Errorf("SpecProvenance = %q, want %q (degraded fallback) — the tool must not present a tier-3 projection as faithful", got.SpecProvenance, coreag.SpecProvenanceLibraryFallback)
	}
}

// TestGraphAuthoringTool_AC011_AuditEmittedBothOutcomesNoLeak is AC-011:
// a permitted AND a refused draft attempt each emit
// kind.KindGraphAuthorAttempted with the outcome, and the payload never
// contains a distinctive string planted in the submitted YAML's attrs
// (asserted by searching the serialised Trailing field).
func TestGraphAuthoringTool_AC011_AuditEmittedBothOutcomesNoLeak(t *testing.T) {
	api := cedarWiringAPI(t, "")

	const secretMarker = "zz-ac011-planted-secret-marker-4d3c2b1a"
	refusedID := "zz_unit7_ac011_refused"
	refusedYAML := `spec_version: "1"
id: ` + refusedID + `
entrypoints: [wf]
nodes:
  - id: wf
    kind: write_file
    attrs:
      path: /tmp/` + secretMarker + `.txt
      content: ` + secretMarker + `
      mode: create
`
	args, _ := json.Marshal(map[string]string{"id": refusedID, "yaml": refusedYAML})
	// Authoring is OFF (fresh profile) — this is refused by
	// graph_author_forbid.cedar, not the write_file clause, but either
	// way it is a REFUSAL, which is what AC-011 needs a negative case for.
	refusedRes := callHarnessToolRaw(t, api, "sess-ac011-refused", harnessmcp.ToolDraftAgentGraph, args)
	if refusedRes.IsError {
		t.Fatalf("expected classified refusal, got IsError: %s", contentText(refusedRes))
	}
	if decodeToolResult(t, refusedRes).OK {
		t.Fatal("expected the draft to be refused (authoring off)")
	}

	if err := api.settingsImpl.Store().SaveGraphAuthoringEnabled(true); err != nil {
		t.Fatalf("SaveGraphAuthoringEnabled(true): %v", err)
	}
	permittedID := "zz_unit7_ac011_permitted"
	permittedYAML := graphAuthoringYAML(permittedID)
	args2, _ := json.Marshal(map[string]string{"id": permittedID, "yaml": permittedYAML})
	permittedRes := callHarnessToolRaw(t, api, "sess-ac011-permitted", harnessmcp.ToolDraftAgentGraph, args2)
	if permittedRes.IsError || !decodeToolResult(t, permittedRes).OK {
		t.Fatalf("expected the second draft to be permitted: IsError=%v", permittedRes.IsError)
	}

	entries, err := api.auditImpl.ListEntries(context.Background(), audit.Filter{})
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	var permittedEntry, refusedEntry *audit.Entry
	for i := range entries {
		if entries[i].Subject != "agentgraph.author.attempted" {
			continue
		}
		if strings.Contains(entries[i].Trailing, "graph_id="+permittedID) {
			permittedEntry = &entries[i]
		}
		if strings.Contains(entries[i].Trailing, "graph_id="+refusedID) {
			refusedEntry = &entries[i]
		}
	}
	if permittedEntry == nil {
		t.Fatal("no agentgraph.author.attempted entry found for the permitted draft")
	}
	if refusedEntry == nil {
		t.Fatal("no agentgraph.author.attempted entry found for the refused draft")
	}
	if !strings.Contains(permittedEntry.Trailing, "outcome=permitted") {
		t.Errorf("permitted entry Trailing = %q, want outcome=permitted", permittedEntry.Trailing)
	}
	if !strings.Contains(refusedEntry.Trailing, "outcome=refused") {
		t.Errorf("refused entry Trailing = %q, want outcome=refused", refusedEntry.Trailing)
	}
	if refusedEntry.Trailing == "" || !strings.Contains(refusedEntry.Trailing, "decision_reason=") {
		t.Errorf("refused entry Trailing = %q, want a non-empty decision_reason", refusedEntry.Trailing)
	}
	for _, e := range []*audit.Entry{permittedEntry, refusedEntry} {
		if strings.Contains(e.Trailing, secretMarker) {
			t.Errorf("audit entry LEAKED the planted secret marker: %q", e.Trailing)
		}
	}
	// node_kinds naming the KIND write_file (not a value) is expected
	// and fine on the refused entry — only the marker above is forbidden.
	if !strings.Contains(refusedEntry.Trailing, "write_file") {
		t.Errorf("refused entry Trailing = %q, want node_kinds to name write_file", refusedEntry.Trailing)
	}
}
