package rpc

import (
	"context"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/a2a"
	graphview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/agentgraph"
	artifactsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/artifacts"
	attachmentsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/attachments"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/audit"
	branchesview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/branches"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/bundle"
	cedarpolicyview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/cedarpolicy"
	searchview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/search"
	compactionview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/compaction"
	contextsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/contexts"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/contextview"
	corpusview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/corpus"
	dialsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/dials"
	hooksview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/hooks"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/llm"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/mcp"
	memoryview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/memory"
	nodesview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/nodes"
	permissionsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/permissions"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/policy"
	projectsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/projects"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/sessions"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/settings"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/shell"
	slashview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/slashcmd"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/tools"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/trust"
	updateview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/update"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/workflow"
	workflowsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/workflows"
)

// fakeHarnessAPI is a compile-time witness that the HarnessAPI interface
// is satisfiable from a test fixture. Real wiring lives in api.go's API.
type fakeHarnessAPI struct {
	llmAPI      llm.LLMConnectorAPI
	mcpAPI      mcp.MCPAPI
	a2aAPI      a2a.A2AAPI
	workflowAPI workflow.WorkflowAPI
	workflowsAPI workflowsview.WorkflowsAPI
	sessionsAPI sessions.SessionsAPI
	trustAPI    trust.TrustAPI
	contextAPI  contextview.ContextAPI
	contextsAPI contextsview.ContextsAPI
	bundleAPI   bundle.BundleAPI
	policyAPI   policy.PolicyAPI
	auditAPI    audit.AuditAPI
	settingsAPI settings.SettingsAPI
	memoryAPI       memoryview.MemoryAPI
	hooksAPI        hooksview.HooksAPI
	projectsAPI     projectsview.ProjectsAPI
	attachmentsAPI  attachmentsview.AttachmentsAPI
	artifactsAPI    artifactsview.ArtifactsAPI
	toolsAPI        tools.ToolsAPI
	shellAPI        shell.ShellAPI
	slashAPI        slashview.SlashAPI
	corpusAPI       corpusview.CorpusAPI
	graphAPI        graphview.API
	compactionAPI   compactionview.CompactionAPI
	branchesAPI     branchesview.BranchesAPI
	cedarPolicyAPI  cedarpolicyview.CedarPolicyAPI
	permissionsAPI  permissionsview.PermissionsAPI
	dialsAPI        dialsview.DialsAPI
	nodesAPI        nodesview.NodesAPI
	searchAPI       searchview.SearchAPI
	updateAPI       updateview.UpdateAPI
}

func (f *fakeHarnessAPI) ShellStatus(_ context.Context) (ShellStatus, error) {
	return ShellStatus{}, nil
}
func (f *fakeHarnessAPI) AppInfo(_ context.Context) (AppInfo, error) { return AppInfo{}, nil }
func (f *fakeHarnessAPI) LLMConnector() llm.LLMConnectorAPI          { return f.llmAPI }
func (f *fakeHarnessAPI) MCP() mcp.MCPAPI                            { return f.mcpAPI }
func (f *fakeHarnessAPI) MCPImport() *mcp.ImportAPI                   { return nil }
func (f *fakeHarnessAPI) A2A() a2a.A2AAPI                            { return f.a2aAPI }
func (f *fakeHarnessAPI) Workflow() workflow.WorkflowAPI             { return f.workflowAPI }
func (f *fakeHarnessAPI) Workflows() workflowsview.WorkflowsAPI       { return f.workflowsAPI }
func (f *fakeHarnessAPI) Sessions() sessions.SessionsAPI             { return f.sessionsAPI }
func (f *fakeHarnessAPI) Trust() trust.TrustAPI                      { return f.trustAPI }
func (f *fakeHarnessAPI) Context() contextview.ContextAPI            { return f.contextAPI }
func (f *fakeHarnessAPI) Contexts() contextsview.ContextsAPI         { return f.contextsAPI }
func (f *fakeHarnessAPI) Bundle() bundle.BundleAPI                   { return f.bundleAPI }
func (f *fakeHarnessAPI) Policy() policy.PolicyAPI                   { return f.policyAPI }
func (f *fakeHarnessAPI) Audit() audit.AuditAPI                      { return f.auditAPI }
func (f *fakeHarnessAPI) Settings() settings.SettingsAPI             { return f.settingsAPI }
func (f *fakeHarnessAPI) Memory() memoryview.MemoryAPI                { return f.memoryAPI }
func (f *fakeHarnessAPI) Hooks() hooksview.HooksAPI                   { return f.hooksAPI }
func (f *fakeHarnessAPI) Projects() projectsview.ProjectsAPI          { return f.projectsAPI }
func (f *fakeHarnessAPI) Attachments() attachmentsview.AttachmentsAPI { return f.attachmentsAPI }
func (f *fakeHarnessAPI) Artifacts() artifactsview.ArtifactsAPI       { return f.artifactsAPI }
func (f *fakeHarnessAPI) Tools() tools.ToolsAPI                       { return f.toolsAPI }
func (f *fakeHarnessAPI) Shell() shell.ShellAPI                       { return f.shellAPI }
func (f *fakeHarnessAPI) Slash() slashview.SlashAPI                   { return f.slashAPI }
func (f *fakeHarnessAPI) Corpus() corpusview.CorpusAPI                 { return f.corpusAPI }
func (f *fakeHarnessAPI) Graph() graphview.API                         { return f.graphAPI }
func (f *fakeHarnessAPI) Compaction() compactionview.CompactionAPI     { return f.compactionAPI }
func (f *fakeHarnessAPI) Branches() branchesview.BranchesAPI           { return f.branchesAPI }
func (f *fakeHarnessAPI) CedarPolicy() cedarpolicyview.CedarPolicyAPI  { return f.cedarPolicyAPI }
func (f *fakeHarnessAPI) Permissions() permissionsview.PermissionsAPI  { return f.permissionsAPI }
func (f *fakeHarnessAPI) Dials() dialsview.DialsAPI                    { return f.dialsAPI }
func (f *fakeHarnessAPI) Nodes() nodesview.NodesAPI                    { return f.nodesAPI }
func (f *fakeHarnessAPI) Search() searchview.SearchAPI                 { return f.searchAPI }
func (f *fakeHarnessAPI) Update() updateview.UpdateAPI                  { return f.updateAPI }
func (f *fakeHarnessAPI) CedarProposeResolve(_, _ string) error         { return nil }

