package rpc

import (
	"context"
	"errors"

	"github.com/sigil-tech/kaneaz-harness/core/autonomy"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/stdio"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/a2a"
	artifactsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/artifacts"
	attachmentsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/attachments"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/contextview"
	dialsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/dials"
	hooksview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/hooks"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/llm"
	memoryview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/memory"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/policy"
	projectsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/projects"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/sessions"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/shell"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/tools"
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
func (s *stubLLM) ResolveConfirm(_ context.Context, _, _ string) error { return errNotWired }
func (s *stubLLM) UpdateProviderCredential(_ context.Context, _, _ string) error {
	return errNotWired
}
func (s *stubLLM) GetAttachmentLimits(_ context.Context, _, _ string) (llm.AttachmentLimitsView, error) {
	return llm.AttachmentLimitsView{}, nil
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
func (s *stubSessions) DeleteWithOptions(_ context.Context, _ string, _ sessions.DeleteOptions) error {
	return errNotWired
}
func (s *stubSessions) Reorder(_ context.Context, _ []string) error { return errNotWired }
func (s *stubSessions) GetUsage(_ context.Context, _ string) (sessions.SessionUsage, error) {
	return sessions.SessionUsage{}, nil
}
func (s *stubSessions) StartStream(_ context.Context, _ string) (string, error) {
	return "", errNotWired
}
func (s *stubSessions) StopStream(_ context.Context, _ string) error { return errNotWired }
func (s *stubSessions) ListMessages(_ context.Context, _ string) ([]sessions.Message, error) {
	return []sessions.Message{}, nil
}
func (s *stubSessions) ListMessagesActive(_ context.Context, _ string) (sessions.ListMessagesResult, error) {
	return sessions.ListMessagesResult{Messages: []sessions.Message{}}, nil
}
func (s *stubSessions) ListMessagesAll(_ context.Context, _ string) (sessions.ListMessagesResult, error) {
	return sessions.ListMessagesResult{Messages: []sessions.Message{}}, nil
}
func (s *stubSessions) AppendMessage(_ context.Context, _, _, _ string) (sessions.Message, error) {
	return sessions.Message{}, errNotWired
}
func (s *stubSessions) SendMessageWithBlocks(_ context.Context, _ string, _ []sessions.ContentBlock) (sessions.Message, error) {
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
func (s *stubSessions) SuggestTitle(_ context.Context, _ string) (string, error) {
	return "", errNotWired
}
func (s *stubSessions) ClearTitle(_ context.Context, _ string) error {
	return errNotWired
}
func (s *stubSessions) ResumeMessage(_ context.Context, _, _ string) (sessions.ResumeMessageResult, error) {
	return sessions.ResumeMessageResult{}, errNotWired
}
func (s *stubSessions) LoadAutonomyProfile(_ context.Context, _ string) (autonomy.Layer, error) {
	return autonomy.Layer{}, nil
}
func (s *stubSessions) SaveAutonomyProfile(_ context.Context, _ string, _ autonomy.Layer) error {
	return errNotWired
}
func (s *stubSessions) ResolveAutonomy(_ context.Context, _ string) (sessions.ResolvedAutonomy, error) {
	return sessions.ResolvedAutonomy{}, errNotWired
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
func (s *stubMemory) Pin(_ context.Context, _ string, _ bool) error { return errNotWired }
func (s *stubMemory) JournalTail(_ context.Context, _ string, _ int64, _ int) ([]memoryview.JournalEntry, error) {
	return []memoryview.JournalEntry{}, nil
}
func (s *stubMemory) PrunePreview(_ context.Context, _ string) (memoryview.PrunePreview, error) {
	return memoryview.PrunePreview{}, nil
}
func (s *stubMemory) RunPruneNow(_ context.Context, _ string) (memoryview.PruneStats, error) {
	return memoryview.PruneStats{}, nil
}
func (s *stubMemory) HealthSnapshot(_ context.Context) (memoryview.HealthSnapshot, error) {
	return memoryview.HealthSnapshot{}, nil
}
func (s *stubMemory) TestEmbedder(_ context.Context) (int, error) {
	return 0, errNotWired
}
func (s *stubMemory) CaptureRate(_ context.Context) (memoryview.CaptureRateSnapshot, error) {
	return memoryview.CaptureRateSnapshot{EmbedderHealth: "ok"}, nil
}
func (s *stubMemory) EmbedderEligibility(_ context.Context) (memoryview.EmbedderEligibility, error) {
	return memoryview.EmbedderEligibility{}, nil
}
func (s *stubMemory) MarkImportant(_ context.Context, _ string, _ bool) error { return nil }
func (s *stubMemory) NarrativeFailedCount(_ context.Context) (int, error)     { return 0, nil }
func (s *stubMemory) NarrativeFailedList(_ context.Context) ([]memoryview.NarrativeJobStatus, error) {
	return []memoryview.NarrativeJobStatus{}, nil
}
func (s *stubMemory) RetryFailedNarrative(_ context.Context, _ string) error { return nil }
func (s *stubMemory) NarrativeMetricsForChunk(_ context.Context, _ string) (memoryview.NarrativeMetrics, error) {
	return memoryview.NarrativeMetrics{}, nil
}

// ── dials ──────────────────────────────────────────────────────────────

type stubDials struct{}

func (s *stubDials) GetDials(_ context.Context, _ dialsview.ScopeKey) (dialsview.DialConfig, error) {
	return dialsview.DialConfig{}, nil
}
func (s *stubDials) SetDials(_ context.Context, _ dialsview.ScopeKey, _ dialsview.DialConfig) error {
	return errNotWired
}
func (s *stubDials) GetEffective(_ context.Context, _, _, _, _ string) (dialsview.EffectiveDials, error) {
	return dialsview.EffectiveDials{}, nil
}
func (s *stubDials) BumpAndResume(_ context.Context, _ string, _ dialsview.DialDelta) error {
	return errNotWired
}

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
func (s *stubHooks) DryRun(_ context.Context, _ string, _ string) (hooksview.DryRunResult, error) {
	return hooksview.DryRunResult{}, errNotWired
}

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
func (s *stubProjects) LoadAutonomyProfile(_ context.Context, _ string) (autonomy.Layer, error) {
	return autonomy.Layer{}, nil
}
func (s *stubProjects) SaveAutonomyProfile(_ context.Context, _ string, _ autonomy.Layer) error {
	return errNotWired
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
func (s *stubAttachments) AddMedia(_ context.Context, _ attachmentsview.AddMediaInput) (attachmentsview.Attachment, error) {
	return attachmentsview.Attachment{}, errNotWired
}
func (s *stubAttachments) Remove(_ context.Context, _ string) error { return errNotWired }
func (s *stubAttachments) Reorder(_ context.Context, _, _ string, _ []string) error {
	return errNotWired
}
func (s *stubAttachments) Refresh(_ context.Context, _ string) (attachmentsview.Attachment, error) {
	return attachmentsview.Attachment{}, errNotWired
}

// ── artifacts ──────────────────────────────────────────────────────────

type stubArtifacts struct{}

func (s *stubArtifacts) List(_ context.Context, _ artifactsview.ArtifactFilter) ([]artifactsview.Artifact, error) {
	return []artifactsview.Artifact{}, nil
}
func (s *stubArtifacts) Get(_ context.Context, _ string) (artifactsview.ArtifactWithBytes, error) {
	return artifactsview.ArtifactWithBytes{}, errNotWired
}
func (s *stubArtifacts) Promote(_ context.Context, _, _, _ string) (artifactsview.Artifact, error) {
	return artifactsview.Artifact{}, errNotWired
}
func (s *stubArtifacts) Delete(_ context.Context, _ string) error { return errNotWired }
func (s *stubArtifacts) SaveFromMessage(_ context.Context, _, _, _ string, _, _ int) (artifactsview.Artifact, error) {
	return artifactsview.Artifact{}, errNotWired
}

// ── tools ──────────────────────────────────────────────────────────────

type stubTools struct{}

func (s *stubTools) ListRecipes(_ context.Context) ([]tools.RecipeListing, error) {
	return []tools.RecipeListing{}, nil
}
func (s *stubTools) InstallRecipe(_ context.Context, _ string, _ map[string]string, _ map[string]any) (stdio.RecipeStatus, error) {
	return stdio.RecipeStatus{}, errNotWired
}
func (s *stubTools) UninstallRecipe(_ context.Context, _ string) error { return errNotWired }
func (s *stubTools) ForgetRecipeKey(_ context.Context, _, _ string) error {
	return errNotWired
}
func (s *stubTools) RecipeStatus(_ context.Context, _ string) (stdio.RecipeStatus, error) {
	return stdio.RecipeStatus{}, errNotWired
}
func (s *stubTools) RecipeConfig(_ context.Context, _ string) (map[string]any, error) {
	return map[string]any{}, nil
}

func (s *stubTools) RequestAdditionalAllowedDir(_ context.Context, _, _, _ string) (bool, string, error) {
	return false, "", errNotWired
}

// ── shell ──────────────────────────────────────────────────────────────

type stubShell struct{}

func (s *stubShell) OpenInOSBrowser(_ context.Context, _ string) error {
	return errNotWired
}

func (s *stubShell) PathComplete(_ context.Context, _ string) ([]string, error) {
	return nil, errNotWired
}

func (s *stubShell) ReadFile(_ context.Context, _ string) ([]byte, string, error) {
	return nil, "", errNotWired
}

// Compile-time witness.
var _ shell.ShellAPI = (*stubShell)(nil)

// ── policy ─────────────────────────────────────────────────────────────

type stubPolicy struct{}

func (s *stubPolicy) Explain(_ context.Context, _ map[string]any) (policy.Denial, error) {
	return policy.Denial{}, errNotWired
}
func (s *stubPolicy) StartStream(_ context.Context) (string, error) { return "", errNotWired }
func (s *stubPolicy) StopStream(_ context.Context, _ string) error  { return errNotWired }

