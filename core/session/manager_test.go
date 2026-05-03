package session

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordingAudit captures every audit emit for assertion. Safe for
// concurrent writes since the manager may schedule emits from
// different goroutines.
type recordingAudit struct {
	mu     sync.Mutex
	events []recordedEvent
}

type recordedEvent struct {
	Kind    string
	Payload map[string]any
}

func (a *recordingAudit) Emit(_ context.Context, kind string, payload map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cp := make(map[string]any, len(payload))
	for k, v := range payload {
		cp[k] = v
	}
	a.events = append(a.events, recordedEvent{Kind: kind, Payload: cp})
}

func (a *recordingAudit) Snapshot() []recordedEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]recordedEvent, len(a.events))
	copy(out, a.events)
	return out
}

func (a *recordingAudit) Kinds() []string {
	out := []string{}
	for _, e := range a.Snapshot() {
		out = append(out, e.Kind)
	}
	return out
}

// counterIDGen returns "id-N" with N monotonically increasing. Tests
// pin this for deterministic ids.
func counterIDGen() IDGen {
	var n int64
	return func() (string, error) {
		v := atomic.AddInt64(&n, 1)
		return "id-" + strconv.FormatInt(v, 10), nil
	}
}

func newTestManager(t *testing.T) (*Manager, *recordingAudit) {
	t.Helper()
	audit := &recordingAudit{}
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	clk := func() time.Time { now = now.Add(time.Second); return now }
	m := NewManager(NewMemoryStore(),
		WithAudit(audit),
		WithClock(clk),
		WithIDGen(counterIDGen()),
	)
	return m, audit
}

func TestManager_Create_AssignsMonotonicPositions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, audit := newTestManager(t)

	r1, err := m.Create(ctx, "first")
	if err != nil {
		t.Fatal(err)
	}
	r2, err := m.Create(ctx, "second")
	if err != nil {
		t.Fatal(err)
	}
	r3, err := m.Create(ctx, "third")
	if err != nil {
		t.Fatal(err)
	}
	if r1.Position != 0 || r2.Position != 1 || r3.Position != 2 {
		t.Errorf("positions = %d/%d/%d, want 0/1/2", r1.Position, r2.Position, r3.Position)
	}
	for _, r := range []Record{r1, r2, r3} {
		if r.ID == "" || r.Name == "" {
			t.Errorf("missing ID/name: %+v", r)
		}
		if r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() {
			t.Errorf("zero timestamps: %+v", r)
		}
	}
	// Three created emits.
	count := 0
	for _, k := range audit.Kinds() {
		if k == EventKindSessionCreated {
			count++
		}
	}
	if count != 3 {
		t.Errorf("session.created emits = %d, want 3", count)
	}
}

func TestManager_Create_RejectsBlankName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, _ := newTestManager(t)
	if _, err := m.Create(ctx, "   "); !errors.Is(err, ErrInvalidName) {
		t.Errorf("got %v, want ErrInvalidName", err)
	}
}

func TestManager_Rename_EmitsEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, audit := newTestManager(t)
	r, _ := m.Create(ctx, "old")
	if err := m.Rename(ctx, r.ID, "new"); err != nil {
		t.Fatal(err)
	}
	got, _ := m.Get(ctx, r.ID)
	if got.Name != "new" {
		t.Errorf("Name = %q, want new", got.Name)
	}
	if !containsKind(audit.Kinds(), EventKindSessionRenamed) {
		t.Errorf("missing %s emit; got %v", EventKindSessionRenamed, audit.Kinds())
	}
}

func TestManager_Rename_TrimsAndRejectsBlank(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, _ := newTestManager(t)
	r, _ := m.Create(ctx, "x")
	if err := m.Rename(ctx, r.ID, "  trimmed  "); err != nil {
		t.Fatal(err)
	}
	got, _ := m.Get(ctx, r.ID)
	if got.Name != "trimmed" {
		t.Errorf("Name = %q, want trimmed", got.Name)
	}
	if err := m.Rename(ctx, r.ID, "   "); !errors.Is(err, ErrInvalidName) {
		t.Errorf("got %v, want ErrInvalidName", err)
	}
}

