package agentgraph

import (
	"context"
	"errors"
	"sync"
	"time"
)

// BranchSeam is the agentgraph-side interface onto the conversation
// manager + a child-run spawner. The production wiring (in core/rpc /
// core/main) binds this against core/conversation.Manager + a kernel
// instance that can spawn child runs; tests bind a fake.
//
// Keeping the interface here (instead of in core/conversation) keeps
// the import direction clean: agentgraph never depends on conversation.
type BranchSeam interface {
	// Fork takes the parent context (already-compacted handoff prompt),
	// allocates a new child session, persists a Branch row, writes the
	// handoff prompt as the child's first user message, and starts a
	// background child kernel run if a Run callback was wired.
	//
	// Returns the new branch's ID + the new child session's ID; the
	// caller (ForkNode executor) emits the branch_fork event with both.
	Fork(ctx context.Context, req ForkRequest) (BranchHandle, error)

	// PullChildTail returns the most-recent N messages on a child
	// session — used by MergeNode to feed compaction.
	PullChildTail(ctx context.Context, branchID string, n int) ([]Message, error)

	// WaitForChildRun blocks until the child branch's kernel run
	// completes (or until ctx cancels). Returns immediately when no
	// run was spawned (manual-merge case).
	WaitForChildRun(ctx context.Context, branchID string) error

	// AppendToParent appends a message to the parent session. role is
	// usually "system" so the rail UI can mark the merge summary as a
	// system note. Returns the persisted message id.
	AppendToParent(ctx context.Context, branchID, role, content string) (string, error)

	// MarkMerged flips the branch row's status to merged.
	MarkMerged(ctx context.Context, branchID string) error

	// ActiveBranchForChildSession reports whether sessionID is the
	// live child session of a branch that is still active — not yet
	// merged, abandoned, or mid-merge. ok is false for parent sessions
	// and for branches past that lifecycle point.
	//
	// Consumed by the chat runner's post-run merge-suggestion trigger
	// (engineer-truth-pass-01PMTP01 WP08, finding B16): a branch's
	// child session is chatted through the ordinary ChatRunner path,
	// so "the child run completed" is observable there as an ordinary
	// clean StartStream exit. This lookup is how that completion hook
	// tells an interactive branch turn apart from a turn on any other
	// session, without agentgraph importing core/conversation.
	ActiveBranchForChildSession(ctx context.Context, sessionID string) (branchID string, ok bool)
}

// ForkRequest is the input to BranchSeam.Fork. The agentgraph layer
// builds this from the BranchAttrs payload + recent parent history; the
// seam owns the storage details.
type ForkRequest struct {
	ParentSessionID    string
	Title              string
	TaskHint           string
	HandoffPrompt      string
	ProviderID         string
	ModelID            string
	ToolAllowlist      []string
	CrossProviderWarn  bool
	ParentMessageIDs   []string // CoW reference seeds (FR-038)
}

// BranchHandle is what BranchSeam.Fork returns. Carries the IDs both
// sides need (the kernel for events; the merge node to pick up the
// child run).
type BranchHandle struct {
	BranchID       string
	ChildSessionID string
}

// ErrNoBranchSeam is returned when a ForkNode/MergeNode fires without
// a BranchSeam wired. v1 surfaces this as a friendly error in the chat
// surface so the user sees "branching not enabled in this build".
var ErrNoBranchSeam = errors.New("agentgraph: no branch seam configured")

// nilBranchSeam is the default for builds that haven't wired the
// branching subsystem (e.g. tests that don't exercise Fork/Merge).
type nilBranchSeam struct{}

func (nilBranchSeam) Fork(_ context.Context, _ ForkRequest) (BranchHandle, error) {
	return BranchHandle{}, ErrNoBranchSeam
}
func (nilBranchSeam) PullChildTail(_ context.Context, _ string, _ int) ([]Message, error) {
	return nil, ErrNoBranchSeam
}
func (nilBranchSeam) WaitForChildRun(_ context.Context, _ string) error {
	return ErrNoBranchSeam
}
func (nilBranchSeam) AppendToParent(_ context.Context, _, _, _ string) (string, error) {
	return "", ErrNoBranchSeam
}
func (nilBranchSeam) MarkMerged(_ context.Context, _ string) error {
	return ErrNoBranchSeam
}
func (nilBranchSeam) ActiveBranchForChildSession(_ context.Context, _ string) (string, bool) {
	return "", false
}

