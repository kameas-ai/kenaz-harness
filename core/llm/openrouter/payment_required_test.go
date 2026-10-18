package openrouter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestAdapter_Stream_402IsPaymentRequired is the dogfood 2026-10-07 case:
// an OpenRouter account out of credits answers 402 on the response line.
// It must type as ErrPaymentRequired (user_actionable) so the chat
// surface says "Out of credits with OpenRouter" and never auto-retries.
func TestAdapter_Stream_402IsPaymentRequired(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = io.WriteString(w, `{"error":{"message":"This request requires more credits, or fewer max_tokens.","code":402}}`)
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	_, err := a.Stream(context.Background(), req, prof, []byte("sk-or-test"))
	var pay *llm.ErrPaymentRequired
	if !errors.As(err, &pay) || pay.Status != 402 {
		t.Fatalf("want *llm.ErrPaymentRequired status 402, got %T %v", err, err)
	}
	f := llm.ClassifyFailure(err, Kind)
	if f.Class != llm.FailureUserActionable || f.Summary != "Out of credits with OpenRouter" {
		t.Fatalf("classification: %+v", f)
	}
}

// TestClassifyStreamErrorFrame: OpenRouter forwards upstream failures as
// an in-stream {"error":{"code":N}} frame on an HTTP 200. The code is
// classified like the same status on the response line; a frame without
// a numeric code keeps the historical transient typing.
func TestClassifyStreamErrorFrame(t *testing.T) {
	cases := []struct {
		code any
		want llm.FailureClass
	}{
		{float64(402), llm.FailureUserActionable},
		{float64(401), llm.FailureUserActionable},
		{float64(429), llm.FailureTransient},
		{float64(502), llm.FailureTransient},
		{nil, llm.FailureTransient},
		{"server_error", llm.FailureTransient},
	}
	for _, tc := range cases {
		err := classifyStreamErrorFrame(tc.code, "upstream failed")
		if got := llm.ClassifyFailure(err, Kind).Class; got != tc.want {
			t.Fatalf("code %v: class %s want %s (%T)", tc.code, got, tc.want, err)
		}
	}
}
