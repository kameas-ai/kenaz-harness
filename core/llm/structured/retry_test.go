package structured

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestWithRetry_HappyPath verifies that a first-attempt success returns data
// without calling retryCall.
func TestWithRetry_HappyPath(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)
	validData := []byte(`{"name":"Alice"}`)

	callCount := 0
	call := func(ctx context.Context) ([]byte, error) {
		callCount++
		return validData, nil
	}
	retryCall := func(ctx context.Context, hint string) ([]byte, error) {
		t.Fatal("retryCall should not be called on success")
		return nil, nil
	}

	got, err := WithRetry(context.Background(), schema, call, retryCall, "json_schema", false, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(validData) {
		t.Fatalf("data mismatch: got %s", got)
	}
	if callCount != 1 {
		t.Fatalf("call count = %d, want 1", callCount)
	}
}

// TestWithRetry_FirstFailRetryPass verifies that a first-attempt failure
// triggers retry and succeeds on the second call.
func TestWithRetry_FirstFailRetryPass(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)
	invalidData := []byte(`{"wrong_field":"Alice"}`)
	validData := []byte(`{"name":"Alice"}`)

	retried := false
	call := func(ctx context.Context) ([]byte, error) {
		return invalidData, nil
	}
	retryCall := func(ctx context.Context, hint string) ([]byte, error) {
		retried = true
		if hint == "" {
			t.Fatal("expected non-empty hint in retry call")
		}
		return validData, nil
	}

	got, err := WithRetry(context.Background(), schema, call, retryCall, "json_schema", false, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !retried {
		t.Fatal("retryCall was not called")
	}
	if string(got) != string(validData) {
		t.Fatalf("data mismatch: got %s", got)
	}
}

// TestWithRetry_DoubleFailLenient verifies that when both calls fail,
// an ErrResponseValidationFailed is returned (non-strict mode).
func TestWithRetry_DoubleFailLenient(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","required":["name"]}`)
	badData := []byte(`{"wrong":"field"}`)

	call := func(ctx context.Context) ([]byte, error) { return badData, nil }
	retryCall := func(ctx context.Context, hint string) ([]byte, error) { return badData, nil }

	_, err := WithRetry(context.Background(), schema, call, retryCall, "json_schema", false, 1)
	if err == nil {
		t.Fatal("expected error on double failure")
	}
	var vf *llm.ErrResponseValidationFailed
	if !errors.As(err, &vf) {
		t.Fatalf("expected *llm.ErrResponseValidationFailed, got %T: %v", err, err)
	}
	if vf.Mode != "json_schema" {
		t.Fatalf("Mode = %q, want %q", vf.Mode, "json_schema")
	}
}

// TestWithRetry_StrictMode verifies that StrictValidation=true fails
// immediately without retry.
func TestWithRetry_StrictMode(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","required":["name"]}`)
	badData := []byte(`{"wrong":"field"}`)

	retried := false
	call := func(ctx context.Context) ([]byte, error) { return badData, nil }
	retryCall := func(ctx context.Context, hint string) ([]byte, error) {
		retried = true
		return badData, nil
	}

	_, err := WithRetry(context.Background(), schema, call, retryCall, "json_schema", true, 1)
	if err == nil {
		t.Fatal("expected error in strict mode")
	}
	if retried {
		t.Fatal("retry should not fire in strict mode")
	}
	var vf *llm.ErrResponseValidationFailed
	if !errors.As(err, &vf) {
		t.Fatalf("expected *llm.ErrResponseValidationFailed, got %T", err)
	}
}

// TestWithRetry_MaxRetriesZero verifies that maxRetries=0 disables retry.
func TestWithRetry_MaxRetriesZero(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","required":["name"]}`)
	badData := []byte(`{}`)

	retried := false
	call := func(ctx context.Context) ([]byte, error) { return badData, nil }
	retryCall := func(ctx context.Context, hint string) ([]byte, error) {
		retried = true
		return badData, nil
	}

	_, err := WithRetry(context.Background(), schema, call, retryCall, "json_schema", false, 0)
	if err == nil {
		t.Fatal("expected error when maxRetries=0")
	}
	if retried {
		t.Fatal("retry should not fire when maxRetries=0")
	}
}

// TestWithRetry_NilSchema_PassesOnValidJSON verifies that nil schema
// accepts any valid JSON without retry.
func TestWithRetry_NilSchema_PassesOnValidJSON(t *testing.T) {
	call := func(ctx context.Context) ([]byte, error) { return []byte(`{"any":"json"}`), nil }
	retryCall := func(ctx context.Context, hint string) ([]byte, error) {
		t.Fatal("retryCall should not be called for nil schema")
		return nil, nil
	}

	got, err := WithRetry(context.Background(), nil, call, retryCall, "json", false, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != `{"any":"json"}` {
		t.Fatalf("data mismatch: got %s", got)
	}
}

// TestWithRetry_CallError propagates errors from call without retrying.
func TestWithRetry_CallError(t *testing.T) {
	callErr := errors.New("network error")
	call := func(ctx context.Context) ([]byte, error) { return nil, callErr }
	retryCall := func(ctx context.Context, hint string) ([]byte, error) {
		t.Fatal("retryCall should not fire on call error")
		return nil, nil
	}

	_, err := WithRetry(context.Background(), nil, call, retryCall, "json", false, 1)
	if !errors.Is(err, callErr) {
		t.Fatalf("expected callErr, got %v", err)
	}
}