// FakeBranchSeam is an in-memory BranchSeam suitable for tests. It
// records every Fork / Merge call; tests assert on the recorded
// invocations.
type FakeBranchSeam struct {
	mu sync.Mutex
	// Forks records each Fork invocation in order.
	Forks []ForkRequest
	// ChildSessionPrefix is prepended to the synthetic child session id.
	ChildSessionPrefix string
	// ParentMessages records every AppendToParent call.
	ParentMessages []FakeAppend
	// Merged records the branch ids passed to MarkMerged.
	Merged []string
	// ChildTails maps branchID → the canned tail returned by PullChildTail.
	ChildTails map[string][]Message
	// CompleteAfter is used by WaitForChildRun. When non-zero the seam
	// blocks for that duration; tests use a small duration.
	CompleteAfter time.Duration
	// ChildToBranch maps a child session id to its branch id, populated
	// by Fork. Backs ActiveBranchForChildSession — a branch id is
	// "active" here as long as it hasn't also been recorded in Merged.
	ChildToBranch map[string]string
}

// FakeAppend is one recorded AppendToParent call.
type FakeAppend struct {
	BranchID string
	Role     string
	Content  string
}

// NewFakeBranchSeam constructs an empty seam.
func NewFakeBranchSeam() *FakeBranchSeam {
	return &FakeBranchSeam{
		ChildSessionPrefix: "child-",
		ChildTails:         map[string][]Message{},
		ChildToBranch:      map[string]string{},
	}
}

// Fork records the request and returns a synthetic handle.
func (f *FakeBranchSeam) Fork(_ context.Context, req ForkRequest) (BranchHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Forks = append(f.Forks, req)
	idx := len(f.Forks)
	branchID := "br-fake-" + nopadInt(idx)
	childID := f.ChildSessionPrefix + nopadInt(idx)
	if f.ChildToBranch == nil {
		f.ChildToBranch = map[string]string{}
	}
	f.ChildToBranch[childID] = branchID
	return BranchHandle{BranchID: branchID, ChildSessionID: childID}, nil
}

// PullChildTail returns the canned tail for a branch.
func (f *FakeBranchSeam) PullChildTail(_ context.Context, branchID string, n int) ([]Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := f.ChildTails[branchID]
	if n > 0 && len(rows) > n {
		rows = rows[len(rows)-n:]
	}
	out := append([]Message(nil), rows...)
	return out, nil
}

// WaitForChildRun blocks for CompleteAfter or returns immediately.
func (f *FakeBranchSeam) WaitForChildRun(ctx context.Context, _ string) error {
	if f.CompleteAfter <= 0 {
		return nil
	}
	select {
	case <-time.After(f.CompleteAfter):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// AppendToParent records the call.
func (f *FakeBranchSeam) AppendToParent(_ context.Context, branchID, role, content string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ParentMessages = append(f.ParentMessages, FakeAppend{BranchID: branchID, Role: role, Content: content})
	return "msg-" + nopadInt(len(f.ParentMessages)), nil
}

// MarkMerged records the branch id.
func (f *FakeBranchSeam) MarkMerged(_ context.Context, branchID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Merged = append(f.Merged, branchID)
	return nil
}

// ActiveBranchForChildSession looks up the branch id recorded for
// sessionID at Fork time and reports it as active unless that branch
// id has since appeared in Merged.
func (f *FakeBranchSeam) ActiveBranchForChildSession(_ context.Context, sessionID string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	branchID, ok := f.ChildToBranch[sessionID]
	if !ok {
		return "", false
	}
	for _, merged := range f.Merged {
		if merged == branchID {
			return "", false
		}
	}
	return branchID, true
}

// nopadInt formats an int without strconv import (small util to avoid
// reaching into fmt for hot paths).
func nopadInt(v int) string {
	if v == 0 {
		return "0"
	}
	out := make([]byte, 0, 4)
	if v < 0 {
		out = append(out, '-')
		v = -v
	}
	digits := make([]byte, 0, 4)
	for v > 0 {
		digits = append(digits, byte('0'+v%10))
		v /= 10
	}
	for i := len(digits) - 1; i >= 0; i-- {
		out = append(out, digits[i])
	}
	return string(out)
}
