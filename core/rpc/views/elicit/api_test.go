package elicit_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/elicitation"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/elicit"
	"github.com/kameas-ai/kenaz-harness/core/tools/askuserquestion"
)

// fakeEmitter records Emit calls for assertions. The API's OpenDialog /
// OpenWizard helpers block on a goroutine that calls Emit; the test
// goroutine reads emitted records concurrently. Guard the slice with a
// mutex so the race detector stays quiet.
type fakeEmitter struct {
	mu      sync.Mutex
	emitted []emitRecord
}

type emitRecord struct {
	topic   string
	payload any
}

func (f *fakeEmitter) Emit(_ context.Context, topic string, payload any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.emitted = append(f.emitted, emitRecord{topic: topic, payload: payload})
}

// snapshot returns a copy of the recorded emit history for assertion-side
// reads. All test-goroutine reads of em.snapshot() MUST go through snapshot
// to keep the race detector quiet.
func (f *fakeEmitter) snapshot() []emitRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]emitRecord, len(f.emitted))
	copy(out, f.emitted)
	return out
}

func TestSubmitAnswer_ResolvesOpenDialog(t *testing.T) {
	em := &fakeEmitter{}
	api := elicit.New(elicit.Config{Emitter: em})
	api.SetContext(context.Background())

	args := askuserquestion.AskArgs{
		Question: "Pick one",
		Kind:     askuserquestion.KindRadio,
		Options: []askuserquestion.QuestionOption{
			{Value: "a", Label: "A"},
		},
	}

	var result elicitation.Answer
	var goErr error

	done := make(chan struct{})
	go func() {
		defer close(done)
		result, goErr = api.OpenDialog(context.Background(), args.ToQuestion())
	}()

	// Give OpenDialog time to register the pending entry and emit.
	time.Sleep(10 * time.Millisecond)

	// Verify the event was emitted.
	if len(em.snapshot()) != 1 {
		t.Fatalf("expected 1 emit, got %d", len(em.snapshot()))
	}
	if em.snapshot()[0].topic != elicit.TopicElicitPending {
		t.Errorf("expected topic=%q, got %q", elicit.TopicElicitPending, em.snapshot()[0].topic)
	}

	req, ok := em.snapshot()[0].payload.(elicit.ElicitRequest)
	if !ok {
		t.Fatalf("payload is not ElicitRequest: %T", em.snapshot()[0].payload)
	}
	if req.Question != "Pick one" {
		t.Errorf("unexpected question: %q", req.Question)
	}

	// Submit the answer.
	answerJSON := json.RawMessage(`"a"`)
	if err := api.SubmitAnswer(context.Background(), req.RequestID, answerJSON, false); err != nil {
		t.Fatalf("SubmitAnswer: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("OpenDialog did not return after SubmitAnswer")
	}

	if goErr != nil {
		t.Fatalf("OpenDialog error: %v", goErr)
	}
	if result.Cancelled {
		t.Error("expected Cancelled=false")
	}
	var answer string
	if err := json.Unmarshal(result.Value, &answer); err != nil {
		t.Fatalf("unmarshal answer: %v", err)
	}
	if answer != "a" {
		t.Errorf("expected answer=a, got %q", answer)
	}
}

func TestSubmitAnswer_CancelledFlow(t *testing.T) {
	em := &fakeEmitter{}
	api := elicit.New(elicit.Config{Emitter: em})
	api.SetContext(context.Background())

	args := askuserquestion.AskArgs{
		Question: "Are you sure?",
		Kind:     askuserquestion.KindText,
	}

	var result elicitation.Answer
	done := make(chan struct{})
	go func() {
		defer close(done)
		result, _ = api.OpenDialog(context.Background(), args.ToQuestion())
	}()

	time.Sleep(10 * time.Millisecond)

	if len(em.snapshot()) == 0 {
		t.Fatal("no emit recorded")
	}
	req := em.snapshot()[0].payload.(elicit.ElicitRequest)

	// Cancel.
	if err := api.SubmitAnswer(context.Background(), req.RequestID, nil, true); err != nil {
		t.Fatalf("SubmitAnswer: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("OpenDialog did not return after cancel")
	}

	if !result.Cancelled {
		t.Error("expected Cancelled=true")
	}
}

func TestSubmitAnswer_UnknownRequestID(t *testing.T) {
	api := elicit.New(elicit.Config{})
	err := api.SubmitAnswer(context.Background(), "nonexistent-id", nil, false)
	if err == nil {
		t.Error("expected ErrUnknownRequest, got nil")
	}
}

func TestListPending_ReturnsEmpty(t *testing.T) {
	api := elicit.New(elicit.Config{})
	pending, err := api.ListPending(context.Background())
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected empty list, got %d entries", len(pending))
	}
}

func TestOpenDialog_ContextCancellation(t *testing.T) {
	api := elicit.New(elicit.Config{})
	api.SetContext(context.Background())

	ctx, cancel := context.WithCancel(context.Background())

	args := askuserquestion.AskArgs{
		Question: "Will be cancelled",
		Kind:     askuserquestion.KindText,
	}

	done := make(chan error, 1)
	go func() {
		_, err := api.OpenDialog(ctx, args.ToQuestion())
		done <- err
	}()

	time.Sleep(5 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected context cancellation error, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OpenDialog did not return after context cancellation")
	}
}

// ── WP05: multi-question wizard tests ──────────────────────────────────────

func TestSubmitWizardStep_TwoSteps_Complete(t *testing.T) {
	em := &fakeEmitter{}
	api := elicit.New(elicit.Config{Emitter: em})
	api.SetContext(context.Background())

	questions := []elicit.WizardQuestion{
		{ID: "q1", Question: "First?", Kind: "radio"},
		{ID: "q2", Question: "Second?", Kind: "text"},
	}
	req := elicit.ElicitRequest{Questions: questions}

	var wa elicit.WizardAnswer
	var wizErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		wa, wizErr = api.OpenWizard(context.Background(), req)
	}()

	// Wait for the wizard to register (emitter should have fired).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(em.snapshot()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if len(em.snapshot()) == 0 {
		t.Fatal("emitter did not receive any event")
	}

	emittedReq := em.snapshot()[0].payload.(elicit.ElicitRequest)
	reqID := emittedReq.RequestID
	if reqID == "" {
		t.Fatal("emitted request has empty request_id")
	}

	// Submit step 1.
	if err := api.SubmitWizardStep(context.Background(), reqID, "q1", json.RawMessage(`"optA"`), false); err != nil {
		t.Fatalf("SubmitWizardStep q1: %v", err)
	}
	// Not done yet — q2 still pending.
	select {
	case <-done:
		t.Fatal("wizard resolved after only q1")
	case <-time.After(20 * time.Millisecond):
	}

	// Submit step 2 — should complete.
	if err := api.SubmitWizardStep(context.Background(), reqID, "q2", json.RawMessage(`"hello"`), false); err != nil {
		t.Fatalf("SubmitWizardStep q2: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("wizard did not resolve after both questions answered")
	}

	if wizErr != nil {
		t.Fatalf("OpenWizard error: %v", wizErr)
	}
	if wa.Dismissed {
		t.Errorf("wizard should not be dismissed")
	}
	if string(wa.Answers["q1"]) != `"optA"` {
		t.Errorf("q1 answer = %s, want optA", wa.Answers["q1"])
	}
	if string(wa.Answers["q2"]) != `"hello"` {
		t.Errorf("q2 answer = %s, want hello", wa.Answers["q2"])
	}
}