func TestManager_Delete_EmitsEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, audit := newTestManager(t)
	r, _ := m.Create(ctx, "doomed")
	if err := m.Delete(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(ctx, r.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("got %v, want ErrSessionNotFound", err)
	}
	if !containsKind(audit.Kinds(), EventKindSessionDeleted) {
		t.Errorf("missing %s emit; got %v", EventKindSessionDeleted, audit.Kinds())
	}
}

func TestManager_Reorder_PersistsOrderAndEmits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, audit := newTestManager(t)
	a, _ := m.Create(ctx, "a")
	b, _ := m.Create(ctx, "b")
	c, _ := m.Create(ctx, "c")
	if err := m.Reorder(ctx, []string{c.ID, a.ID, b.ID}); err != nil {
		t.Fatal(err)
	}
	got, _ := m.List(ctx)
	wantOrder := []string{c.ID, a.ID, b.ID}
	for i, want := range wantOrder {
		if got[i].ID != want {
			t.Errorf("List[%d] = %s, want %s", i, got[i].ID, want)
		}
	}
	if !containsKind(audit.Kinds(), EventKindSessionReordered) {
		t.Errorf("missing %s emit; got %v", EventKindSessionReordered, audit.Kinds())
	}
}

func TestManager_AppendMessage_AssignsSequenceAndBumpsActive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, audit := newTestManager(t)
	r, _ := m.Create(ctx, "chat")
	createdActive := r.LastActiveAt

	msg, err := m.AppendMessage(ctx, r.ID, Message{
		Role:    RoleUser,
		Content: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Sequence != 0 {
		t.Errorf("Sequence = %d, want 0", msg.Sequence)
	}
	msg2, err := m.AppendMessage(ctx, r.ID, Message{Role: RoleAssistant, Content: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if msg2.Sequence != 1 {
		t.Errorf("Sequence = %d, want 1", msg2.Sequence)
	}

	got, _ := m.Get(ctx, r.ID)
	if !got.LastActiveAt.After(createdActive) {
		t.Errorf("LastActiveAt = %v, want > %v", got.LastActiveAt, createdActive)
	}

	msgs, err := m.ListMessages(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Errorf("len = %d, want 2", len(msgs))
	}
	count := 0
	for _, k := range audit.Kinds() {
		if k == EventKindSessionMessageAppended {
			count++
		}
	}
	if count != 2 {
		t.Errorf("message_appended emits = %d, want 2", count)
	}
}

func TestManager_DraftAndScroll_PerSessionPreserved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, audit := newTestManager(t)
	a, _ := m.Create(ctx, "a")
	b, _ := m.Create(ctx, "b")

	// FR-002: each session's draft + scroll preserved independently.
	if err := m.SaveDraft(ctx, a.ID, "writing in a"); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveScrollPosition(ctx, a.ID, 100); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveDraft(ctx, b.ID, "different draft"); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveScrollPosition(ctx, b.ID, 250); err != nil {
		t.Fatal(err)
	}

	gotA, _ := m.Get(ctx, a.ID)
	gotB, _ := m.Get(ctx, b.ID)
	if gotA.Draft != "writing in a" || gotA.ScrollPosition != 100 {
		t.Errorf("session a state = %+v", gotA)
	}
	if gotB.Draft != "different draft" || gotB.ScrollPosition != 250 {
		t.Errorf("session b state = %+v", gotB)
	}
	if !containsKind(audit.Kinds(), EventKindSessionDraftSaved) {
		t.Errorf("missing %s emit", EventKindSessionDraftSaved)
	}
	if !containsKind(audit.Kinds(), EventKindSessionScrollSaved) {
		t.Errorf("missing %s emit", EventKindSessionScrollSaved)
	}
}

func TestManager_MarkActive_EmitsAndUpdates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, audit := newTestManager(t)
	r, _ := m.Create(ctx, "x")
	originalActive := r.LastActiveAt
	if err := m.MarkActive(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := m.Get(ctx, r.ID)
	if !got.LastActiveAt.After(originalActive) {
		t.Errorf("LastActiveAt did not advance: was %v, now %v",
			originalActive, got.LastActiveAt)
	}
	if !containsKind(audit.Kinds(), EventKindSessionLastActiveBumped) {
		t.Errorf("missing %s emit", EventKindSessionLastActiveBumped)
	}
}

func TestManager_AppendMessage_UnknownSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, _ := newTestManager(t)
	_, err := m.AppendMessage(ctx, "ghost", Message{Role: RoleUser, Content: "x"})
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("got %v, want ErrSessionNotFound", err)
	}
}

