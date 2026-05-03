// Package branches is the view-scoped accessor for the conversation
// branching subsystem (mission agent-kernel-graph; Bundle B WP08).
// Wraps core/conversation.Manager + core/agentgraph.BranchRecommender
// behind the rpc boundary so the frontend never imports the storage
// layout directly.
//
// The wire shapes mirror core/conversation types but render timestamps
// as RFC3339Nano strings for byte-stable JSON.
package branches

import (
	"context"
	"time"
)

// Branch is the wire shape for one branches row.
type Branch struct {
	ID              string `json:"id"`
	ParentSessionID string `json:"parentSessionId"`
	ChildSessionID  string `json:"childSessionId"`
	Kind            string `json:"kind"`
	Status          string `json:"status"`
	ProviderID      string `json:"providerId,omitempty"`
	ModelID         string `json:"modelId,omitempty"`
	Title           string `json:"title,omitempty"`
	TaskHint        string `json:"taskHint,omitempty"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
	MergedAt        string `json:"mergedAt,omitempty"`
	AbandonedAt     string `json:"abandonedAt,omitempty"`
	// Subagent-enriched fields (branch-as-subagent-recommendation WP04).
	SubagentBranch   bool     `json:"subagentBranch,omitempty"`
	RecommendationID string   `json:"recommendationId,omitempty"`
	AdvisorSignals   []string `json:"advisorSignals,omitempty"`
}

// CreateBranchOptions is the request body for CreateBranch.
type CreateBranchOptions struct {
	ParentSessionID string `json:"parentSessionId"`
	// Title is a short user-facing branch title.
	Title string `json:"title,omitempty"`
	// TaskHint is the user's free-text description; the recommendation
	// heuristic uses it.
	TaskHint string `json:"taskHint,omitempty"`
	// ModelPreference picks the recommendation tier: "smaller" |
	// "larger" | "same" | "exact". Default "same" pins parent's tier.
	ModelPreference string `json:"modelPreference,omitempty"`
	// ExactProviderID + ExactModelID let the user pin a specific model
	// (when ModelPreference == "exact").
	ExactProviderID string `json:"exactProviderId,omitempty"`
	ExactModelID    string `json:"exactModelId,omitempty"`
	// SystemPromptOverride replaces the auto-summarized handoff prompt
	// when non-empty; otherwise the kernel compacts the parent tail.
	SystemPromptOverride string `json:"systemPromptOverride,omitempty"`
	// ChildName overrides the auto-generated "<parent> (branch)" name.
	ChildName string `json:"childName,omitempty"`
	// Subagent-enriched fields (WP04). When RecommendationID is
	// non-empty, CreateBranch sets SubagentBranch = true on the row.
	RecommendationID  string   `json:"recommendationId,omitempty"`
	AdvisorSignals    []string `json:"advisorSignals,omitempty"`
	AdvisorConfidence float64  `json:"advisorConfidence,omitempty"`
}

// ReintegrationProposal is the response shape for
// ProposeReintegrationSummary (WP05).
type ReintegrationProposal struct {
	// ProposedSummary is the model-generated (or rule-based) summary.
	// Empty when the branch has no assistant turns yet.
	ProposedSummary string `json:"proposedSummary"`
	// TokenCount is the estimated token count of ProposedSummary.
	TokenCount int `json:"tokenCount"`
	// Model is the (provider:model) string that produced the summary, or
	// "rule_based" for the no-LLM path.
	Model string `json:"model,omitempty"`
}

// CommitReintegrationOptions is the request body for CommitReintegration
// (WP07).
type CommitReintegrationOptions struct {
	// BranchSessionID is the child session whose transcript was summarised.
	BranchSessionID string `json:"branchSessionId"`
	// FinalSummaryText is the (possibly edited) summary to insert.
	FinalSummaryText string `json:"finalSummaryText"`
	// WasEdited is true when the user modified the proposed text before
	// submitting; carried through to the audit payload.
	WasEdited bool `json:"wasEdited"`
}

// BranchStatus + ChildRunStatus is the wire shape for GetBranchStatus.
type BranchStatus struct {
	Branch          Branch `json:"branch"`
	ChildSessionID  string `json:"childSessionId"`
	HasInflightRun  bool   `json:"hasInflightRun"`
	LastActivityAt  string `json:"lastActivityAt,omitempty"`
	LastAssistantMsg string `json:"lastAssistantMessage,omitempty"`
}

// RecommendedModel is the wire shape for RecommendModel.
type RecommendedModel struct {
	ProviderID string `json:"providerId"`
	ModelID    string `json:"modelId"`
	Tier       string `json:"tier"`
	Reason     string `json:"reason"`
	Notes      string `json:"notes,omitempty"`
	// CrossProviderWarning is non-empty when the recommended model
	// crosses a provider boundary that may lose content fidelity
	// (e.g. Anthropic image blocks → OpenAI). Spec FR-039.
	CrossProviderWarning string `json:"crossProviderWarning,omitempty"`
}

// BranchesAPI is the view-scoped surface backing /branches.
//
// A nil manager is allowed — methods return ErrManagerUnavailable so
// the frontend can render an empty state without the chassis crashing.
type BranchesAPI interface {
	// ListBranches returns every branch off a parent session.
	ListBranches(ctx context.Context, parentSessionID string) ([]Branch, error)
	// CreateBranch allocates a new fork. Returns the populated row plus
	// the new child session id (callers route the user into the child
	// session immediately). When opts.RecommendationID is set, the
	// branch is flagged as a subagent branch (WP04).
	CreateBranch(ctx context.Context, opts CreateBranchOptions) (Branch, error)
	// GetBranchStatus returns the latest lifecycle state + child run
	// snapshot.
	GetBranchStatus(ctx context.Context, branchID string) (BranchStatus, error)
	// MergeBranch runs the merge action manually (skipping the
	// suggestion-based prompt). Returns nil on success; the parent
	// session has the summary message appended.
	MergeBranch(ctx context.Context, branchID string) error
	// AbandonBranch flips the branch row's status to abandoned. The
	// child session is preserved; the rail UI greys it out.
	AbandonBranch(ctx context.Context, branchID string) error
	// RecommendModel returns the recommended (provider, model) for a
	// fork off parentSessionID with the given task hint + preference.
	RecommendModel(ctx context.Context, parentSessionID, taskHint, preference string) (RecommendedModel, error)

	// ProposeReintegrationSummary generates a proposed summary of the
	// branch transcript (WP05 / FR-008a). Callers present this in the
	// ReintegrationPreviewModal for user review + editing.
	ProposeReintegrationSummary(ctx context.Context, branchSessionID string) (ReintegrationProposal, error)

	// CommitReintegration inserts the final (possibly user-edited)
	// summary as a synthetic system message in the parent session and
	// flips the branch to merged status (WP07 / FR-008b).
	CommitReintegration(ctx context.Context, opts CommitReintegrationOptions) error

	// SetAdvisorDismissed persists the per-session "don't suggest again"
	// flag for the branch advisor (FR-010 / WP04).
	SetAdvisorDismissed(ctx context.Context, sessionID string, dismissed bool) error
}

// fmtTime renders a time.Time as RFC3339Nano UTC, returning "" for the
// zero value.
func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// fmtTimePtr is the *time.Time variant.
func fmtTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return fmtTime(*t)
}
