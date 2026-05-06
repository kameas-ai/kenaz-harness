package sessions

// impl_broker_test.go — LeftRail real-time update integration tests (PR #88, v0.5.3).
//
// Every session-list mutation must emit a "session.list_changed" event via
// the wired SessionListBroker so the LeftRail can refresh without polling.
//
// Tests verify that WithBrokerOpt wires publishListChanged correctly for:
//   - Create         → reason "created"
//   - Rename         → reason "renamed"
//   - Delete         → reason "deleted"
//   - MoveToProject  → reason "moved"
//   - SuggestTitle   → reason "title_set"
//   - ClearTitle     → reason "title_cleared"

import (
	"context"
	"sync"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/session"
	autotitle "github.com/sigil-tech/kaneaz-harness/core/sessions/autotitle"
)

// recordingBroker captures every Publish call for assertion.
type recordingBroker struct {
	mu     sync.Mutex
	events []brokerEvent
}

type brokerEvent struct {
	topic   string
	payload any
}

func (b *recordingBroker) Publish(topic string, payload any) {
	b.mu.Lock()
	b.events = append(b.events, brokerEvent{topic: topic, payload: payload})
	b.mu.Unlock()
}

func (b *recordingBroker) events_() []brokerEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]brokerEvent, len(b.events))
	copy(out, b.events)
	return out
}

// fakeTitleGen implements TitleGenerator for tests that exercise SuggestTitle.
type fakeTitleGen struct {
	title string
	err   error
}

func (f *fakeTitleGen) GenerateTitle(_ context.Context, _ autotitle.Transcript) (string, error) {
	return f.title, f.err
}

// newBrokeredAPI builds a managerAPI wired with a fresh recording broker.
func newBrokeredAPI(t *testing.T) (SessionsAPI, *recordingBroker) {
	t.Helper()
	mgr := session.NewManager(session.NewMemoryStore())
	broker := &recordingBroker{}
	api := WithBrokerOpt(NewManagerAPI(mgr), broker)
	return api, broker
}

func findEvent(events []brokerEvent, topic, reason string) *listChangedPayload {
	for _, e := range events {
		if e.topic != topic {
			continue
		}
		if p, ok := e.payload.(listChangedPayload); ok && p.Reason == reason {
			return &p
		}
	}
	return nil
}

// TestBroker_Create_EmitsListChanged verifies that Create publishes
// "session.list_changed" with reason="created".
func TestBroker_Create_EmitsListChanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api, broker := newBrokeredAPI(t)

	s, err := api.Create(ctx, "alpha")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	events := broker.events_()
	p := findEvent(events, "session.list_changed", "created")
	if p == nil {
		t.Fatalf("no session.list_changed/created event; got %+v", events)
	}
	if p.SessionID != s.ID {
		t.Errorf("SessionID = %q, want %q", p.SessionID, s.ID)
	}
	if p.Timestamp == 0 {
		t.Errorf("Timestamp is zero")
	}
}

// TestBroker_Rename_EmitsListChanged verifies that Rename publishes
// "session.list_changed" with reason="renamed".
func TestBroker_Rename_EmitsListChanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api, broker := newBrokeredAPI(t)

	s, _ := api.Create(ctx, "before")
	broker.events = nil // reset after Create event

	if err := api.Rename(ctx, s.ID, "after"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	events := broker.events_()
	p := findEvent(events, "session.list_changed", "renamed")
	if p == nil {
		t.Fatalf("no session.list_changed/renamed event; got %+v", events)
	}
	if p.SessionID != s.ID {
		t.Errorf("SessionID = %q, want %q", p.SessionID, s.ID)
	}
}

// TestBroker_Delete_EmitsListChanged verifies that Delete publishes
// "session.list_changed" with reason="deleted".
func TestBroker_Delete_EmitsListChanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api, broker := newBrokeredAPI(t)

	s, _ := api.Create(ctx, "doomed")
	broker.mu.Lock()
	broker.events = nil
	broker.mu.Unlock()

	if err := api.Delete(ctx, s.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	events := broker.events_()
	p := findEvent(events, "session.list_changed", "deleted")
	if p == nil {
		t.Fatalf("no session.list_changed/deleted event; got %+v", events)
	}
	if p.SessionID != s.ID {
		t.Errorf("SessionID = %q, want %q", p.SessionID, s.ID)
	}
}

