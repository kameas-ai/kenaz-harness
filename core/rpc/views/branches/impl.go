// Concrete BranchesAPI implementation. Wraps core/conversation.Manager
// + core/agentgraph.BranchRecommender so the rpc surface stays a thin
// projection over the storage / heuristic layers.
package branches

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/conversation"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

// ErrManagerUnavailable signals the chassis booted without the
// conversation manager wired.
var ErrManagerUnavailable = errors.New("branches: manager unavailable")

// ErrInvalidArg covers trivially invalid inputs.
var ErrInvalidArg = errors.New("branches: invalid argument")

// BranchListBroker is the narrow publish surface the branches API needs
// to emit session.list_changed events after a new branch session is created.
// Production binds this to *rpc.StreamBroker; tests inject a recording fake.
type BranchListBroker interface {
	Publish(topic string, payload any)
}

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
	// Audit is the append-only event log emitter (branch-as-subagent-
	// recommendation WP04/WP07). nil means audit events are silently
	// dropped — acceptable in test harnesses.
	Audit audit.Emitter
	// Now is the wall-clock source. nil uses time.Now().UTC().
	Now func() time.Time
	// Broker is the optional event publisher for session.list_changed.
	// When non-nil, every successful CreateBranch (which creates a new
	// child session row) emits a "branch_created" event so the LeftRail
	// refreshes in real-time (v0.5.3 fix).
	Broker BranchListBroker
	// Settings resolves the current persisted Settings snapshot. Read by
	// ProposeReintegrationSummary for BranchReintegrationMaxTokens
	// (engineer-truth-pass-01PMTP01 WP02, finding B2). nil is a valid
	// value — ProposeReintegrationSummary falls back to
	// settings.DefaultBranchReintegrationMaxTokens when Settings is nil,
	// matching the pre-WP02 hardcoded default so the New(nil) test-
	// harness path keeps working unchanged. The wiring site
	// (core/rpc/api.go) is responsible for swallowing load errors into
	// the zero value, which EffectiveBranchReintegrationMaxTokens
	// already treats as "use the default".
	Settings func() settings.Settings
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

