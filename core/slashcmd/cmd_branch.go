package slashcmd

import (
	"context"
	"fmt"
	"strings"
)

// branchCommand implements /branch [model-id]. With no argument it
// lists the recommended smaller / larger models for the parent session
// (a no-op probe — no branch is created). With a model id it forks the
// conversation onto a new child session pinned to the supplied model.
type branchCommand struct{}

func (branchCommand) Name() string { return "branch" }
func (branchCommand) Description() string {
	return "Branch the conversation onto a smaller or larger model — /branch lists candidates; /branch <model-id> forks"
}
func (branchCommand) Hidden() bool     { return false }
func (branchCommand) ComingSoon() bool { return false }

func (branchCommand) Run(ctx context.Context, env Env, args []string) (Result, error) {
	if env.Branches == nil {
		return Result{
			Kind: ResultKindError,
			Text: "/branch: branching subsystem not wired",
		}, nil
	}
	if env.SessionID == "" {
		return Result{
			Kind: ResultKindError,
			Text: "/branch: no active session",
		}, nil
	}
	if len(args) == 0 {
		return branchListing(ctx, env)
	}
	if len(args) > 1 {
		return Result{
			Kind: ResultKindError,
			Text: fmt.Sprintf("/branch: too many arguments (%d) — usage: /branch [model-id]", len(args)),
		}, nil
	}
	modelID := strings.TrimSpace(args[0])
	if modelID == "" {
		return branchListing(ctx, env)
	}
	handle, err := env.Branches.CreateBranch(ctx, env.SessionID, modelID)
	if err != nil {
		return Result{
			Kind: ResultKindError,
			Text: "/branch: " + err.Error(),
		}, err
	}
	meta := map[string]any{
		"branchId":       handle.BranchID,
		"childSessionId": handle.ChildSessionID,
	}
	if handle.ProviderID != "" {
		meta[MetaKeyProviderID] = handle.ProviderID
	}
	if handle.ModelID != "" {
		meta[MetaKeyModelID] = handle.ModelID
	}
	display := handle.ModelID
	if display == "" {
		display = modelID
	}
	return Result{
		Kind:     ResultKindInfo,
		Text:     fmt.Sprintf("branched onto %s — child session %s (branch %s)", display, handle.ChildSessionID, handle.BranchID),
		Metadata: meta,
	}, nil
}

// branchListing renders the smaller / larger / same-tier recommendations
// without creating a branch. Used when /branch is invoked with no args.
func branchListing(ctx context.Context, env Env) (Result, error) {
	recs, err := env.Branches.RecommendModels(ctx, env.SessionID)
	if err != nil {
		return Result{
			Kind: ResultKindError,
			Text: "/branch: " + err.Error(),
		}, err
	}
	rows := []struct {
		label string
		m     BranchModel
	}{
		{"smaller", recs.Smaller},
		{"same", recs.Same},
		{"larger", recs.Larger},
	}
	var b strings.Builder
	b.WriteString("Branch candidates (run /branch <model-id> to fork):\n")
	any := false
	for _, r := range rows {
		if r.m.IsZero() {
			continue
		}
		any = true
		fmt.Fprintf(&b, "  %s: %s", r.label, r.m.ModelID)
		if r.m.ProviderID != "" {
			fmt.Fprintf(&b, " (%s)", r.m.ProviderID)
		}
		if r.m.Reason != "" {
			fmt.Fprintf(&b, " — %s", r.m.Reason)
		}
		b.WriteString("\n")
	}
	if !any {
		return Result{
			Kind: ResultKindInfo,
			Text: "/branch: no recommended models available — pass /branch <model-id> to fork explicitly",
		}, nil
	}
	return Result{
		Kind: ResultKindInfo,
		Text: strings.TrimRight(b.String(), "\n"),
	}, nil
}
