package conversation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/session"
)

// IDGen is the branch-id generator. Tests override; production uses
// 16-byte hex.
type IDGen func() (string, error)

// Manager is the public facade for branch lifecycle operations. It
// owns the bridge between the Store (branches table) and session.Manager
// (the child sessions a branch points at), so callers don't have to
// keep both in sync by hand.
//
// IMPORTANT: this Manager doesn't own session creation directly — it
// asks session.Manager.Create for the child session and records the
// branch row referencing the returned id. If the session-side write
// fails, no branch row is persisted; if the branch row fails, the
// child session is left orphaned (a follow-up Abandon cleans it up).
type Manager struct {
	store    Store
	sessions *session.Manager
	now      func() time.Time
	idGen    IDGen
}

// Option tunes a Manager.
type Option func(*Manager)

// WithClock overrides the wall-clock source.
func WithClock(now func() time.Time) Option {
	return func(m *Manager) {
		if now != nil {
			m.now = now
		}
	}
}

// WithIDGen overrides the branch id generator.
func WithIDGen(g IDGen) Option {
	return func(m *Manager) {
		if g != nil {
			m.idGen = g
		}
	}
}

// NewManager constructs a Manager. store is required; sessions can be
// nil — tests that exercise the storage path alone can pass nil and
// only call CreateRaw / List / Mark*.
func NewManager(store Store, sessions *session.Manager, opts ...Option) *Manager {
	if store == nil {
		panic("conversation.NewManager: nil store")
	}
	m := &Manager{
		store:    store,
		sessions: sessions,
		now:      func() time.Time { return time.Now().UTC() },
		idGen:    defaultIDGen,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// ForkOptions configures a CreateBranch call.
type ForkOptions struct {
	// ParentSessionID is required.
	ParentSessionID string
	// ChildName is the new session's display name. Defaults to
	// "<parent name> (branch)" when empty.
	ChildName string
	// Title is a short user-facing branch title.
	Title string
	// TaskHint is a free-text user description; the recommendation
	// heuristic uses it.
	TaskHint string
	// ProviderID + ModelID record the recommended/chosen model.
	ProviderID string
	ModelID    string
	// Kind defaults to BranchKindFork.
	Kind BranchKind
	// Subagent-enriched fields (branch-as-subagent-recommendation WP04).
	// When SubagentBranch is true, the branch row is flagged as a
	// subagent branch in the DB.
	SubagentBranch   bool
	RecommendationID string
	AdvisorSignals   []string
}

// CreateBranch allocates a new child session via session.Manager, then
// records a branches row pointing at it. Returns the populated Branch
// + the child session record. Callers (Bundle B kernel) write the
// compacted handoff prompt as the first message on the returned child
// session.
//
// Requires the Manager to have been constructed with a non-nil
// session.Manager. CreateRaw is the alternate entry point for callers
// that want to allocate the child session themselves.
func (m *Manager) CreateBranch(ctx context.Context, opts ForkOptions) (Branch, session.Record, error) {
	if m.sessions == nil {
		return Branch{}, session.Record{}, fmt.Errorf("conversation: CreateBranch requires session.Manager")
	}
	if opts.ParentSessionID == "" {
		return Branch{}, session.Record{}, ErrInvalidArg
	}
	parent, err := m.sessions.Get(ctx, opts.ParentSessionID)
	if err != nil {
		return Branch{}, session.Record{}, fmt.Errorf("conversation: fetch parent session: %w", err)
	}
	childName := strings.TrimSpace(opts.ChildName)
	if childName == "" {
		childName = parent.Name + " (branch)"
	}
	child, err := m.sessions.CreateInProject(ctx, childName, parent.ProjectID)
	if err != nil {
		return Branch{}, session.Record{}, fmt.Errorf("conversation: create child session: %w", err)
	}

	id, err := m.idGen()
	if err != nil {
		return Branch{}, session.Record{}, fmt.Errorf("conversation: id gen: %w", err)
	}
	now := m.now()
	kind := opts.Kind
	if kind == "" {
		kind = BranchKindFork
	}
	br := Branch{
		ID:               id,
		ParentSessionID:  opts.ParentSessionID,
		ChildSessionID:   child.ID,
		Kind:             kind,
		Status:           BranchStatusActive,
		ProviderID:       opts.ProviderID,
		ModelID:          opts.ModelID,
		Title:            opts.Title,
		TaskHint:         opts.TaskHint,
		SubagentBranch:   opts.SubagentBranch,
		RecommendationID: opts.RecommendationID,
		AdvisorSignals:   opts.AdvisorSignals,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := m.store.Create(ctx, br); err != nil {
		return Branch{}, session.Record{}, fmt.Errorf("conversation: persist branch: %w", err)
	}
	return br, child, nil
}

// CreateRaw stores a branch row directly. Callers MUST have already
// allocated the child session id through some other mechanism. Used by
// integration tests + internal callers (the kernel's ForkNode supplies
// its own child session via the Fork executor's seam).
func (m *Manager) CreateRaw(ctx context.Context, b Branch) error {
	if b.CreatedAt.IsZero() {
		b.CreatedAt = m.now()
	}
	if b.UpdatedAt.IsZero() {
		b.UpdatedAt = b.CreatedAt
	}
	return m.store.Create(ctx, b)
}

// Get returns one branch by id.
func (m *Manager) Get(ctx context.Context, id string) (Branch, error) {
	return m.store.Get(ctx, id)
}

// ListByParent returns every branch off a parent session, oldest first.
func (m *Manager) ListByParent(ctx context.Context, parentSessionID string) ([]Branch, error) {
	return m.store.ListByParent(ctx, parentSessionID)
}

// ListByChild returns every branch row where the given session is the
// child. v1 always returns 0 or 1 row.
func (m *Manager) ListByChild(ctx context.Context, childSessionID string) ([]Branch, error) {
	return m.store.ListByChild(ctx, childSessionID)
}

// MarkMerging flips the branch into a merging state (compaction in
// progress). Callers transition to MarkMerged when the summary message
// has landed on the parent session.
func (m *Manager) MarkMerging(ctx context.Context, id string) error {
	return m.store.UpdateStatus(ctx, id, BranchStatusMerging, m.now())
}

// MarkMerged flips the branch into the merged terminal state.
func (m *Manager) MarkMerged(ctx context.Context, id string) error {
	return m.store.MarkMerged(ctx, id, m.now())
}

// MarkAbandoned flips the branch into the abandoned terminal state.
// The child session is preserved; the rail UI greys it out.
func (m *Manager) MarkAbandoned(ctx context.Context, id string) error {
	return m.store.MarkAbandoned(ctx, id, m.now())
}

// AppendMessageRef writes one row of the copy-on-write reference
// table. Called by the kernel's ForkNode at fork time to record which
// parent messages the child branch initially aliases.
func (m *Manager) AppendMessageRef(ctx context.Context, branchID, parentMsgID string) error {
	return m.store.AppendMessageRef(ctx, MessageRef{
		BranchID:    branchID,
		ParentMsgID: parentMsgID,
	})
}

// ListMessageRefs returns every CoW reference row for a branch.
func (m *Manager) ListMessageRefs(ctx context.Context, branchID string) ([]MessageRef, error) {
	return m.store.ListMessageRefs(ctx, branchID)
}

// Sessions exposes the underlying session.Manager so callers (the
// kernel's ForkNode/MergeNode) can append messages directly. May be
// nil when the Manager was constructed without one.
func (m *Manager) Sessions() *session.Manager { return m.sessions }

// defaultIDGen returns a 16-byte hex id (32 chars).
func defaultIDGen() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
