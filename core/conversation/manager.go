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
	// CreationPath discriminates how the branch was created:
	// "edit_resend" | "explicit" | "unknown". Defaults to "unknown".
	// (branching-ux-polish-01KQ8TD7 WP01)
	CreationPath string
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
	creationPath := opts.CreationPath
	if creationPath == "" {
		creationPath = "unknown"
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
		CreationPath:     creationPath,
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

// ErrCycle is returned by CreateBranchAtMessage when the proposed parent
// is a descendant of the new child — cycles cannot form through the UI
// but this is a cheap defensive guard.
var ErrCycle = fmt.Errorf("conversation: branch cycle detected")

// ForkAtMessageOptions configures CreateBranchAtMessage.
type ForkAtMessageOptions struct {
	// ParentSessionID is required.
	ParentSessionID string
	// ParentMessageID is the anchor message. Messages [0..ParentMessageID]
	// (inclusive) are copied into the child session.
	ParentMessageID string
	// Title is an optional display override; stored as BranchTitle.
	Title string
	// ChildName overrides the auto-generated "<parent> (branch)" name.
	ChildName string
	// ProviderID + ModelID for the branch row.
	ProviderID string
	ModelID    string
}

// CreateBranchAtMessage allocates a child session whose message history
// is exactly the parent's messages [0..ParentMessageID] (inclusive).
// Returns the populated Branch + the child session.Record.
//
// Requires the Manager to have been constructed with a non-nil session.Manager.
func (m *Manager) CreateBranchAtMessage(ctx context.Context, opts ForkAtMessageOptions) (Branch, session.Record, error) {
	if m.sessions == nil {
		return Branch{}, session.Record{}, fmt.Errorf("conversation: CreateBranchAtMessage requires session.Manager")
	}
	if opts.ParentSessionID == "" || opts.ParentMessageID == "" {
		return Branch{}, session.Record{}, ErrInvalidArg
	}

	// Load parent.
	parent, err := m.sessions.Get(ctx, opts.ParentSessionID)
	if err != nil {
		return Branch{}, session.Record{}, fmt.Errorf("conversation: fetch parent session: %w", err)
	}

	// Collect all parent messages and find the anchor index.
	msgs, err := m.sessions.ListMessages(ctx, opts.ParentSessionID)
	if err != nil {
		return Branch{}, session.Record{}, fmt.Errorf("conversation: list parent messages: %w", err)
	}
	anchorIdx := -1
	for i, msg := range msgs {
		if msg.ID == opts.ParentMessageID {
			anchorIdx = i
			break
		}
	}
	if anchorIdx < 0 {
		return Branch{}, session.Record{}, fmt.Errorf("conversation: parentMessageID %q not found in parent session", opts.ParentMessageID)
	}
	slice := msgs[:anchorIdx+1]

	// Cycle detection: ensure the parent session is not a descendant of
	// the (not-yet-created) child. Walk the ancestor chain upward from
	// the parent and confirm there is no loop back to parentSessionID.
	// Since the child doesn't exist yet we only need to verify the parent
	// chain doesn't cycle back on itself.
	if err := m.detectCycle(ctx, opts.ParentSessionID, 0); err != nil {
		return Branch{}, session.Record{}, err
	}

	// Allocate child session.
	childName := strings.TrimSpace(opts.ChildName)
	if childName == "" {
		childName = parent.Name + " (branch)"
	}
	child, err := m.sessions.CreateInProject(ctx, childName, parent.ProjectID)
	if err != nil {
		return Branch{}, session.Record{}, fmt.Errorf("conversation: create child session: %w", err)
	}

	// Replay messages into child.
	for _, msg := range slice {
		if _, err := m.sessions.AppendMessage(ctx, child.ID, session.Message{
			Role:    msg.Role,
			Content: msg.Content,
		}); err != nil {
			return Branch{}, session.Record{}, fmt.Errorf("conversation: replay message: %w", err)
		}
	}

	// Create branch row.
	id, err := m.idGen()
	if err != nil {
		return Branch{}, session.Record{}, fmt.Errorf("conversation: id gen: %w", err)
	}
	now := m.now()
	br := Branch{
		ID:                 id,
		ParentSessionID:    opts.ParentSessionID,
		ChildSessionID:     child.ID,
		Kind:               BranchKindFork,
		Status:             BranchStatusActive,
		ProviderID:         opts.ProviderID,
		ModelID:            opts.ModelID,
		Title:              opts.Title,
		ParentMessageID:    opts.ParentMessageID,
		BranchTitle:        opts.Title,
		CreationPath:       "explicit",
		ParentSessionTitle: parent.Name,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := m.store.Create(ctx, br); err != nil {
		return Branch{}, session.Record{}, fmt.Errorf("conversation: persist branch: %w", err)
	}
	return br, child, nil
}

// ListBranchesByProject returns every branch row whose parent session
// belongs to the given project. It does so by listing all branches for
// all sessions in the project. This is O(sessions_in_project) — suitable
// for the sidebar tree which only calls it once per navigation.
func (m *Manager) ListBranchesByProject(ctx context.Context, projectID string) ([]Branch, error) {
	if m.sessions == nil {
		return nil, fmt.Errorf("conversation: ListBranchesByProject requires session.Manager")
	}
	sessions, err := m.sessions.ListByProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("conversation: list sessions for project: %w", err)
	}
	var out []Branch
	for _, sess := range sessions {
		branches, err := m.store.ListByParent(ctx, sess.ID)
		if err != nil {
			return nil, fmt.Errorf("conversation: list branches for session %q: %w", sess.ID, err)
		}
		out = append(out, branches...)
	}
	return out, nil
}

// DeleteChildrenOf recursively deletes all descendant sessions of
// parentSessionID in depth-first order (leaves first). This is the
// cascade-delete implementation for Settings.DeleteBranchesWithParent=true
// (branching-ux-polish-01KQ8TD7 WP06).
//
// Each child session is deleted via session.Manager.Delete; the branch
// row is removed by the ON DELETE CASCADE FK that already exists in
// migration 0306. Returns the first error encountered; partial deletion
// may occur.
func (m *Manager) DeleteChildrenOf(ctx context.Context, parentSessionID string) error {
	if m.sessions == nil {
		return fmt.Errorf("conversation: DeleteChildrenOf requires session.Manager")
	}
	branches, err := m.store.ListByParent(ctx, parentSessionID)
	if err != nil {
		return fmt.Errorf("conversation: list branches of %q: %w", parentSessionID, err)
	}
	for _, br := range branches {
		// Recurse depth-first so grandchildren are deleted before their
		// parent child sessions, avoiding FK constraint violations.
		if err := m.DeleteChildrenOf(ctx, br.ChildSessionID); err != nil {
			return err
		}
		if err := m.sessions.Delete(ctx, br.ChildSessionID); err != nil {
			return fmt.Errorf("conversation: delete child session %q: %w", br.ChildSessionID, err)
		}
	}
	return nil
}

// detectCycle walks up the branch ancestor chain from sessionID and
// returns ErrCycle if depth exceeds 32 (a cap that catches any practical
// cycle without infinite looping). This is a belt-and-braces guard;
// cycles cannot form via normal UI paths.
func (m *Manager) detectCycle(ctx context.Context, sessionID string, depth int) error {
	const maxDepth = 32
	if depth > maxDepth {
		return ErrCycle
	}
	branches, err := m.store.ListByChild(ctx, sessionID)
	if err != nil || len(branches) == 0 {
		return nil
	}
	return m.detectCycle(ctx, branches[0].ParentSessionID, depth+1)
}

// defaultIDGen returns a 16-byte hex id (32 chars).
func defaultIDGen() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
