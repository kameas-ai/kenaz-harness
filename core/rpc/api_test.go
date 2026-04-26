package rpc

import (
	"context"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/a2a"
	attachmentsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/attachments"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/audit"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/bundle"
	contextsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/contexts"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/contextview"
	hooksview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/hooks"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/llm"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/mcp"
	memoryview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/memory"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/policy"
	projectsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/projects"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/sessions"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/settings"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/tools"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/trust"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/workflow"
)

// fakeHarnessAPI is a compile-time witness that the HarnessAPI interface
// is satisfiable from a test fixture. Real wiring lives in api.go's API.
type fakeHarnessAPI struct {
	llmAPI      llm.LLMConnectorAPI
	mcpAPI      mcp.MCPAPI
	a2aAPI      a2a.A2AAPI
	workflowAPI workflow.WorkflowAPI
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
	toolsAPI        tools.ToolsAPI
}

func (f *fakeHarnessAPI) ShellStatus(_ context.Context) (ShellStatus, error) {
	return ShellStatus{}, nil
}
func (f *fakeHarnessAPI) AppInfo(_ context.Context) (AppInfo, error) { return AppInfo{}, nil }
func (f *fakeHarnessAPI) LLMConnector() llm.LLMConnectorAPI          { return f.llmAPI }
func (f *fakeHarnessAPI) MCP() mcp.MCPAPI                            { return f.mcpAPI }
func (f *fakeHarnessAPI) A2A() a2a.A2AAPI                            { return f.a2aAPI }
func (f *fakeHarnessAPI) Workflow() workflow.WorkflowAPI             { return f.workflowAPI }
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
func (f *fakeHarnessAPI) Tools() tools.ToolsAPI                       { return f.toolsAPI }

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