func TestManager_RecordTimestamps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, _ := newTestManager(t)
	r, err := m.Create(ctx, "round trip")
	if err != nil {
		t.Fatal(err)
	}
	if r.ID == "" {
		t.Errorf("missing ID: %+v", r)
	}
	if r.Name != "round trip" {
		t.Errorf("Name = %q, want round trip", r.Name)
	}
	// Timestamps come from the pinned test clock — yyyymmdd prefix is
	// enough proof of clock plumbing without freezing the exact value.
	if !strings.HasPrefix(r.CreatedAt.UTC().Format(time.RFC3339Nano), "2026-04-25") {
		t.Errorf("CreatedAt = %v, want 2026-04-25 prefix", r.CreatedAt)
	}
}

func TestManager_SetSystemPrompt_PersistsAndEmits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, audit := newTestManager(t)
	r, err := m.Create(ctx, "chat")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetSystemPrompt(ctx, r.ID, "you are helpful", ContextKindSystem); err != nil {
		t.Fatalf("SetSystemPrompt: %v", err)
	}
	got, err := m.Get(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SystemPrompt != "you are helpful" || got.ContextKind != ContextKindSystem {
		t.Errorf("round-trip: got %+v", got)
	}
	if !containsKind(audit.Kinds(), EventKindSessionSystemPromptSet) {
		t.Errorf("missing %s emit; got %v", EventKindSessionSystemPromptSet, audit.Kinds())
	}
}

func TestManager_SetSystemPrompt_AcceptsUserSeed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, _ := newTestManager(t)
	r, err := m.Create(ctx, "chat")
	if err != nil {
		t.Fatal(err)
	}
	// user_seed sessions persist the kind even when content is empty
	// — the dialog appends the seed as a user message separately.
	if err := m.SetSystemPrompt(ctx, r.ID, "", ContextKindUserSeed); err != nil {
		t.Fatalf("SetSystemPrompt: %v", err)
	}
	got, _ := m.Get(ctx, r.ID)
	if got.ContextKind != ContextKindUserSeed {
		t.Errorf("ContextKind = %q, want %q", got.ContextKind, ContextKindUserSeed)
	}
}

func TestManager_SetSystemPrompt_RejectsInvalidKind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, _ := newTestManager(t)
	r, err := m.Create(ctx, "chat")
	if err != nil {
		t.Fatal(err)
	}
	err = m.SetSystemPrompt(ctx, r.ID, "x", "garbage")
	if !errors.Is(err, ErrInvalidContextKind) {
		t.Errorf("got %v, want ErrInvalidContextKind", err)
	}
}

func TestManager_SetSystemPrompt_MissingSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, _ := newTestManager(t)
	err := m.SetSystemPrompt(ctx, "ghost", "x", ContextKindSystem)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("got %v, want ErrSessionNotFound", err)
	}
}

func containsKind(kinds []string, want string) bool {
	for _, k := range kinds {
		if k == want {
			return true
		}
	}
	return false
}

// ── auto_titled manager method tests ────────────────────────────────────────

func TestManager_AutoTitle_WritesAndEmitsEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, audit := newTestManager(t)
	r, err := m.Create(ctx, "New session")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AutoTitle(ctx, r.ID, "Rust basics"); err != nil {
		t.Fatalf("AutoTitle: %v", err)
	}
	got, _ := m.Get(ctx, r.ID)
	if got.Name != "Rust basics" {
		t.Errorf("Name = %q, want Rust basics", got.Name)
	}
	if !got.AutoTitled {
		t.Error("AutoTitled = false, want true")
	}
	if !containsKind(audit.Kinds(), EventKindSessionAutoTitled) {
		t.Errorf("no %q event emitted; events: %v", EventKindSessionAutoTitled, audit.Kinds())
	}
}

func TestManager_AutoTitle_SupersededDropsWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, _ := newTestManager(t)
	r, err := m.Create(ctx, "New session")
	if err != nil {
		t.Fatal(err)
	}
	// First auto-title succeeds.
	if err := m.AutoTitle(ctx, r.ID, "First title"); err != nil {
		t.Fatalf("first AutoTitle: %v", err)
	}
	// Second auto-title must return ErrAutoTitleSuperseded.
	err = m.AutoTitle(ctx, r.ID, "Second title")
	if !errors.Is(err, ErrAutoTitleSuperseded) {
		t.Errorf("got %v, want ErrAutoTitleSuperseded", err)
	}
	got, _ := m.Get(ctx, r.ID)
	if got.Name != "First title" {
		t.Errorf("Name changed despite superseded; got %q", got.Name)
	}
}