func TestSubmitWizardStep_Dismissed_ReturnsPartial(t *testing.T) {
	em := &fakeEmitter{}
	api := elicit.New(elicit.Config{Emitter: em})
	api.SetContext(context.Background())

	questions := []elicit.WizardQuestion{
		{ID: "q1", Question: "First?", Kind: "text"},
		{ID: "q2", Question: "Second?", Kind: "text"},
	}
	req := elicit.ElicitRequest{Questions: questions}

	var wa elicit.WizardAnswer
	done := make(chan struct{})
	go func() {
		defer close(done)
		wa, _ = api.OpenWizard(context.Background(), req)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(em.snapshot()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	emittedReq := em.snapshot()[0].payload.(elicit.ElicitRequest)
	reqID := emittedReq.RequestID

	_ = api.SubmitWizardStep(context.Background(), reqID, "q1", json.RawMessage(`"partial"`), false)
	_ = api.SubmitWizardStep(context.Background(), reqID, "", nil, true) // dismiss

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("wizard did not resolve on dismissal")
	}

	if !wa.Dismissed {
		t.Errorf("wizard should be dismissed")
	}
	if string(wa.AnsweredSoFar["q1"]) != `"partial"` {
		t.Errorf("answered_so_far[q1] = %s, want partial", wa.AnsweredSoFar["q1"])
	}
}

func TestSubmitWizardStep_ConditionalQuestion_Skipped(t *testing.T) {
	em := &fakeEmitter{}
	api := elicit.New(elicit.Config{Emitter: em})
	api.SetContext(context.Background())

	questions := []elicit.WizardQuestion{
		{ID: "q1", Question: "First?", Kind: "radio"},
		{
			ID:       "q2",
			Question: "Second? (only when q1=yes)",
			Kind:     "text",
			DependsOn: &elicit.WizardDependsOn{
				QuestionID: "q1",
				Condition:  json.RawMessage(`{"equals":"yes"}`),
			},
		},
	}
	req := elicit.ElicitRequest{Questions: questions}

	var wa elicit.WizardAnswer
	done := make(chan struct{})
	go func() {
		defer close(done)
		wa, _ = api.OpenWizard(context.Background(), req)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(em.snapshot()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	emittedReq := em.snapshot()[0].payload.(elicit.ElicitRequest)
	reqID := emittedReq.RequestID

	// Answer q1 with "no" — q2 condition (equals "yes") is NOT met, so q2 is hidden.
	// Answering q1 should complete the wizard immediately.
	_ = api.SubmitWizardStep(context.Background(), reqID, "q1", json.RawMessage(`"no"`), false)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("wizard should complete when only visible question is answered")
	}

	if wa.Dismissed {
		t.Errorf("wizard should not be dismissed")
	}
	if _, ok := wa.Answers["q2"]; ok {
		t.Errorf("q2 should not be in answers (condition not met)")
	}
}

func TestSubmitWizardStep_UnknownRequest_Error(t *testing.T) {
	api := elicit.New(elicit.Config{})
	err := api.SubmitWizardStep(context.Background(), "no-such-id", "q1", nil, false)
	if err != elicit.ErrUnknownRequest {
		t.Errorf("expected ErrUnknownRequest, got %v", err)
	}
}

// ── WP06: deferred mode tests ───────────────────────────────────────────────

func TestRegisterDeferred_ImmediateReturn(t *testing.T) {
	em := &fakeEmitter{}
	api := elicit.New(elicit.Config{Emitter: em})
	api.SetContext(context.Background())

	req := elicit.ElicitRequest{
		Question: "Deploy now?",
		Kind:     "radio",
		Mode:     "deferred",
	}
	result, err := api.RegisterDeferred(context.Background(), "sess-1", req)
	if err != nil {
		t.Fatalf("RegisterDeferred: %v", err)
	}
	if !result.Deferred {
		t.Error("result.Deferred should be true")
	}
	if result.AskID == "" {
		t.Error("result.AskID should not be empty")
	}
	if len(em.snapshot()) == 0 {
		t.Error("should have emitted a deferred event")
	}
}

func TestAnswerDeferred_ReturnsSystemReminder(t *testing.T) {
	em := &fakeEmitter{}
	api := elicit.New(elicit.Config{Emitter: em})
	api.SetContext(context.Background())

	req := elicit.ElicitRequest{Question: "Q?", Kind: "text"}
	dr, _ := api.RegisterDeferred(context.Background(), "sess-1", req)

	reminder, err := api.AnswerDeferred(context.Background(), dr.AskID, "my answer")
	if err != nil {
		t.Fatalf("AnswerDeferred: %v", err)
	}
	if reminder == "" {
		t.Error("system reminder text should not be empty")
	}
	// Check that a DeferredAnswered event was emitted.
	var found bool
	for _, e := range em.snapshot() {
		if e.topic == elicit.TopicElicitDeferredAnswered {
			found = true
		}
	}
	if !found {
		t.Error("should have emitted TopicElicitDeferredAnswered")
	}
}

func TestRegisterDeferred_TooManyPending(t *testing.T) {
	api := elicit.New(elicit.Config{})
	req := elicit.ElicitRequest{Question: "Q?", Kind: "text"}
	for i := 0; i < 5; i++ {
		if _, err := api.RegisterDeferred(context.Background(), "sess-1", req); err != nil {
			t.Fatalf("Register %d: %v", i, err)
		}
	}
	// 6th should fail.
	_, err := api.RegisterDeferred(context.Background(), "sess-1", req)
	if err == nil {
		t.Error("expected error for too many pending, got nil")
	}
}

// ── 01PMGX01 WP06: one pending surface ────────────────────────────────

// The kenaz__ask_user_question tool must resolve through the same
// registry the view exposes on ListPending. Before WP06 the tool's
// pending call lived in a map private to the view; a caller holding the
// ElicitAPI could not see or resolve it by id. This drives the real
// tool — not the Delegate directly — so the whole model-facing path is
// covered end to end.
func TestTool_ResolvesThroughTheSharedPendingSurface(t *testing.T) {
	em := &fakeEmitter{}
	api := elicit.New(elicit.Config{Emitter: em})
	api.SetContext(context.Background())

	tool := askuserquestion.New(askuserquestion.Options{Delegate: api})

	type toolResult struct {
		raw json.RawMessage
		err error
	}
	done := make(chan toolResult, 1)
	go func() {
		raw, err := tool.Call(context.Background(), json.RawMessage(`{
			"question": "Which environment?",
			"kind": "radio",
			"options": [{"value":"prod","label":"Production"}]
		}`))
		done <- toolResult{raw, err}
	}()

	// Poll the *public* pending surface for the tool's ask.
	var reqID string
	deadline := time.Now().Add(2 * time.Second)
	for reqID == "" {
		if time.Now().After(deadline) {
			t.Fatal("tool ask never appeared on ListPending")
		}
		pending, err := api.ListPending(context.Background())
		if err != nil {
			t.Fatalf("ListPending: %v", err)
		}
		for _, p := range pending {
			if p.Question == "Which environment?" {
				if p.Kind != "radio" {
					t.Errorf("kind = %q, want radio", p.Kind)
				}
				if len(p.Options) != 1 || p.Options[0].Value != "prod" {
					t.Errorf("options = %+v", p.Options)
				}
				reqID = p.RequestID
			}
		}
		time.Sleep(time.Millisecond)
	}

	if err := api.SubmitAnswer(context.Background(), reqID, json.RawMessage(`"prod"`), false); err != nil {
		t.Fatalf("SubmitAnswer: %v", err)
	}

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("tool.Call: %v", got.err)
		}
		var r askuserquestion.AskResult
		if err := json.Unmarshal(got.raw, &r); err != nil {
			t.Fatalf("decode tool result: %v", err)
		}
		if r.Cancelled {
			t.Error("Cancelled = true, want false")
		}
		if string(r.Answer) != `"prod"` {
			t.Errorf("answer = %s, want \"prod\"", r.Answer)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tool.Call did not return after SubmitAnswer")
	}

	// Resolved asks leave the pending surface.
	pending, _ := api.ListPending(context.Background())
	for _, p := range pending {
		if p.RequestID == reqID {
			t.Fatal("resolved ask is still listed as pending")
		}
	}
}

// A deferred ask and a blocking dialog live in one store but stay in
// their own lanes: ListPending is blocking-only, ListDeferred is
// deferred-only. Pins the mode split that used to be two packages.
func TestPendingAndDeferredShareOneStoreWithoutBleeding(t *testing.T) {
	em := &fakeEmitter{}
	api := elicit.New(elicit.Config{Emitter: em})
	api.SetContext(context.Background())

	if _, err := api.RegisterDeferred(context.Background(), "sess-1", elicit.ElicitRequest{
		Question: "Later?",
		Kind:     "text",
	}); err != nil {
		t.Fatalf("RegisterDeferred: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = api.OpenDialog(context.Background(), elicitation.Question{
			Text: "Now?", Kind: elicitation.KindText,
		})
	}()
	t.Cleanup(func() {
		pending, _ := api.ListPending(context.Background())
		for _, p := range pending {
			_ = api.SubmitAnswer(context.Background(), p.RequestID, nil, true)
		}
		<-done
	})

	deadline := time.Now().Add(2 * time.Second)
	for {
		pending, _ := api.ListPending(context.Background())
		if len(pending) == 1 {
			if pending[0].Question != "Now?" {
				t.Fatalf("ListPending returned the deferred ask: %+v", pending[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ListPending = %d entries, want exactly the blocking one", len(pending))
		}
		time.Sleep(time.Millisecond)
	}

	deferred, err := api.ListDeferred(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("ListDeferred: %v", err)
	}
	if len(deferred) != 1 || deferred[0].Question.Text != "Later?" {
		t.Fatalf("ListDeferred = %+v, want just the deferred ask", deferred)
	}
	if got, _ := api.ListDeferred(context.Background(), "other-session"); len(got) != 0 {
		t.Fatalf("deferred asks leaked across sessions: %+v", got)
	}
}
