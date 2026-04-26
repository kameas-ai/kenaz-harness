package rpc

import (
	"context"
	"errors"

	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/a2a"
	attachmentsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/attachments"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/contextview"
	hooksview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/hooks"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/llm"
	memoryview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/memory"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/policy"
	projectsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/projects"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/sessions"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/trust"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/workflow"
)

// errNotWired is returned by every stub method. Feature missions replace
// these stubs with real implementations.
var errNotWired = errors.New("rpc: not wired by feature mission yet")

// ── llm ────────────────────────────────────────────────────────────────

type stubLLM struct{}

func (s *stubLLM) ListProviders(_ context.Context) ([]llm.Provider, error) {
	return nil, errNotWired
}
func (s *stubLLM) StartStream(_ context.Context, _, _, _ string) (string, error) {
	return "", errNotWired
}
func (s *stubLLM) StopStream(_ context.Context, _ string) error { return errNotWired }
func (s *stubLLM) AddProvider(_ context.Context, _ llm.AddProviderInput) error {
	return errNotWired
}
func (s *stubLLM) RemoveProvider(_ context.Context, _ string) error { return errNotWired }
func (s *stubLLM) UpdateProvider(_ context.Context, _ llm.AddProviderInput) error {
	return errNotWired
}
func (s *stubLLM) TestProvider(_ context.Context, _ string) (llm.TestResult, error) {
	return llm.TestResult{}, errNotWired
}
func (s *stubLLM) ListModels(_ context.Context, _, _ string) ([]llm.ModelInfo, error) {
	return nil, errNotWired
}

// ── a2a ────────────────────────────────────────────────────────────────

type stubA2A struct{}

func (s *stubA2A) ListCards(_ context.Context) ([]a2a.Card, error) { return nil, errNotWired }
func (s *stubA2A) StartStream(_ context.Context) (string, error)   { return "", errNotWired }
func (s *stubA2A) StopStream(_ context.Context, _ string) error    { return errNotWired }

// ── workflow ───────────────────────────────────────────────────────────

type stubWorkflow struct{}

func (s *stubWorkflow) ListJobs(_ context.Context) ([]workflow.Job, error) {
	return nil, errNotWired
}
func (s *stubWorkflow) StartStream(_ context.Context) (string, error) { return "", errNotWired }
func (s *stubWorkflow) StopStream(_ context.Context, _ string) error  { return errNotWired }

// ── sessions ───────────────────────────────────────────────────────────

type stubSessions struct{}