func TestManager_MarkAutoTitleAttempted_SetsFlag(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, audit := newTestManager(t)
	r, err := m.Create(ctx, "New session")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkAutoTitleAttempted(ctx, r.ID); err != nil {
		t.Fatalf("MarkAutoTitleAttempted: %v", err)
	}
	got, _ := m.Get(ctx, r.ID)
	if !got.AutoTitled {
		t.Error("AutoTitled = false after MarkAutoTitleAttempted")
	}
	if got.Name != "New session" {
		t.Errorf("Name changed; got %q", got.Name)
	}
	if !containsKind(audit.Kinds(), EventKindSessionAutoTitled) {
		t.Errorf("no %q event emitted", EventKindSessionAutoTitled)
	}
}

func TestManager_ClearTitle_ResetsAndEmitsRenamedEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, audit := newTestManager(t)
	r, err := m.Create(ctx, "some title")
	if err != nil {
		t.Fatal(err)
	}
	// Mark as auto-titled first.
	if err := m.AutoTitle(ctx, r.ID, "auto title"); err != nil {
		t.Fatalf("AutoTitle: %v", err)
	}
	if err := m.ClearTitle(ctx, r.ID); err != nil {
		t.Fatalf("ClearTitle: %v", err)
	}
	got, _ := m.Get(ctx, r.ID)
	if got.Name != "" {
		t.Errorf("Name = %q, want empty", got.Name)
	}
	if got.AutoTitled {
		t.Error("AutoTitled = true after ClearTitle, want false")
	}
	if !containsKind(audit.Kinds(), EventKindSessionRenamed) {
		t.Errorf("no %q event emitted after ClearTitle; events: %v", EventKindSessionRenamed, audit.Kinds())
	}
}

func TestManager_RequestRetitle_OverwritesTitle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, audit := newTestManager(t)
	r, err := m.Create(ctx, "old title")
	if err != nil {
		t.Fatal(err)
	}
	// First auto-title locks the flag.
	if err := m.AutoTitle(ctx, r.ID, "auto-generated"); err != nil {
		t.Fatalf("AutoTitle: %v", err)
	}
	// RequestRetitle should overwrite even though auto_titled is already 1.
	if err := m.RequestRetitle(ctx, r.ID, "new manual title"); err != nil {
		t.Fatalf("RequestRetitle: %v", err)
	}
	got, _ := m.Get(ctx, r.ID)
	if got.Name != "new manual title" {
		t.Errorf("Name = %q, want new manual title", got.Name)
	}
	if !got.AutoTitled {
		t.Error("AutoTitled = false after RequestRetitle, want true")
	}
	if !containsKind(audit.Kinds(), EventKindSessionAutoTitled) {
		t.Errorf("no %q event emitted", EventKindSessionAutoTitled)
	}
}

// TestManager_AutoTitle_RaceCheck verifies that when auto_titled is flipped
// between the predicate check and the store write (simulated by a manual
// AutoTitle call before the second), the second call is dropped.
// This is the re-check-predicate-inside-transaction acceptance test.
func TestManager_AutoTitle_RaceCheck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, _ := newTestManager(t)
	r, err := m.Create(ctx, "New session")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a user rename happening in the window: the store's AutoTitle
	// predicate is checked inside the transaction, so calling store.Rename
	// (which sets auto_titled=1) before AutoTitle will cause ErrAutoTitleSuperseded.
	if err := m.Rename(ctx, r.ID, "User renamed me"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	// Now AutoTitle should be superseded because auto_titled is already 1.
	err = m.AutoTitle(ctx, r.ID, "Engine would set this")
	if !errors.Is(err, ErrAutoTitleSuperseded) {
		t.Errorf("got %v, want ErrAutoTitleSuperseded (race check)", err)
	}
	got, _ := m.Get(ctx, r.ID)
	if got.Name != "User renamed me" {
		t.Errorf("Name = %q, want User renamed me (user rename preserved)", got.Name)
	}
}
