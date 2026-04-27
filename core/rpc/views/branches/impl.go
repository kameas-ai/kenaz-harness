// Concrete BranchesAPI implementation. Wraps core/conversation.Manager
// + core/agentgraph.BranchRecommender so the rpc surface stays a thin
// projection over the storage / heuristic layers.
package branches

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sigil-tech/kaneaz-harness/core/agentgraph"
	"github.com/sigil-tech/kaneaz-harness/core/conversation"
	"github.com/sigil-tech/kaneaz-harness/core/session"
)

// ErrManagerUnavailable signals the chassis booted without the
// conversation manager wired.
var ErrManagerUnavailable = errors.New("branches: manager unavailable")

// ErrInvalidArg covers trivially invalid inputs.
var ErrInvalidArg = errors.New("branches: invalid argument")

// Config bundles the dependencies the impl needs.
type Config struct {
	// Conversations is the conversation.Manager (branches table).
	Conversations *conversation.Manager
	// Sessions is the session.Manager (parent + child sessions).
	Sessions *session.Manager
	// Recommender is the model-recommendation heuristic. nil disables
	// the recommendation chip — RecommendModel returns the parent's
	// model unchanged.
	Recommender *agentgraph.BranchRecommender
	// ModelInfos is the list of (provider, model, tier) triples used
	// by RecommendModel. May be nil; the recommender falls back to
	// the parent's exact pair when the list is empty.
	ModelInfos []agentgraph.ModelInfo
}

// API is the concrete BranchesAPI implementation.
type API struct {
	cfg Config
}

// New constructs a BranchesAPI from cfg. A nil-Conversations cfg is
// allowed — methods then return ErrManagerUnavailable.
func New(cfg Config) *API {
	return &API{cfg: cfg}
}

// ListBranches returns every branch off a parent session.
func (a *API) ListBranches(ctx context.Context, parentSessionID string) ([]Branch, error) {
	if a == nil || a.cfg.Conversations == nil {
		return []Branch{}, ErrManagerUnavailable
	}
	if parentSessionID == "" {
		return nil, ErrInvalidArg
	}
	rows, err := a.cfg.Conversations.ListByParent(ctx, parentSessionID)
	if err != nil {
		return nil, err
	}
	out := make([]Branch, 0, len(rows))
	for _, b := range rows {
		out = append(out, toWire(b))
	}
	return out, nil
}

// CreateBranch allocates a new fork.
func (a *API) CreateBranch(ctx context.Context, opts CreateBranchOptions) (Branch, error) {
	if a == nil || a.cfg.Conversations == nil {
		return Branch{}, ErrManagerUnavailable
	}
	if opts.ParentSessionID == "" {
		return Branch{}, ErrInvalidArg
	}

	// Pick a (provider, model) up-front. The recommender favors the
	// parent's provider when one is configured at the recommended tier.
	parentProvider, parentModel := a.parentModel(ctx, opts.ParentSessionID)
	pref := agentgraph.ForkPreference(strings.TrimSpace(strings.ToLower(opts.ModelPreference)))
	if pref == "" {
		pref = agentgraph.ForkPrefSame
	}
	var rec agentgraph.Recommendation
	if pref == agentgraph.ForkPrefExact {
		rec = agentgraph.Recommendation{
			ProviderID: opts.ExactProviderID,
			ModelID:    opts.ExactModelID,
			Tier:       agentgraph.ModelTierMedium,
			Reason:     agentgraph.ReasonUserOverride,
			Notes:      "Pinned by user",
		}
	} else if a.cfg.Recommender != nil {
		rec = a.cfg.Recommender.Recommend(parentProvider, parentModel, opts.TaskHint, pref)
	} else {
		rec = agentgraph.Recommendation{
			ProviderID: parentProvider,
			ModelID:    parentModel,
			Tier:       agentgraph.ModelTierMedium,
			Reason:     agentgraph.ReasonDefault,
		}
	}

	br, child, err := a.cfg.Conversations.CreateBranch(ctx, conversation.ForkOptions{
		ParentSessionID: opts.ParentSessionID,
		ChildName:       opts.ChildName,
		Title:           opts.Title,
		TaskHint:        opts.TaskHint,
		ProviderID:      rec.ProviderID,
		ModelID:         rec.ModelID,
	})
	if err != nil {
		return Branch{}, err
	}

	// Write the handoff prompt as the child session's first user
	// message. v1 uses the system prompt override when non-empty; else
	// a tiny placeholder ("<task hint or title>"). Bundle B's kernel
	// can replace this once the compaction strategies wire in.
	if a.cfg.Sessions != nil {
		handoff := strings.TrimSpace(opts.SystemPromptOverride)
		if handoff == "" {
			handoff = strings.TrimSpace(opts.TaskHint)
		}
		if handoff == "" {
			handoff = strings.TrimSpace(opts.Title)
		}
		if handoff != "" {
			_, _ = a.cfg.Sessions.AppendMessage(ctx, child.ID, session.Message{
				Role:    session.RoleUser,
				Content: handoff,
			})
		}
	}

	return toWire(br), nil
}

