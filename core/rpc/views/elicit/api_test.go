package elicit_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/elicit"
	"github.com/sigil-tech/kaneaz-harness/core/tools/askuserquestion"
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

	var result askuserquestion.AskResult
	var goErr error

	done := make(chan struct{})
	go func() {
		defer close(done)
		result, goErr = api.OpenDialog(context.Background(), args)
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
	if err := json.Unmarshal(result.Answer, &answer); err != nil {
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

	var result askuserquestion.AskResult
	done := make(chan struct{})
	go func() {
		defer close(done)
		result, _ = api.OpenDialog(context.Background(), args)
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
		_, err := api.OpenDialog(ctx, args)
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
