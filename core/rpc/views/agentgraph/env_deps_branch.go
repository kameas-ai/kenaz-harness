package agentgraph

import (
	"context"
	"fmt"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	coreconv "github.com/kameas-ai/kenaz-harness/core/conversation"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

// BranchSeamAdapter wraps core/conversation.Manager + session.Manager
// onto the agentgraph.BranchSeam interface. The kernel's ForkNode and
// MergeNode call into this for child-session lifecycle; production
// wiring binds the real managers here, tests bind the in-memory
// FakeBranchSeam.
//
// IMPORTANT: this adapter does NOT spawn a child kernel run by itself.
// The kernel's ForkNode contract is "create the child session and the
// branch row"; the parent kernel resumes after Fork. A future v2 will
// thread a child run-spawner through this seam — captured as a hook
// (RunSpawner) but unwired for v1.
type BranchSeamAdapter struct {
	conversations *coreconv.Manager
	sessions      *session.Manager
}

// NewBranchSeamAdapter constructs the adapter. Both managers may be nil
// — the resulting seam returns ErrNoBranchSeam from every method, which
// matches the kernel's nilBranchSeam stub. The defensive nil-check
// keeps the chassis bootable on the test path (rpc.New(nil)).
func NewBranchSeamAdapter(conversations *coreconv.Manager, sessions *session.Manager) *BranchSeamAdapter {
	return &BranchSeamAdapter{conversations: conversations, sessions: sessions}
}

// available reports whether the adapter has been constructed with the
// minimal dependencies it needs to do real work.
func (a *BranchSeamAdapter) available() bool {
	return a != nil && a.conversations != nil && a.sessions != nil
}

// Fork allocates a new child session and persists a branch row.
// Writes the handoff prompt as the child's first user message. The
// kernel emits the branch_fork event on its side; this adapter only
// owns the storage transition.
func (a *BranchSeamAdapter) Fork(ctx context.Context, req coreag.ForkRequest) (coreag.BranchHandle, error) {
	if !a.available() {
		return coreag.BranchHandle{}, coreag.ErrNoBranchSeam
	}
	if req.ParentSessionID == "" {
		return coreag.BranchHandle{}, fmt.Errorf("branch_seam: parent_session_id required")
	}
	br, child, err := a.conversations.CreateBranch(ctx, coreconv.ForkOptions{
		ParentSessionID: req.ParentSessionID,
		Title:           req.Title,
		TaskHint:        req.TaskHint,
		ProviderID:      req.ProviderID,
		ModelID:         req.ModelID,
		// The BranchSeamAdapter is the implicit edit-and-resend path.
		// Mark the branch row so audit consumers can distinguish it from
		// explicit "Branch from this turn" forks.
		// (branching-ux-polish-01KQ8TD7 WP01)
		CreationPath: "edit_resend",
	})
	if err != nil {
		return coreag.BranchHandle{}, fmt.Errorf("branch_seam: create branch: %w", err)
	}
	// Seed the child session with the handoff prompt as a user message
	// so the child kernel run sees it on its first turn.
	if req.HandoffPrompt != "" {
		if _, perr := a.sessions.AppendMessage(ctx, child.ID, session.Message{
			Role:    session.RoleUser,
			Content: req.HandoffPrompt,
		}); perr != nil {
			return coreag.BranchHandle{}, fmt.Errorf("branch_seam: seed handoff: %w", perr)
		}
	}
	// Write CoW reference rows for each parent message id so the child
	// can pick them up later without copying their content.
	for _, msgID := range req.ParentMessageIDs {
		if msgID == "" {
			continue
		}
		_ = a.conversations.AppendMessageRef(ctx, br.ID, msgID)
	}
	return coreag.BranchHandle{BranchID: br.ID, ChildSessionID: child.ID}, nil
}

// PullChildTail returns the most-recent N messages on the branch's
// child session. MergeNode feeds these into compaction.
func (a *BranchSeamAdapter) PullChildTail(ctx context.Context, branchID string, n int) ([]coreag.Message, error) {
	if !a.available() {
		return nil, coreag.ErrNoBranchSeam
	}
	br, err := a.conversations.Get(ctx, branchID)
	if err != nil {
		return nil, fmt.Errorf("branch_seam: get branch %q: %w", branchID, err)
	}
	msgs, err := a.sessions.ListMessages(ctx, br.ChildSessionID)
	if err != nil {
		return nil, fmt.Errorf("branch_seam: list messages: %w", err)
	}
	if n > 0 && len(msgs) > n {
		msgs = msgs[len(msgs)-n:]
	}
	out := make([]coreag.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, coreag.Message{
			Role:    string(m.Role),
			Content: m.Content,
		})
	}
	return out, nil
}

// WaitForChildRun is a no-op in v1: child kernel runs are not spawned
// from this seam yet. Returns nil so the merge path proceeds with
// whatever messages are already on the child session.
func (a *BranchSeamAdapter) WaitForChildRun(_ context.Context, _ string) error {
	return nil
}

// AppendToParent appends a message to the parent session of the
// branch. role is usually "system" so the rail UI can mark the merge
// summary as a system note.
func (a *BranchSeamAdapter) AppendToParent(ctx context.Context, branchID, role, content string) (string, error) {
	if !a.available() {
		return "", coreag.ErrNoBranchSeam
	}
	br, err := a.conversations.Get(ctx, branchID)
	if err != nil {
		return "", fmt.Errorf("branch_seam: get branch %q: %w", branchID, err)
	}
	r := session.Role(role)
	if r == "" {
		r = session.RoleSystem
	}
	stored, err := a.sessions.AppendMessage(ctx, br.ParentSessionID, session.Message{
		Role:    r,
		Content: content,
	})
	if err != nil {
		return "", fmt.Errorf("branch_seam: append parent: %w", err)
	}
	return stored.ID, nil
}

// MarkMerged flips the branch row's status to merged.
func (a *BranchSeamAdapter) MarkMerged(ctx context.Context, branchID string) error {
	if !a.available() {
		return coreag.ErrNoBranchSeam
	}
	return a.conversations.MarkMerged(ctx, branchID)
}

// ActiveBranchForChildSession reports the branch whose child session is
// sessionID, when that branch is still BranchStatusActive. A session
// with no owning branch (a parent session, or any other ordinary
// session), or a branch already merging/merged/abandoned, reports ok
// false — the chat runner's merge-suggestion trigger
// (engineer-truth-pass-01PMTP01 WP08) uses this to skip every session
// that isn't a live branch child before it evaluates the heuristic.
func (a *BranchSeamAdapter) ActiveBranchForChildSession(ctx context.Context, sessionID string) (string, bool) {
	if !a.available() || sessionID == "" {
		return "", false
	}
	branches, err := a.conversations.ListByChild(ctx, sessionID)
	if err != nil {
		return "", false
	}
	for _, br := range branches {
		if br.Status == coreconv.BranchStatusActive {
			return br.ID, true
		}
	}
	return "", false
}

// Compile-time witness.
var _ coreag.BranchSeam = (*BranchSeamAdapter)(nil)