func (s *stubSessions) List(_ context.Context) ([]sessions.Session, error) {
	return []sessions.Session{}, nil
}
func (s *stubSessions) Get(_ context.Context, _ string) (sessions.Session, error) {
	return sessions.Session{}, errNotWired
}
func (s *stubSessions) Create(_ context.Context, _ string) (sessions.Session, error) {
	return sessions.Session{}, errNotWired
}
func (s *stubSessions) Rename(_ context.Context, _, _ string) error { return errNotWired }
func (s *stubSessions) Delete(_ context.Context, _ string) error    { return errNotWired }
func (s *stubSessions) Reorder(_ context.Context, _ []string) error { return errNotWired }
func (s *stubSessions) StartStream(_ context.Context, _ string) (string, error) {
	return "", errNotWired
}
func (s *stubSessions) StopStream(_ context.Context, _ string) error { return errNotWired }
func (s *stubSessions) ListMessages(_ context.Context, _ string) ([]sessions.Message, error) {
	return []sessions.Message{}, nil
}
func (s *stubSessions) AppendMessage(_ context.Context, _, _, _ string) (sessions.Message, error) {
	return sessions.Message{}, errNotWired
}
func (s *stubSessions) SaveDraft(_ context.Context, _, _ string) error { return nil }
func (s *stubSessions) LoadDraft(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (s *stubSessions) SetSystemPrompt(_ context.Context, _, _, _ string) error {
	return errNotWired
}
func (s *stubSessions) MoveToProject(_ context.Context, _, _ string) error {
	return errNotWired
}

// ── trust ──────────────────────────────────────────────────────────────

type stubTrust struct{}

func (s *stubTrust) ListSecretReferences(_ context.Context) ([]trust.SecretReference, error) {
	return nil, errNotWired
}
func (s *stubTrust) GetSecretReference(_ context.Context, _ string) (trust.SecretReference, error) {
	return trust.SecretReference{}, errNotWired
}

// ── context ────────────────────────────────────────────────────────────

type stubContext struct{}

func (s *stubContext) List(_ context.Context) ([]contextview.ContextEntry, error) {
	return nil, errNotWired
}
func (s *stubContext) StartStream(_ context.Context) (string, error) { return "", errNotWired }
func (s *stubContext) StopStream(_ context.Context, _ string) error  { return errNotWired }

// ── memory ─────────────────────────────────────────────────────────────

type stubMemory struct{}

func (s *stubMemory) ListChunks(_ context.Context, _ memoryview.ListFilter) ([]memoryview.Chunk, error) {
	return []memoryview.Chunk{}, nil
}
func (s *stubMemory) RememberMessage(_ context.Context, _, _, _ string) (string, error) {
	return "", errNotWired
}
func (s *stubMemory) PromoteScope(_ context.Context, _, _, _ string) (string, error) {
	return "", errNotWired
}
func (s *stubMemory) Forget(_ context.Context, _ string) error { return errNotWired }

// ── hooks ──────────────────────────────────────────────────────────────

type stubHooks struct{}

func (s *stubHooks) List(_ context.Context) ([]hooksview.Hook, error) {
	return []hooksview.Hook{}, nil
}
func (s *stubHooks) Get(_ context.Context, _ string) (hooksview.Hook, error) {
	return hooksview.Hook{}, errNotWired
}
func (s *stubHooks) Add(_ context.Context, _ hooksview.HookInput) (hooksview.Hook, error) {
	return hooksview.Hook{}, errNotWired
}
func (s *stubHooks) Update(_ context.Context, _ hooksview.HookInput) error { return errNotWired }
func (s *stubHooks) Remove(_ context.Context, _ string) error              { return errNotWired }
func (s *stubHooks) AvailableBuiltins(_ context.Context) ([]hooksview.BuiltinDescriptor, error) {
	return []hooksview.BuiltinDescriptor{}, nil
}
func (s *stubHooks) InstallStarterMemoryHooks(_ context.Context) error { return nil }
func (s *stubHooks) RemoveStarterMemoryHooks(_ context.Context) error  { return nil }

// ── projects ───────────────────────────────────────────────────────────

type stubProjects struct{}

func (s *stubProjects) List(_ context.Context) ([]projectsview.Project, error) {
	return []projectsview.Project{}, nil
}
func (s *stubProjects) Get(_ context.Context, _ string) (projectsview.Project, error) {
	return projectsview.Project{}, errNotWired
}
func (s *stubProjects) Create(_ context.Context, _, _ string) (projectsview.Project, error) {
	return projectsview.Project{}, errNotWired
}
func (s *stubProjects) Rename(_ context.Context, _, _ string) error            { return errNotWired }
func (s *stubProjects) UpdateDescription(_ context.Context, _, _ string) error { return errNotWired }
func (s *stubProjects) Delete(_ context.Context, _ string, _ bool) error       { return errNotWired }
func (s *stubProjects) AddSession(_ context.Context, _, _ string) error        { return errNotWired }
func (s *stubProjects) RemoveSession(_ context.Context, _ string) error        { return errNotWired }
func (s *stubProjects) ListSessions(_ context.Context, _ string) ([]projectsview.Session, error) {
	return []projectsview.Session{}, nil
}

// ── attachments ────────────────────────────────────────────────────────

type stubAttachments struct{}

func (s *stubAttachments) List(_ context.Context, _, _ string) ([]attachmentsview.Attachment, error) {
	return []attachmentsview.Attachment{}, nil
}
func (s *stubAttachments) ListResolved(_ context.Context, _ string) ([]attachmentsview.Attachment, error) {
	return []attachmentsview.Attachment{}, nil
}
func (s *stubAttachments) Add(_ context.Context, _ attachmentsview.AddInput) (attachmentsview.Attachment, error) {
	return attachmentsview.Attachment{}, errNotWired
}
func (s *stubAttachments) Remove(_ context.Context, _ string) error { return errNotWired }
func (s *stubAttachments) Reorder(_ context.Context, _, _ string, _ []string) error {
	return errNotWired
}
func (s *stubAttachments) Refresh(_ context.Context, _ string) (attachmentsview.Attachment, error) {
	return attachmentsview.Attachment{}, errNotWired
}

// ── policy ─────────────────────────────────────────────────────────────

type stubPolicy struct{}

func (s *stubPolicy) Explain(_ context.Context, _ map[string]any) (policy.Denial, error) {
	return policy.Denial{}, errNotWired
}
func (s *stubPolicy) StartStream(_ context.Context) (string, error) { return "", errNotWired }
func (s *stubPolicy) StopStream(_ context.Context, _ string) error  { return errNotWired }

