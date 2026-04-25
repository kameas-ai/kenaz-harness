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

func TestManager_RecordToView_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, _ := newTestManager(t)
	r, err := m.Create(ctx, "round trip")
	if err != nil {
		t.Fatal(err)
	}
	view := r.ToView()
	if view.ID != r.ID || view.Name != "round trip" {
		t.Errorf("view = %+v, want id=%s name=%q", view, r.ID, "round trip")
	}
	if !strings.HasPrefix(view.CreatedAt, "2026-04-25") {
		t.Errorf("CreatedAt = %q, want 2026-04-25 prefix", view.CreatedAt)
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
