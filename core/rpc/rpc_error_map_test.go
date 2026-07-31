package rpc

import (
	"errors"
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestMapLLMError_FriendlySurfaced verifies that MapLLMError prefers a
// type's Friendly() rendering over the raw .Message/.Error() fallback,
// for both the two new types (ErrAuth, ErrInvalidRequest) and at least
// one pre-existing attachment error, proving the wiring is general.
func TestMapLLMError_FriendlySurfaced(t *testing.T) {
	t.Run("ErrAuth", func(t *testing.T) {
		err := &corellm.ErrAuth{Status: 401, Message: "invalid api key"}
		got := MapLLMError(err)
		want := err.Friendly()
		if got.Message != want {
			t.Fatalf("Message = %q, want Friendly() text %q", got.Message, want)
		}
		if got.Code != "auth" {
			t.Fatalf("Code = %q, want %q", got.Code, "auth")
		}
	})

	t.Run("ErrInvalidRequest non-overflow", func(t *testing.T) {
		err := &corellm.ErrInvalidRequest{Status: 400, Message: "bad param foo"}
		got := MapLLMError(err)
		want := err.Friendly()
		if got.Message != want {
			t.Fatalf("Message = %q, want Friendly() text %q", got.Message, want)
		}
		if got.Code != "invalid_request" {
			t.Fatalf("Code = %q, want %q", got.Code, "invalid_request")
		}
	})

	t.Run("pre-existing attachment error family lights up too", func(t *testing.T) {
		err := &corellm.ErrAttachmentTooLarge{
			Provider: "openai",
			Mime:     "image/png",
			Given:    10 * 1024 * 1024,
			Cap:      5 * 1024 * 1024,
		}
		got := MapLLMError(err)
		want := err.Friendly()
		if got.Message != want {
			t.Fatalf("Message = %q, want Friendly() text %q", got.Message, want)
		}
	})
}

// TestMapLLMError_ContextOverflowKeepsRawMessage verifies that the
// context_overflow classification path deliberately does NOT substitute
// ErrInvalidRequest's generic Friendly() text (which talks about
// malformed parameters), because that would contradict the
// context_overflow Hint telling the user the conversation is too long.
func TestMapLLMError_ContextOverflowKeepsRawMessage(t *testing.T) {
	err := &corellm.ErrInvalidRequest{Status: 400, Message: "input exceeds maximum context length"}
	got := MapLLMError(err)
	if got.Code != "context_overflow" {
		t.Fatalf("Code = %q, want %q", got.Code, "context_overflow")
	}
	if got.Message != err.Error() {
		t.Fatalf("Message = %q, want raw err.Error() %q (not Friendly())", got.Message, err.Error())
	}
	if got.Message == err.Friendly() {
		t.Fatalf("Message unexpectedly equals the generic Friendly() text — would contradict the context_overflow Hint")
	}
}

// noFriendlyErr is a plain error type with no Friendly() method, used to
// prove that MapLLMError's fallback path is byte-identical to today's
// behavior when no Friendly() implementation is present.
type noFriendlyErr struct{ msg string }

func (e *noFriendlyErr) Error() string { return e.msg }

func TestMapLLMError_NoFriendly_FallsBackUnchanged(t *testing.T) {
	err := &noFriendlyErr{msg: "boom: something went wrong"}
	got := MapLLMError(err)
	if got.Code != "internal" {
		t.Fatalf("Code = %q, want %q", got.Code, "internal")
	}
	if got.Message != err.Error() {
		t.Fatalf("Message = %q, want raw err.Error() %q", got.Message, err.Error())
	}
}

// emptyFriendlyErr implements Friendly() but returns "". MapLLMError must
// treat that as "no usable Friendly() text" and fall back to .Error()
// rather than surfacing an empty message to the frontend.
type emptyFriendlyErr struct{ msg string }

func (e *emptyFriendlyErr) Error() string    { return e.msg }
func (e *emptyFriendlyErr) Friendly() string { return "" }

func TestMapLLMError_EmptyFriendly_FallsBack(t *testing.T) {
	err := &emptyFriendlyErr{msg: "boom: something went wrong"}
	got := MapLLMError(err)
	if got.Message == "" {
		t.Fatalf("Message is empty — must fall back to .Error() instead of surfacing an empty Friendly() string")
	}
	if got.Message != err.Error() {
		t.Fatalf("Message = %q, want raw err.Error() %q", got.Message, err.Error())
	}
}

func TestFriendlyOr_Direct(t *testing.T) {
	if got := friendlyOr(&emptyFriendlyErr{msg: "x"}, "fallback"); got != "fallback" {
		t.Fatalf("friendlyOr with empty Friendly() = %q, want fallback", got)
	}
	authErr := &corellm.ErrAuth{Status: 403, Message: "forbidden"}
	if got := friendlyOr(authErr, "fallback"); got != authErr.Friendly() {
		t.Fatalf("friendlyOr = %q, want Friendly() text %q", got, authErr.Friendly())
	}
	plain := errors.New("plain error")
	if got := friendlyOr(plain, "fallback"); got != "fallback" {
		t.Fatalf("friendlyOr with no Friendly() = %q, want fallback", got)
	}
}