// TestBroker_MoveToProject_EmitsListChanged verifies that MoveToProject
// publishes "session.list_changed" with reason="moved".
func TestBroker_MoveToProject_EmitsListChanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api, broker := newBrokeredAPI(t)

	s, _ := api.Create(ctx, "rover")
	broker.mu.Lock()
	broker.events = nil
	broker.mu.Unlock()

	if err := api.MoveToProject(ctx, s.ID, "p-99"); err != nil {
		t.Fatalf("MoveToProject: %v", err)
	}

	events := broker.events_()
	p := findEvent(events, "session.list_changed", "moved")
	if p == nil {
		t.Fatalf("no session.list_changed/moved event; got %+v", events)
	}
	if p.SessionID != s.ID {
		t.Errorf("SessionID = %q, want %q", p.SessionID, s.ID)
	}
	if p.ProjectID != "p-99" {
		t.Errorf("ProjectID = %q, want p-99", p.ProjectID)
	}
}

// TestBroker_SuggestTitle_EmitsListChanged verifies that SuggestTitle
// publishes "session.list_changed" with reason="title_set".
func TestBroker_SuggestTitle_EmitsListChanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mgr := session.NewManager(session.NewMemoryStore())
	broker := &recordingBroker{}
	api := WithBrokerOpt(
		WithTitleGeneratorOpt(NewManagerAPI(mgr), &fakeTitleGen{title: "AI Title"}),
		broker,
	)

	s, _ := api.Create(ctx, "untitled")
	// Add a message so the transcript is non-empty for the generator.
	smgr := session.NewManager(session.NewMemoryStore())
	_ = smgr // unused; the fake generator doesn't use the transcript
	broker.mu.Lock()
	broker.events = nil
	broker.mu.Unlock()

	got, err := api.SuggestTitle(ctx, s.ID)
	if err != nil {
		t.Fatalf("SuggestTitle: %v", err)
	}
	if got != "AI Title" {
		t.Errorf("SuggestTitle returned %q, want AI Title", got)
	}

	events := broker.events_()
	p := findEvent(events, "session.list_changed", "title_set")
	if p == nil {
		t.Fatalf("no session.list_changed/title_set event; got %+v", events)
	}
	if p.SessionID != s.ID {
		t.Errorf("SessionID = %q, want %q", p.SessionID, s.ID)
	}
}

// TestBroker_ClearTitle_EmitsListChanged verifies that ClearTitle
// publishes "session.list_changed" with reason="title_cleared".
func TestBroker_ClearTitle_EmitsListChanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api, broker := newBrokeredAPI(t)

	s, _ := api.Create(ctx, "titled")
	broker.mu.Lock()
	broker.events = nil
	broker.mu.Unlock()

	if err := api.ClearTitle(ctx, s.ID); err != nil {
		t.Fatalf("ClearTitle: %v", err)
	}

	events := broker.events_()
	p := findEvent(events, "session.list_changed", "title_cleared")
	if p == nil {
		t.Fatalf("no session.list_changed/title_cleared event; got %+v", events)
	}
	if p.SessionID != s.ID {
		t.Errorf("SessionID = %q, want %q", p.SessionID, s.ID)
	}
}

// TestBroker_NilBroker_NoopPublish verifies that calling session mutations
// with no broker wired does not panic (nil guard in publishListChanged).
func TestBroker_NilBroker_NoopPublish(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := session.NewManager(session.NewMemoryStore())
	// No broker wired.
	api := NewManagerAPI(mgr)

	s, err := api.Create(ctx, "no-broker")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := api.Rename(ctx, s.ID, "renamed"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if err := api.MoveToProject(ctx, s.ID, "p-x"); err != nil {
		t.Fatalf("MoveToProject: %v", err)
	}
	if err := api.ClearTitle(ctx, s.ID); err != nil {
		t.Fatalf("ClearTitle: %v", err)
	}
	if err := api.Delete(ctx, s.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// If we reach here without panicking, the nil guard is working.
}