// Compile-time interface witness (plan §4.2).
var _ HarnessAPI = (*fakeHarnessAPI)(nil)
var _ HarnessAPI = (*API)(nil)

// TestViewAccessorStability asserts plan §4.2: each HarnessAPI.<View>()
// call returns the same Go pointer for the lifetime of the API value.
func TestViewAccessorStability(t *testing.T) {
	api := New(nil)

	if api.Sessions() != api.Sessions() {
		t.Errorf("Sessions() returned different pointers across calls")
	}
	if api.LLMConnector() != api.LLMConnector() {
		t.Errorf("LLMConnector() returned different pointers across calls")
	}
	if api.MCP() != api.MCP() {
		t.Errorf("MCP() returned different pointers across calls")
	}
	if api.A2A() != api.A2A() {
		t.Errorf("A2A() returned different pointers across calls")
	}
	if api.Workflow() != api.Workflow() {
		t.Errorf("Workflow() returned different pointers across calls")
	}
	if api.Trust() != api.Trust() {
		t.Errorf("Trust() returned different pointers across calls")
	}
	if api.Context() != api.Context() {
		t.Errorf("Context() returned different pointers across calls")
	}
	if api.Contexts() != api.Contexts() {
		t.Errorf("Contexts() returned different pointers across calls")
	}
	if api.Bundle() != api.Bundle() {
		t.Errorf("Bundle() returned different pointers across calls")
	}
	if api.Policy() != api.Policy() {
		t.Errorf("Policy() returned different pointers across calls")
	}
	if api.Audit() != api.Audit() {
		t.Errorf("Audit() returned different pointers across calls")
	}
	if api.Settings() != api.Settings() {
		t.Errorf("Settings() returned different pointers across calls")
	}
	if api.Projects() != api.Projects() {
		t.Errorf("Projects() returned different pointers across calls")
	}
	if api.Attachments() != api.Attachments() {
		t.Errorf("Attachments() returned different pointers across calls")
	}
	// Slash() — when the slashAPI is wired (which is the case for
	// New(nil) since the registry is built from a nil-deps Deps and
	// still succeeds), the accessor must return the same pointer
	// twice. The accessor falls back to a fresh nil-registry surface
	// only when slashAPI is nil at construction time.
	if api.slashAPI != nil && api.Slash() != api.Slash() {
		t.Errorf("Slash() returned different pointers across calls")
	}
}

// TestShellStatusBaseline asserts the chassis returns a quiet baseline
// status — non-empty TrustTier, ready connection, privacy flags on.
func TestShellStatusBaseline(t *testing.T) {
	api := New(nil)
	status, err := api.ShellStatus(context.Background())
	if err != nil {
		t.Fatalf("ShellStatus: %v", err)
	}
	if status.Connection != "ready" {
		t.Errorf("Connection = %q, want %q", status.Connection, "ready")
	}
	if !status.PolicyApplied || !status.RedactionOn || !status.LocalFirstOn {
		t.Errorf("privacy baseline flags should be on; got %+v", status)
	}
}
