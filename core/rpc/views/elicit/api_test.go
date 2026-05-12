package elicit_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/elicit"
	"github.com/sigil-tech/kaneaz-harness/core/tools/askuserquestion"
)

// fakeEmitter records Emit calls for assertions.
type fakeEmitter struct {
	emitted []emitRecord
}

type emitRecord struct {
	topic   string
	payload any
}

func (f *fakeEmitter) Emit(_ context.Context, topic string, payload any) {
	f.emitted = append(f.emitted, emitRecord{topic: topic, payload: payload})
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
	if len(em.emitted) != 1 {
		t.Fatalf("expected 1 emit, got %d", len(em.emitted))
	}
	if em.emitted[0].topic != elicit.TopicElicitPending {
		t.Errorf("expected topic=%q, got %q", elicit.TopicElicitPending, em.emitted[0].topic)
	}

	req, ok := em.emitted[0].payload.(elicit.ElicitRequest)
	if !ok {
		t.Fatalf("payload is not ElicitRequest: %T", em.emitted[0].payload)
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

	if len(em.emitted) == 0 {
		t.Fatal("no emit recorded")
	}
	req := em.emitted[0].payload.(elicit.ElicitRequest)

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
