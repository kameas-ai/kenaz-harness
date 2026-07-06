package chat

import (
	"context"
	"errors"
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestIsContextOverflowError classifies provider errors for FR-005.
func TestIsContextOverflowError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"auth error", &corellm.ErrAuth{Status: 401}, false},
		{"transient 429", &corellm.ErrTransient{Status: 429}, false},
		{"invalid request 400 context", &corellm.ErrInvalidRequest{Status: 400, Message: "This model's maximum context length is 8192 tokens"}, true},
		{"invalid request 400 token", &corellm.ErrInvalidRequest{Status: 400, Message: "too many tokens in the request"}, true},
		{"invalid request 400 length", &corellm.ErrInvalidRequest{Status: 400, Message: "message exceeds maximum length"}, true},
		{"invalid request 400 too long", &corellm.ErrInvalidRequest{Status: 400, Message: "input is too long"}, true},
		{"invalid request 400 maximum context", &corellm.ErrInvalidRequest{Status: 400, Message: "exceeds maximum context"}, true},
		{"invalid request 400 unrelated", &corellm.ErrInvalidRequest{Status: 400, Message: "invalid model specified"}, false},
		{"transient 413 request entity", &corellm.ErrTransient{Status: 413, Message: "request too large"}, true},
		{"generic error", errors.New("something went wrong"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isContextOverflowError(tc.err)
			if got != tc.want {
				t.Errorf("isContextOverflowError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestAttemptOverflowRecovery_NilDeps verifies that nil deps returns an error
// (no panic) so the run falls through to backend-error.
func TestAttemptOverflowRecovery_NilDeps(t *testing.T) {
	err := attemptOverflowRecovery(context.Background(), "sess", "prof", "model", nil)
	if err == nil {
		t.Error("expected error with nil CompactionDeps")
	}
}

// TestAttemptOverflowRecovery_CompactCalled verifies that when deps are wired
// the Engine.Compact is called (FR-005 acceptance: overflow compacts-and-retries).
func TestAttemptOverflowRecovery_CompactCalled(t *testing.T) {
	engine := &fakeCompactionEngine{}
	deps := &CompactionDeps{
		Engine: engine,
	}

	err := attemptOverflowRecovery(context.Background(), "sess-overflow", "prof", "model", deps)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if engine.compactCount() != 1 {
		t.Errorf("expected 1 Compact call, got %d", engine.compactCount())
	}
}