// GetBranchStatus returns the latest lifecycle state + child run snapshot.
func (a *API) GetBranchStatus(ctx context.Context, branchID string) (BranchStatus, error) {
	if a == nil || a.cfg.Conversations == nil {
		return BranchStatus{}, ErrManagerUnavailable
	}
	if branchID == "" {
		return BranchStatus{}, ErrInvalidArg
	}
	br, err := a.cfg.Conversations.Get(ctx, branchID)
	if err != nil {
		return BranchStatus{}, err
	}
	out := BranchStatus{
		Branch:         toWire(br),
		ChildSessionID: br.ChildSessionID,
	}
	// Best-effort child activity snapshot.
	if a.cfg.Sessions != nil && br.ChildSessionID != "" {
		msgs, err := a.cfg.Sessions.ListMessages(ctx, br.ChildSessionID)
		if err == nil && len(msgs) > 0 {
			last := msgs[len(msgs)-1]
			out.LastActivityAt = fmtTime(last.CreatedAt)
			if last.Role == session.RoleAssistant {
				out.LastAssistantMsg = last.Content
			}
		}
	}
	return out, nil
}

// MergeBranch runs the merge action manually.
func (a *API) MergeBranch(ctx context.Context, branchID string) error {
	if a == nil || a.cfg.Conversations == nil {
		return ErrManagerUnavailable
	}
	if branchID == "" {
		return ErrInvalidArg
	}
	br, err := a.cfg.Conversations.Get(ctx, branchID)
	if err != nil {
		return err
	}
	// Compose a simple summary from the child session's recent tail.
	// v1 deliberately keeps this synchronous — the full kernel-driven
	// summarize_append fires through the agentgraph MergeNode when the
	// kernel is wired into the request path.
	summary := "Branch closed."
	if a.cfg.Sessions != nil {
		msgs, err := a.cfg.Sessions.ListMessages(ctx, br.ChildSessionID)
		if err == nil && len(msgs) > 0 {
			var b strings.Builder
			b.WriteString("Branch summary (")
			b.WriteString(br.Title)
			b.WriteString("):\n")
			tail := msgs
			if len(tail) > 5 {
				tail = tail[len(tail)-5:]
			}
			for _, m := range tail {
				if m.Role == session.RoleAssistant {
					b.WriteString("- ")
					b.WriteString(strings.TrimSpace(m.Content))
					b.WriteString("\n")
				}
			}
			summary = b.String()
		}
		// Append the summary as a system message on the parent.
		if _, err := a.cfg.Sessions.AppendMessage(ctx, br.ParentSessionID, session.Message{
			Role:    session.RoleSystem,
			Content: summary,
		}); err != nil {
			return fmt.Errorf("branches: append parent: %w", err)
		}
	}
	return a.cfg.Conversations.MarkMerged(ctx, branchID)
}

// AbandonBranch flips the branch row's status to abandoned.
func (a *API) AbandonBranch(ctx context.Context, branchID string) error {
	if a == nil || a.cfg.Conversations == nil {
		return ErrManagerUnavailable
	}
	if branchID == "" {
		return ErrInvalidArg
	}
	return a.cfg.Conversations.MarkAbandoned(ctx, branchID)
}

// RecommendModel returns the recommendation for a fork off parentSessionID.
func (a *API) RecommendModel(ctx context.Context, parentSessionID, taskHint, preference string) (RecommendedModel, error) {
	if a == nil || a.cfg.Recommender == nil {
		return RecommendedModel{}, ErrManagerUnavailable
	}
	if parentSessionID == "" {
		return RecommendedModel{}, ErrInvalidArg
	}
	parentProvider, parentModel := a.parentModel(ctx, parentSessionID)
	pref := agentgraph.ForkPreference(strings.TrimSpace(strings.ToLower(preference)))
	rec := a.cfg.Recommender.Recommend(parentProvider, parentModel, taskHint, pref)
	out := RecommendedModel{
		ProviderID: rec.ProviderID,
		ModelID:    rec.ModelID,
		Tier:       string(rec.Tier),
		Reason:     string(rec.Reason),
		Notes:      rec.Notes,
	}
	// Cross-provider warning (FR-039). v1 just reports the kind change.
	if parentProvider != "" && rec.ProviderID != "" && parentProvider != rec.ProviderID {
		out.CrossProviderWarning = "Cross-provider fork: image bytes / citations may lose fidelity in conversion."
	}
	return out, nil
}

// parentModel returns the (provider, model) pair the parent session is
// currently using. v1 falls back to ("","") when the session has no
// recorded model — a nil parentModel is OK for the heuristic to chew.
func (a *API) parentModel(_ context.Context, _ string) (string, string) {
	// v1: we don't yet thread the parent's active model through
	// session.Record. The recommender accepts empty parents and uses
	// the model id heuristic. Future patch: read from the per-session
	// model dial.
	return "", ""
}

// toWire projects a conversation.Branch onto the Branch wire shape.
func toWire(b conversation.Branch) Branch {
	return Branch{
		ID:              b.ID,
		ParentSessionID: b.ParentSessionID,
		ChildSessionID:  b.ChildSessionID,
		Kind:            string(b.Kind),
		Status:          string(b.Status),
		ProviderID:      b.ProviderID,
		ModelID:         b.ModelID,
		Title:           b.Title,
		TaskHint:        b.TaskHint,
		CreatedAt:       fmtTime(b.CreatedAt),
		UpdatedAt:       fmtTime(b.UpdatedAt),
		MergedAt:        fmtTimePtr(b.MergedAt),
		AbandonedAt:     fmtTimePtr(b.AbandonedAt),
	}
}

// Compile-time witness.
var _ BranchesAPI = (*API)(nil)
