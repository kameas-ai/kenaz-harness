package askuserquestion_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/elicitation"
	"github.com/kameas-ai/kenaz-harness/core/tools/askuserquestion"
)

// fakeDelegate implements Delegate for tests.
type fakeDelegate struct {
	answer elicitation.Answer
	err    error

	// UNIT-15 (deferred mode)
	deferredAskID string
	deferredErr   error
	deferredCalls []elicitation.Question
}

func (f *fakeDelegate) OpenDialog(_ context.Context, _ elicitation.Question) (elicitation.Answer, error) {
	return f.answer, f.err
}

func (f *fakeDelegate) Defer(_ context.Context, q elicitation.Question) (string, error) {
	f.deferredCalls = append(f.deferredCalls, q)
	if f.deferredErr != nil {
		return "", f.deferredErr
	}
	return f.deferredAskID, nil
}

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestCall_NotWired_NilDelegate(t *testing.T) {
	tool := askuserquestion.New(askuserquestion.Options{})
	args := mustMarshal(map[string]any{
		"question": "Pick one",
		"kind":     "radio",
		"options":  []map[string]string{{"value": "a", "label": "A"}},
	})
	result, goErr := tool.Call(context.Background(), args)
	if goErr != nil {
		t.Fatalf("unexpected Go error: %v", goErr)
	}
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(result, &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Error != "not_wired" {
		t.Errorf("expected error=not_wired, got %q", e.Error)
	}
}

func TestCall_InvalidKind(t *testing.T) {
	tool := askuserquestion.New(askuserquestion.Options{})
	args := mustMarshal(map[string]any{
		"question": "Pick one",
		"kind":     "bogus",
	})
	result, goErr := tool.Call(context.Background(), args)
	if goErr != nil {
		t.Fatalf("unexpected Go error: %v", goErr)
	}
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(result, &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Error != "invalid_args" {
		t.Errorf("expected error=invalid_args, got %q", e.Error)
	}
}

func TestCall_RadioMissingOptions(t *testing.T) {
	tool := askuserquestion.New(askuserquestion.Options{})
	args := mustMarshal(map[string]any{
		"question": "Pick one",
		"kind":     "radio",
	})
	result, goErr := tool.Call(context.Background(), args)
	if goErr != nil {
		t.Fatalf("unexpected Go error: %v", goErr)
	}
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(result, &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Error != "invalid_args" {
		t.Errorf("expected error=invalid_args, got %q", e.Error)
	}
}

func TestCall_EmptyArgs(t *testing.T) {
	tool := askuserquestion.New(askuserquestion.Options{})
	result, goErr := tool.Call(context.Background(), json.RawMessage(""))
	if goErr != nil {
		t.Fatalf("unexpected Go error: %v", goErr)
	}
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(result, &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Error != "invalid_args" {
		t.Errorf("expected error=invalid_args, got %q", e.Error)
	}
}

func TestCall_DisabledTool(t *testing.T) {
	tool := askuserquestion.New(askuserquestion.Options{
		Enabled: func() bool { return false },
	})
	args := mustMarshal(map[string]any{
		"question": "Pick one",
		"kind":     "text",
	})
	result, goErr := tool.Call(context.Background(), args)
	if goErr != nil {
		t.Fatalf("unexpected Go error: %v", goErr)
	}
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(result, &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Error != "not_wired" {
		t.Errorf("expected error=not_wired, got %q", e.Error)
	}
}

func TestCall_DelegateSuccess(t *testing.T) {
	delegate := &fakeDelegate{
		answer: elicitation.JSONAnswer(json.RawMessage(`"hello"`)),
	}
	tool := askuserquestion.New(askuserquestion.Options{Delegate: delegate})
	args := mustMarshal(map[string]any{
		"question": "Type something",
		"kind":     "text",
	})
	result, goErr := tool.Call(context.Background(), args)
	if goErr != nil {
		t.Fatalf("unexpected Go error: %v", goErr)
	}
	var r askuserquestion.AskResult
	if err := json.Unmarshal(result, &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Cancelled {
		t.Error("expected Cancelled=false")
	}
	var answer string
	if err := json.Unmarshal(r.Answer, &answer); err != nil {
		t.Fatalf("unmarshal answer: %v", err)
	}
	if answer != "hello" {
		t.Errorf("expected answer=hello, got %q", answer)
	}
}

func TestCall_DelegateCancelled(t *testing.T) {
	delegate := &fakeDelegate{
		answer: elicitation.Answer{Cancelled: true},
	}
	tool := askuserquestion.New(askuserquestion.Options{Delegate: delegate})
	args := mustMarshal(map[string]any{
		"question": "Pick a date",
		"kind":     "date",
	})
	result, goErr := tool.Call(context.Background(), args)
	if goErr != nil {
		t.Fatalf("unexpected Go error: %v", goErr)
	}
	var r askuserquestion.AskResult
	if err := json.Unmarshal(result, &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !r.Cancelled {
		t.Error("expected Cancelled=true")
	}
}

func TestCall_DelegateError_ContextCancelled(t *testing.T) {
	delegate := &fakeDelegate{err: context.Canceled}
	tool := askuserquestion.New(askuserquestion.Options{Delegate: delegate})
	args := mustMarshal(map[string]any{
		"question": "Pick a file",
		"kind":     "file",
	})
	result, goErr := tool.Call(context.Background(), args)
	if goErr != nil {
		t.Fatalf("unexpected Go error: %v", goErr)
	}
	var r askuserquestion.AskResult
	if err := json.Unmarshal(result, &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !r.Cancelled {
		t.Error("expected Cancelled=true for context.Canceled")
	}
}

func TestCall_DelegateError_Other(t *testing.T) {
	delegate := &fakeDelegate{err: errors.New("dialog closed unexpectedly")}
	tool := askuserquestion.New(askuserquestion.Options{Delegate: delegate})
	args := mustMarshal(map[string]any{
		"question": "Pick a number",
		"kind":     "number",
	})
	result, goErr := tool.Call(context.Background(), args)
	if goErr != nil {
		t.Fatalf("unexpected Go error: %v", goErr)
	}
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(result, &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Error != "delegate_error" {
		t.Errorf("expected error=delegate_error, got %q", e.Error)
	}
}

func TestToolName(t *testing.T) {
	tool := askuserquestion.New(askuserquestion.Options{})
	if tool.Name() != "kenaz__ask_user_question" {
		t.Errorf("unexpected tool name: %q", tool.Name())
	}
}

func TestInputSchema_ValidJSON(t *testing.T) {
	tool := askuserquestion.New(askuserquestion.Options{})
	schema := tool.InputSchema()
	var obj map[string]any
	if err := json.Unmarshal(schema, &obj); err != nil {
		t.Fatalf("InputSchema is not valid JSON: %v", err)
	}
}

func TestCall_AllKinds_NotWired(t *testing.T) {
	kinds := []struct {
		kind    string
		options []map[string]string
	}{
		{"radio", []map[string]string{{"value": "a", "label": "A"}}},
		{"checkbox", []map[string]string{{"value": "b", "label": "B"}}},
		{"text", nil},
		{"number", nil},
		{"slider", nil},
		{"date", nil},
		{"file", nil},
	}
	for _, tc := range kinds {
		t.Run(tc.kind, func(t *testing.T) {
			tool := askuserquestion.New(askuserquestion.Options{})
			m := map[string]any{
				"question": "test?",
				"kind":     tc.kind,
			}
			if tc.options != nil {
				m["options"] = tc.options
			}
			result, goErr := tool.Call(context.Background(), mustMarshal(m))
			if goErr != nil {
				t.Fatalf("unexpected Go error: %v", goErr)
			}
			var e struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(result, &e); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if e.Error != "not_wired" {
				t.Errorf("kind=%s: expected not_wired, got %q", tc.kind, e.Error)
			}
		})
	}
}

// ── UNIT-15 (automation-actually-runs-01PMZ404, A-12): deferred mode ──

// TestCall_DeferredMode_CallsDeferNotOpenDialog is the core producer-leg
// proof: mode:"deferred" must call Delegate.Defer, never
// Delegate.OpenDialog — the whole point of deferred mode is that the
// tool call does not park.
func TestCall_DeferredMode_CallsDeferNotOpenDialog(t *testing.T) {
	delegate := &fakeDelegate{deferredAskID: "ask-123"}
	tool := askuserquestion.New(askuserquestion.Options{Delegate: delegate})
	args := mustMarshal(map[string]any{
		"question": "Should I proceed?",
		"kind":     "radio",
		"options":  []map[string]string{{"value": "yes", "label": "Yes"}, {"value": "no", "label": "No"}},
		"mode":     "deferred",
	})

	result, goErr := tool.Call(context.Background(), args)
	if goErr != nil {
		t.Fatalf("unexpected Go error: %v", goErr)
	}

	var r askuserquestion.DeferredAskResult
	if err := json.Unmarshal(result, &r); err != nil {
		t.Fatalf("unmarshal: %v (result=%s)", err, result)
	}
	if !r.Deferred {
		t.Error("Deferred = false, want true")
	}
	if r.AskID != "ask-123" {
		t.Errorf("AskID = %q, want ask-123", r.AskID)
	}
	if r.Message == "" {
		t.Error("Message is empty — the model gets no explanation of what happened")
	}

	if len(delegate.deferredCalls) != 1 {
		t.Fatalf("Defer called %d times, want 1", len(delegate.deferredCalls))
	}
	if delegate.deferredCalls[0].Text != "Should I proceed?" {
		t.Errorf("Defer was called with question %q, want the original text", delegate.deferredCalls[0].Text)
	}
}

// TestCall_BlockingMode_Default_StillCallsOpenDialog is the negative
// control: omitting mode (or mode:"blocking") must still take the
// pre-UNIT-15 path through OpenDialog, proving deferred mode is
// additive, not a behavior change on the existing surface.
func TestCall_BlockingMode_Default_StillCallsOpenDialog(t *testing.T) {
	delegate := &fakeDelegate{answer: elicitation.Answer{Text: "yes"}}
	tool := askuserquestion.New(askuserquestion.Options{Delegate: delegate})
	args := mustMarshal(map[string]any{
		"question": "Should I proceed?",
		"kind":     "radio",
		"options":  []map[string]string{{"value": "yes", "label": "Yes"}, {"value": "no", "label": "No"}},
	})

	result, goErr := tool.Call(context.Background(), args)
	if goErr != nil {
		t.Fatalf("unexpected Go error: %v", goErr)
	}
	if len(delegate.deferredCalls) != 0 {
		t.Errorf("Defer called %d times, want 0 — the default mode must not defer", len(delegate.deferredCalls))
	}
	var r askuserquestion.AskResult
	if err := json.Unmarshal(result, &r); err != nil {
		t.Fatalf("unmarshal: %v (result=%s)", err, result)
	}
	if r.Cancelled {
		t.Error("Cancelled = true on a successful blocking answer")
	}
}

// TestCall_DeferredMode_DelegateError surfaces a Defer failure (e.g.
// ErrTooManyPending) as a delegate_error result, never a Go error and
// never a silent success.
func TestCall_DeferredMode_DelegateError(t *testing.T) {
	delegate := &fakeDelegate{deferredErr: elicitation.ErrTooManyPending}
	tool := askuserquestion.New(askuserquestion.Options{Delegate: delegate})
	args := mustMarshal(map[string]any{
		"question": "Q?",
		"kind":     "text",
		"mode":     "deferred",
	})

	result, goErr := tool.Call(context.Background(), args)
	if goErr != nil {
		t.Fatalf("unexpected Go error: %v", goErr)
	}
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(result, &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Error != "delegate_error" {
		t.Errorf("Error = %q, want delegate_error", e.Error)
	}
}
