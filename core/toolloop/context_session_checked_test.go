package toolloop

import (
	"context"
	"errors"
	"testing"
)

// TestWithSessionIDChecked (model-harness-toolset-01MHTS001 WP02 security
// review, H1): data-sourced session ids fill an empty ctx and can never
// replace the session a call is already running in.
func TestWithSessionIDChecked(t *testing.T) {
	ctx, err := WithSessionIDChecked(context.Background(), "a")
	if err != nil || SessionIDFromContext(ctx) != "a" {
		t.Fatalf("empty ctx not filled: %v %q", err, SessionIDFromContext(ctx))
	}
	for _, id := range []string{"", "a"} {
		got, err := WithSessionIDChecked(ctx, id)
		if err != nil || SessionIDFromContext(got) != "a" {
			t.Fatalf("id %q: %v %q", id, err, SessionIDFromContext(got))
		}
	}
	got, err := WithSessionIDChecked(ctx, "b")
	if !errors.Is(err, ErrSessionIDMismatch) || SessionIDFromContext(got) != "a" {
		t.Fatalf("different id not refused: %v %q", err, SessionIDFromContext(got))
	}
}