// branchListChangedPayload is the local mirror of
// rpc.SessionListChangedPayload. Duplicated here to avoid an import cycle
// (rpc imports views/branches; views/branches must not import rpc).
// JSON field names are identical so the frontend deserialises them the same.
type branchListChangedPayload struct {
	Reason    string `json:"reason"`
	SessionID string `json:"sessionId,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

// publishBranchCreated emits a "branch_created" list-changed event when
// a Broker is wired. childSessionID is the newly created session row.
func (a *API) publishBranchCreated(childSessionID string) {
	if a == nil || a.cfg.Broker == nil {
		return
	}
	a.cfg.Broker.Publish("session.list_changed", branchListChangedPayload{
		Reason:    "branch_created",
		SessionID: childSessionID,
		Timestamp: time.Now().UnixMilli(),
	})
}

// now returns the current UTC time, using the configured clock or
// time.Now().UTC() when none is set.
func (a *API) now() time.Time {
	if a.cfg.Now != nil {
		return a.cfg.Now()
	}
	return time.Now().UTC()
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

// CreateBranch allocates a new fork. When opts.ParentMessageID is set,
// it delegates to CreateBranchAtMessage (explicit fork path) and copies
// messages [0..ParentMessageID] into the child session. Otherwise it
// falls back to the legacy CreateBranch path.
func (a *API) CreateBranch(ctx context.Context, opts CreateBranchOptions) (Branch, error) {
	if a == nil || a.cfg.Conversations == nil {
		return Branch{}, ErrManagerUnavailable
	}
	if opts.ParentSessionID == "" {
		return Branch{}, ErrInvalidArg
	}

	// Explicit-fork path: use CreateBranchAtMessage when ParentMessageID is set.
	if opts.ParentMessageID != "" {
		br, _, err := a.cfg.Conversations.CreateBranchAtMessage(ctx, conversation.ForkAtMessageOptions{
			ParentSessionID: opts.ParentSessionID,
			ParentMessageID: opts.ParentMessageID,
			Title:           opts.Title,
			ChildName:       opts.ChildName,
		})
		if err != nil {
			return Branch{}, err
		}
		// Emit KindBranchCreated audit event for explicit path.
		audit.MustEmit(ctx, a.cfg.Audit, audit.KindBranchCreated,
			audit.BranchCreatedPayload{
				ParentSessionID: opts.ParentSessionID,
				ParentMessageID: opts.ParentMessageID,
				BranchSessionID: br.ChildSessionID,
				CreationPath:    "explicit",
			}, a.now())
		a.publishBranchCreated(br.ChildSessionID)
		return toWire(br), nil
	}

	// Legacy path: pick a (provider, model) up-front. The recommender
	// favors the parent's provider when one is configured at the
	// recommended tier.
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

	isSubagent := strings.TrimSpace(opts.RecommendationID) != ""
	br, child, err := a.cfg.Conversations.CreateBranch(ctx, conversation.ForkOptions{
		ParentSessionID:  opts.ParentSessionID,
		ChildName:        opts.ChildName,
		Title:            opts.Title,
		TaskHint:         opts.TaskHint,
		ProviderID:       rec.ProviderID,
		ModelID:          rec.ModelID,
		SubagentBranch:   isSubagent,
		RecommendationID: strings.TrimSpace(opts.RecommendationID),
		AdvisorSignals:   opts.AdvisorSignals,
		// CreationPath was previously dropped here, so a caller-supplied
		// value (e.g. "edit_resend") never reached storage — every
		// legacy-path branch persisted as "unknown" regardless of intent
		// (audit-that-tells-the-truth-01PMZA10 WP06).
		CreationPath: opts.CreationPath,
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

	// Emit audit event for accepted subagent branch (WP04).
	if isSubagent {
		audit.MustEmit(ctx, a.cfg.Audit, audit.KindBranchAdvisorAccepted,
			audit.BranchAdvisorAcceptedPayload{
				Confidence:       opts.AdvisorConfidence,
				BranchSessionID:  br.ChildSessionID,
				RecommendationID: opts.RecommendationID,
			}, a.now())
	}

	// Emit KindBranchCreated for the legacy path too (ZA10 WP06). Before
	// this fix only the explicit-fork path above ever emitted — the
	// ordinary "+ Fork" click (CreateBranchModal.vue, which never sets
	// ParentMessageID) produced zero audit trail, even though it is the
	// common case, not the edge case. br.CreationPath is already
	// resolved by conversation.Manager.CreateBranch (defaults to
	// "unknown" when the caller didn't specify one), so it is reused
	// verbatim rather than re-deriving the default here.
	audit.MustEmit(ctx, a.cfg.Audit, audit.KindBranchCreated,
		audit.BranchCreatedPayload{
			ParentSessionID: opts.ParentSessionID,
			BranchSessionID: br.ChildSessionID,
			CreationPath:    br.CreationPath,
		}, a.now())

	a.publishBranchCreated(br.ChildSessionID)
	return toWire(br), nil
}

// ListWithBranchTree returns a flat list of sessions with parent pointers
// for branches under a project. Sessions are returned oldest-first.
// BranchDepth is pre-computed by iterative ancestor walks (capped at 32).
// (branching-ux-polish-01KQ8TD7 WP02)
func (a *API) ListWithBranchTree(ctx context.Context, projectID string) ([]SessionWithBranchPointer, error) {
	if a == nil || a.cfg.Conversations == nil {
		return nil, ErrManagerUnavailable
	}
	if projectID == "" {
		return nil, ErrInvalidArg
	}
	if a.cfg.Sessions == nil {
		return nil, ErrManagerUnavailable
	}

	sessions, err := a.cfg.Sessions.ListByProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("branches: list sessions for project: %w", err)
	}

	// Build a map of childSessionID → branch for quick lookup.
	childToBranch := map[string]conversation.Branch{}
	for _, sess := range sessions {
		branches, err := a.cfg.Conversations.ListByParent(ctx, sess.ID)
		if err != nil {
			return nil, fmt.Errorf("branches: list branches for session: %w", err)
		}
		for _, br := range branches {
			childToBranch[br.ChildSessionID] = br
		}
	}

	// Build parent-set for depth computation.
	parentOf := map[string]string{} // sessionID → parentSessionID
	for childID, br := range childToBranch {
		parentOf[childID] = br.ParentSessionID
	}

	computeDepth := func(sessionID string) int {
		depth := 0
		cur := sessionID
		for depth <= 32 {
			parent, ok := parentOf[cur]
			if !ok {
				break
			}
			depth++
			cur = parent
		}
		return depth
	}

	out := make([]SessionWithBranchPointer, 0, len(sessions))
	for _, sess := range sessions {
		ptr := SessionWithBranchPointer{
			SessionID:   sess.ID,
			SessionName: sess.Name,
			CreatedAt:   sess.CreatedAt.UTC().Format(time.RFC3339Nano),
			BranchDepth: computeDepth(sess.ID),
		}
		if br, ok := childToBranch[sess.ID]; ok {
			ptr.ParentSessionID = br.ParentSessionID
			ptr.ParentMessageID = br.ParentMessageID
			ptr.BranchTitle = br.BranchTitle
			ptr.ParentSessionTitle = br.ParentSessionTitle
		}
		out = append(out, ptr)
	}
	return out, nil
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

// ProposeReintegrationSummary generates a proposed summary for the
// branch transcript (WP05 / FR-008a). v1 is rule-based: it concatenates
// the last ≤8 assistant turns from the child session, truncated to
// DefaultBranchReintegrationMaxTokens-worth of runes (rough 4-char /
// token estimate). A follow-up patch can swap in a real LLM call when
// Settings.CompactionModel is wired.
func (a *API) ProposeReintegrationSummary(ctx context.Context, branchSessionID string) (ReintegrationProposal, error) {
	if a == nil || a.cfg.Sessions == nil {
		return ReintegrationProposal{}, ErrManagerUnavailable
	}
	if branchSessionID == "" {
		return ReintegrationProposal{}, ErrInvalidArg
	}
	msgs, err := a.cfg.Sessions.ListMessages(ctx, branchSessionID)
	if err != nil {
		return ReintegrationProposal{}, fmt.Errorf("branches: list messages: %w", err)
	}
	// Collect the last ≤8 assistant turns.
	var assistantMsgs []string
	for _, m := range msgs {
		if m.Role == session.RoleAssistant {
			assistantMsgs = append(assistantMsgs, strings.TrimSpace(m.Content))
		}
	}
	if len(assistantMsgs) == 0 {
		// Empty-branch case: no assistant turns yet.
		return ReintegrationProposal{ProposedSummary: ""}, nil
	}
	tail := assistantMsgs
	if len(tail) > 8 {
		tail = tail[len(tail)-8:]
	}
	var b strings.Builder
	for i, t := range tail {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(t)
	}
	summary := b.String()

	// Rough truncation: 4 runes ≈ 1 token; cap at the persisted
	// BranchReintegrationMaxTokens (engineer-truth-pass-01PMTP01 WP02,
	// finding B2 — this used to hardcode 2000 regardless of what the
	// user had saved; EffectiveBranchReintegrationMaxTokens's zero-
	// fallback already resolves to the same 2000 default, so an unset
	// or nil-Settings caller sees no behaviour change).
	const runesPerToken = 4
	maxTokens := settings.DefaultBranchReintegrationMaxTokens
	if a.cfg.Settings != nil {
		maxTokens = a.cfg.Settings().EffectiveBranchReintegrationMaxTokens()
	}
	maxRunes := maxTokens * runesPerToken
	if utf8.RuneCountInString(summary) > maxRunes {
		runes := []rune(summary)
		summary = string(runes[:maxRunes])
	}

	tokenCount := utf8.RuneCountInString(summary) / runesPerToken
	if tokenCount < 1 && summary != "" {
		tokenCount = 1
	}
	return ReintegrationProposal{
		ProposedSummary: summary,
		TokenCount:      tokenCount,
		Model:           "rule_based",
	}, nil
}

// CommitReintegration inserts the final summary as a synthetic system
// message on the parent session and flips the branch to merged
// (WP07 / FR-008b).
func (a *API) CommitReintegration(ctx context.Context, opts CommitReintegrationOptions) error {
	if a == nil || a.cfg.Conversations == nil {
		return ErrManagerUnavailable
	}
	if opts.BranchSessionID == "" {
		return ErrInvalidArg
	}
	summary := strings.TrimSpace(opts.FinalSummaryText)
	if summary == "" {
		return fmt.Errorf("branches: commit reintegration: summary must not be empty")
	}

	// Look up the branch row by child session id.
	branches, err := a.cfg.Conversations.ListByChild(ctx, opts.BranchSessionID)
	if err != nil {
		return fmt.Errorf("branches: lookup by child: %w", err)
	}
	if len(branches) == 0 {
		return fmt.Errorf("branches: no branch row found for session %q", opts.BranchSessionID)
	}
	br := branches[0]

	// Append synthetic system message to parent session.
	if a.cfg.Sessions != nil {
		header := "Subagent branch summary"
		if br.Title != "" {
			header += " (" + br.Title + ")"
		}
		fullMsg := header + ":\n" + summary
		if _, err := a.cfg.Sessions.AppendMessage(ctx, br.ParentSessionID, session.Message{
			Role:    session.RoleSystem,
			Content: fullMsg,
		}); err != nil {
			return fmt.Errorf("branches: append parent summary: %w", err)
		}
	}

	// Flip branch to merged.
	if err := a.cfg.Conversations.MarkMerged(ctx, br.ID); err != nil {
		return fmt.Errorf("branches: mark merged: %w", err)
	}

	// Emit audit event.
	tokenCount := utf8.RuneCountInString(summary) / 4
	audit.MustEmit(ctx, a.cfg.Audit, audit.KindBranchReintegrated,
		audit.BranchReintegratedPayload{
			ParentSessionID:   br.ParentSessionID,
			BranchSessionID:   opts.BranchSessionID,
			SummaryTokenCount: tokenCount,
			WasEdited:         opts.WasEdited,
		}, a.now())

	return nil
}

// SetAdvisorDismissed persists the per-session "don't suggest again"
// flag (FR-010 / WP04).
func (a *API) SetAdvisorDismissed(ctx context.Context, sessionID string, dismissed bool) error {
	if a == nil || a.cfg.Sessions == nil {
		return ErrManagerUnavailable
	}
	if sessionID == "" {
		return ErrInvalidArg
	}
	if err := a.cfg.Sessions.SetBranchAdvisorDismissed(ctx, sessionID, dismissed); err != nil {
		return fmt.Errorf("branches: set advisor dismissed: %w", err)
	}
	// Emit audit event for explicit session-scoped dismissal.
	audit.MustEmit(ctx, a.cfg.Audit, audit.KindBranchAdvisorDismissed,
		audit.BranchAdvisorDismissedPayload{
			Scope:  "session",
			Reason: "dont_suggest_again",
		}, a.now())
	return nil
}

// toWire projects a conversation.Branch onto the Branch wire shape.
func toWire(b conversation.Branch) Branch {
	return Branch{
		ID:               b.ID,
		ParentSessionID:  b.ParentSessionID,
		ChildSessionID:   b.ChildSessionID,
		Kind:             string(b.Kind),
		Status:           string(b.Status),
		ProviderID:       b.ProviderID,
		ModelID:          b.ModelID,
		Title:            b.Title,
		TaskHint:         b.TaskHint,
		CreatedAt:        fmtTime(b.CreatedAt),
		UpdatedAt:        fmtTime(b.UpdatedAt),
		MergedAt:         fmtTimePtr(b.MergedAt),
		AbandonedAt:      fmtTimePtr(b.AbandonedAt),
		SubagentBranch:   b.SubagentBranch,
		RecommendationID: b.RecommendationID,
		AdvisorSignals:   b.AdvisorSignals,
	}
}

// Compile-time witness.
var _ BranchesAPI = (*API)(nil)
