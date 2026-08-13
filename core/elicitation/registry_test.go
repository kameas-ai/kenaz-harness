package elicitation_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/elicitation"
)

// recordingPublisher captures announced entries. The registry publishes
// from the parking goroutine while the test body reads, so the slice is
// mutex-guarded and every read goes through snapshot().
type recordingPublisher struct {
	mu      sync.Mutex
	entries []elicitation.Entry
}

func (p *recordingPublisher) publish(e elicitation.Entry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.entries = append(p.entries, e)
}

func (p *recordingPublisher) snapshot() []elicitation.Entry {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]elicitation.Entry, len(p.entries))
	copy(out, p.entries)
	return out
}

func TestQuestionValidate_FreeformIsValid(t *testing.T) {
	q := elicitation.Question{Text: "What next?"}
	if err := q.Validate(); err != nil {
		t.Fatalf("free-form question rejected: %v", err)
	}
	// The tool contract, by contrast, requires an explicit control.
	if err := q.RequireStructured(); err == nil {
		t.Fatal("RequireStructured accepted a free-form question")
	}
}

func TestQuestionValidate_Rules(t *testing.T) {
	tests := []struct {
		name    string
		q       elicitation.Question
		wantErr string
	}{
		{
			name:    "missing text",
			q:       elicitation.Question{Kind: elicitation.KindText},
			wantErr: "question is required",
		},
		{
			name:    "unknown kind",
			q:       elicitation.Question{Text: "q", Kind: elicitation.Kind("dropdown")},
			wantErr: `unknown kind "dropdown"; must be one of radio, checkbox, text, number, slider, date, file`,
		},
		{
			name:    "radio without options",
			q:       elicitation.Question{Text: "q", Kind: elicitation.KindRadio},
			wantErr: `kind="radio" requires at least one option`,
		},
		{
			name:    "checkbox without options",
			q:       elicitation.Question{Text: "q", Kind: elicitation.KindCheckbox},
			wantErr: `kind="checkbox" requires at least one option`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.q.Validate()
			if err == nil {
				t.Fatalf("expected error %q, got nil", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("error text drifted:\n got: %s\nwant: %s", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestPark_ResolveWakesWaiter(t *testing.T) {
	pub := &recordingPublisher{}
	r := elicitation.NewRegistry(elicitation.Config{Publish: pub.publish})

	type result struct {
		a   elicitation.Answer
		err error
	}
	done := make(chan result, 1)
	go func() {
		a, err := r.Park(context.Background(), elicitation.Request{
			Question: elicitation.Question{Text: "Pick", Kind: elicitation.KindText},
		})
		done <- result{a, err}
	}()

	id := waitForAnnounce(t, pub)
	if err := r.Resolve(id, elicitation.JSONAnswer(json.RawMessage(`"yes"`))); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Park: %v", got.err)
		}
		if string(got.a.Value) != `"yes"` {
			t.Fatalf("answer = %s, want \"yes\"", got.a.Value)
		}
		// A bare JSON string also populates Text so free-form consumers
		// see the same value.
		if got.a.Text != "yes" {
			t.Fatalf("Text = %q, want yes", got.a.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Park did not return after Resolve")
	}
}

func TestPark_ContextCancellationUnparks(t *testing.T) {
	r := elicitation.NewRegistry(elicitation.Config{})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := r.Park(ctx, elicitation.Request{
			ID:       "ask-1",
			Question: elicitation.Question{Text: "Waits", Kind: elicitation.KindText},
		})
		done <- err
	}()

	waitFor(t, func() bool { return r.PendingCount() == 1 }, "entry to park")
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Park did not return after cancellation")
	}
	if n := r.PendingCount(); n != 0 {
		t.Fatalf("cancelled ask leaked: PendingCount = %d", n)
	}
}

func TestRegister_NodeAskSurvivesForLookup(t *testing.T) {
	r := elicitation.NewRegistry(elicitation.Config{})
	id := elicitation.NodeAskID("run-1", "ask_user")

	if _, err := r.Register(elicitation.Request{
		ID:       id,
		RunID:    "run-1",
		NodeID:   "ask_user",
		Question: elicitation.Question{Text: "Your move?"},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, ok := r.Answered(id); ok {
		t.Fatal("Answered reported an answer before one was supplied")
	}
	if err := r.Resolve(id, elicitation.TextAnswer("north")); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Idempotent: the ask executor fires again after the kernel resumes
	// and must still see the answer.
	for i := range 2 {
		a, ok := r.Answered(id)
		if !ok {
			t.Fatalf("lookup %d: answer not found after Resolve", i)
		}
		if a.Text != "north" {
			t.Fatalf("lookup %d: answer = %q, want north", i, a.Text)
		}
	}
}

func TestRegister_DuplicateIDRejected(t *testing.T) {
	r := elicitation.NewRegistry(elicitation.Config{})
	req := elicitation.Request{ID: "dup", Question: elicitation.Question{Text: "q"}}
	if _, err := r.Register(req); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if _, err := r.Register(req); !errors.Is(err, elicitation.ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

func TestDeferred_ConcurrencyCap(t *testing.T) {
	r := elicitation.NewRegistry(elicitation.Config{MaxDeferred: 2})
	req := func() elicitation.Request {
		return elicitation.Request{
			SessionID: "sess-1",
			Question:  elicitation.Question{Text: "q", Kind: elicitation.KindText},
			Mode:      elicitation.ModeDeferred,
		}
	}
	for i := range 2 {
		if _, err := r.Register(req()); err != nil {
			t.Fatalf("Register %d: %v", i, err)
		}
	}
	if _, err := r.Register(req()); !errors.Is(err, elicitation.ErrTooManyPending) {
		t.Fatalf("err = %v, want ErrTooManyPending", err)
	}

	// A different session is unaffected.
	other := req()
	other.SessionID = "sess-2"
	if _, err := r.Register(other); err != nil {
		t.Fatalf("other session Register: %v", err)
	}
}

func TestSweepExpired_DeferredOnly(t *testing.T) {
	now := time.Now()
	clock := now
	r := elicitation.NewRegistry(elicitation.Config{
		Expiry: time.Hour,
		Now:    func() time.Time { return clock },
	})

	deferredEntry, err := r.Register(elicitation.Request{
		ID:        "deferred-1",
		SessionID: "sess-1",
		Question:  elicitation.Question{Text: "later?", Kind: elicitation.KindText},
		Mode:      elicitation.ModeDeferred,
	})
	if err != nil {
		t.Fatalf("Register deferred: %v", err)
	}
	if deferredEntry.ExpiresAt.IsZero() {
		t.Fatal("deferred ask has no deadline")
	}

	// A parked graph-run ask. Durable-pause contract: it must never
	// expire, however long the user takes.
	if _, err := r.Register(elicitation.Request{
		ID:       elicitation.NodeAskID("run-1", "ask_user"),
		RunID:    "run-1",
		NodeID:   "ask_user",
		Question: elicitation.Question{Text: "Your move?"},
	}); err != nil {
		t.Fatalf("Register node ask: %v", err)
	}

	clock = now.Add(2 * time.Hour)
	if n := r.SweepExpired(); n != 1 {
		t.Fatalf("SweepExpired = %d, want 1", n)
	}

	got, _ := r.Get("deferred-1")
	if got.Status != elicitation.StatusExpired {
		t.Fatalf("deferred status = %q, want expired", got.Status)
	}
	if got.DeclineReason != "expired" {
		t.Fatalf("decline reason = %q, want expired", got.DeclineReason)
	}
	node, _ := r.Get(elicitation.NodeAskID("run-1", "ask_user"))
	if node.Status != elicitation.StatusPending {
		t.Fatalf("parked graph ask status = %q, want pending — durable pauses must not expire", node.Status)
	}
}

// Reading the pending set enforces expiry. The predecessor
// (core/asks.DeferredRegistry) exported SweepExpired and nothing ever
// called it, so the documented 24-hour TTL never actually expired
// anything. This pins that ListPending is the sweep site.
func TestListPending_EnforcesExpiryWithoutABackgroundTimer(t *testing.T) {
	now := time.Now()
	clock := now
	r := elicitation.NewRegistry(elicitation.Config{
		Expiry: time.Hour,
		Now:    func() time.Time { return clock },
	})
	if _, err := r.Register(elicitation.Request{
		ID: "d1", SessionID: "s1", Mode: elicitation.ModeDeferred,
		Question: elicitation.Question{Text: "later?", Kind: elicitation.KindText},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got := r.ListPending(elicitation.Filter{SessionID: "s1"}); len(got) != 1 {
		t.Fatalf("before expiry: %d pending, want 1", len(got))
	}

	clock = now.Add(2 * time.Hour)
	if got := r.ListPending(elicitation.Filter{SessionID: "s1"}); len(got) != 0 {
		t.Fatalf("after expiry: %d pending, want 0 — nobody called SweepExpired for us", len(got))
	}
	e, _ := r.Get("d1")
	if e.Status != elicitation.StatusExpired {
		t.Fatalf("status = %q, want expired", e.Status)
	}
}

// Dismissal wakes the waiter with Cancelled=true and records a decline
// rather than pretending the ask was answered.
func TestDecline_WakesWaiterAndRecordsTheDismissal(t *testing.T) {
	r := elicitation.NewRegistry(elicitation.Config{})

	done := make(chan elicitation.Answer, 1)
	go func() {
		a, _ := r.Park(context.Background(), elicitation.Request{
			ID:       "ask-1",
			Question: elicitation.Question{Text: "Proceed?", Kind: elicitation.KindText},
		})
		done <- a
	}()
	waitFor(t, func() bool { return r.PendingCount() == 1 }, "entry to park")

	if err := r.Decline("ask-1", "dismissed by user"); err != nil {
		t.Fatalf("Decline: %v", err)
	}
	select {
	case a := <-done:
		if !a.Cancelled {
			t.Fatal("waiter woke with Cancelled=false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Decline did not wake the parked waiter")
	}

	e, _ := r.Get("ask-1")
	if e.Status != elicitation.StatusDeclined {
		t.Fatalf("status = %q, want declined", e.Status)
	}
	if e.DeclineReason != "dismissed by user" {
		t.Fatalf("reason = %q", e.DeclineReason)
	}
	if _, ok := r.Answered("ask-1"); ok {
		t.Fatal("a declined ask must not report an answer")
	}
}

func TestRecordStep_CompletesWhenVisibleQuestionsAnswered(t *testing.T) {
	r := elicitation.NewRegistry(elicitation.Config{})
	batch := []elicitation.Question{
		{ID: "q1", Text: "Deploy?", Kind: elicitation.KindRadio, Options: []elicitation.Option{{Value: "yes", Label: "Yes"}}},
		{
			ID: "q2", Text: "Which env?", Kind: elicitation.KindText,
			DependsOn: &elicitation.Dependency{QuestionID: "q1", Condition: json.RawMessage(`{"equals":"yes"}`)},
		},
	}
	if _, err := r.Register(elicitation.Request{
		ID:       "wiz",
		Question: elicitation.Question{Batch: batch},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	complete, err := r.RecordStep("wiz", "q1", json.RawMessage(`"yes"`), false)
	if err != nil {
		t.Fatalf("RecordStep q1: %v", err)
	}
	if complete {
		t.Fatal("wizard completed while q2 was still visible and unanswered")
	}

	complete, err = r.RecordStep("wiz", "q2", json.RawMessage(`"prod"`), false)
	if err != nil {
		t.Fatalf("RecordStep q2: %v", err)
	}
	if !complete {
		t.Fatal("wizard did not complete after every visible question was answered")
	}

	a, ok := r.Answered("wiz")
	if !ok {
		t.Fatal("wizard answer not recorded")
	}
	var ba elicitation.BatchAnswer
	if err := json.Unmarshal(a.Value, &ba); err != nil {
		t.Fatalf("decode batch answer: %v", err)
	}
	if string(ba.Answers["q2"]) != `"prod"` {
		t.Fatalf("q2 answer = %s", ba.Answers["q2"])
	}
	if ba.Dismissed {
		t.Fatal("Dismissed set on a completed wizard")
	}
}

func TestRecordStep_HiddenQuestionDoesNotBlockCompletion(t *testing.T) {
	r := elicitation.NewRegistry(elicitation.Config{})
	batch := []elicitation.Question{
		{ID: "q1", Text: "Deploy?", Kind: elicitation.KindRadio, Options: []elicitation.Option{{Value: "no", Label: "No"}}},
		{
			ID: "q2", Text: "Which env?", Kind: elicitation.KindText,
			DependsOn: &elicitation.Dependency{QuestionID: "q1", Condition: json.RawMessage(`{"equals":"yes"}`)},
		},
	}
	if _, err := r.Register(elicitation.Request{ID: "wiz", Question: elicitation.Question{Batch: batch}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	complete, err := r.RecordStep("wiz", "q1", json.RawMessage(`"no"`), false)
	if err != nil {
		t.Fatalf("RecordStep: %v", err)
	}
	if !complete {
		t.Fatal("wizard did not complete: the conditional question should be invisible")
	}
}

func TestRecordStep_DismissedReturnsPartial(t *testing.T) {
	r := elicitation.NewRegistry(elicitation.Config{})
	batch := []elicitation.Question{
		{ID: "q1", Text: "One", Kind: elicitation.KindText},
		{ID: "q2", Text: "Two", Kind: elicitation.KindText},
	}
	if _, err := r.Register(elicitation.Request{ID: "wiz", Question: elicitation.Question{Batch: batch}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := r.RecordStep("wiz", "q1", json.RawMessage(`"a"`), false); err != nil {
		t.Fatalf("RecordStep: %v", err)
	}
	complete, err := r.RecordStep("wiz", "q2", nil, true)
	if err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if !complete {
		t.Fatal("dismissal did not resolve the wizard")
	}
	a, _ := r.Answered("wiz")
	var ba elicitation.BatchAnswer
	if err := json.Unmarshal(a.Value, &ba); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !ba.Dismissed {
		t.Fatal("Dismissed not set")
	}
	if string(ba.AnsweredSoFar["q1"]) != `"a"` {
		t.Fatalf("answered_so_far = %v", ba.AnsweredSoFar)
	}
	if ba.Answers != nil {
		t.Fatalf("Answers should be nil on dismissal, got %v", ba.Answers)
	}
}

func TestResolve_UnknownID(t *testing.T) {
	r := elicitation.NewRegistry(elicitation.Config{})
	if err := r.Resolve("nope", elicitation.TextAnswer("x")); !errors.Is(err, elicitation.ErrUnknown) {
		t.Fatalf("err = %v, want ErrUnknown", err)
	}
	if err := r.Decline("nope", ""); !errors.Is(err, elicitation.ErrUnknown) {
		t.Fatalf("Decline err = %v, want ErrUnknown", err)
	}
}

func TestListPending_FiltersByModeAndSession(t *testing.T) {
	r := elicitation.NewRegistry(elicitation.Config{})
	if _, err := r.Register(elicitation.Request{
		ID: "b1", SessionID: "s1",
		Question: elicitation.Question{Text: "blocking"},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := r.Register(elicitation.Request{
		ID: "d1", SessionID: "s1", Mode: elicitation.ModeDeferred,
		Question: elicitation.Question{Text: "deferred", Kind: elicitation.KindText},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	deferred := r.ListPending(elicitation.Filter{SessionID: "s1", Mode: elicitation.ModeDeferred})
	if len(deferred) != 1 || deferred[0].ID != "d1" {
		t.Fatalf("deferred filter = %+v", deferred)
	}
	blocking := r.ListPending(elicitation.Filter{Mode: elicitation.ModeBlocking})
	if len(blocking) != 1 || blocking[0].ID != "b1" {
		t.Fatalf("blocking filter = %+v", blocking)
	}
	if got := r.ListPending(elicitation.Filter{SessionID: "s2"}); len(got) != 0 {
		t.Fatalf("other session = %+v", got)
	}
}

func TestSystemReminderText_Format(t *testing.T) {
	got := elicitation.SystemReminderText("elicit-abc", "yes")
	want := `Pending elicitation "elicit-abc" answered: yes`
	if got != want {
		t.Fatalf("reminder = %q, want %q", got, want)
	}
}

// ---- helpers ----

func waitForAnnounce(t *testing.T, pub *recordingPublisher) string {
	t.Helper()
	var id string
	waitFor(t, func() bool {
		entries := pub.snapshot()
		if len(entries) == 0 {
			return false
		}
		id = entries[0].ID
		return true
	}, "ask to be announced")
	return id
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}
