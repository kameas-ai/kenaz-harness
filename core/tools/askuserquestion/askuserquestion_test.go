package askuserquestion_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/tools/askuserquestion"
)

// fakeDelegate implements Delegate for tests.
type fakeDelegate struct {
	result askuserquestion.AskResult
	err    error
}

func (f *fakeDelegate) OpenDialog(_ context.Context, _ askuserquestion.AskArgs) (askuserquestion.AskResult, error) {
	return f.result, f.err
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
	answerJSON := json.RawMessage(`"hello"`)
	delegate := &fakeDelegate{
		result: askuserquestion.AskResult{Answer: answerJSON, Cancelled: false},
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
	answerJSON := json.RawMessage("null")
	delegate := &fakeDelegate{
		result: askuserquestion.AskResult{Answer: answerJSON, Cancelled: true},
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
	if tool.Name() != "kaneaz__ask_user_question" {
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
