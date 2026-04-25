package rpc

import (
	"context"
	"errors"

	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/a2a"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/contextview"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/llm"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/policy"
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
func (s *stubLLM) StartStream(_ context.Context, _, _ string) (string, error) {
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

// ── policy ─────────────────────────────────────────────────────────────

type stubPolicy struct{}

func (s *stubPolicy) Explain(_ context.Context, _ map[string]any) (policy.Denial, error) {
	return policy.Denial{}, errNotWired
}
func (s *stubPolicy) StartStream(_ context.Context) (string, error) { return "", errNotWired }
func (s *stubPolicy) StopStream(_ context.Context, _ string) error  { return errNotWired }

