package toolloop

import (
	"context"
	"testing"
)

func TestSessionIDFromContext_Empty(t *testing.T) {
	if got := SessionIDFromContext(context.Background()); got != "" {
		t.Fatalf("empty ctx: want \"\", got %q", got)
	}
	if got := SessionIDFromContext(nil); got != "" { //nolint:staticcheck // explicitly testing nil
		t.Fatalf("nil ctx: want \"\", got %q", got)
	}
}

func TestWithSessionID_RoundTrip(t *testing.T) {
	ctx := WithSessionID(context.Background(), "sess-123")
	if got := SessionIDFromContext(ctx); got != "sess-123" {
		t.Fatalf("round trip: want \"sess-123\", got %q", got)
	}
}

func TestWithSessionID_EmptyIsNoop(t *testing.T) {
	parent := context.Background()
	ctx := WithSessionID(parent, "")
	if ctx != parent {
		t.Fatalf("empty session id: ctx should be parent unchanged")
	}
	if got := SessionIDFromContext(ctx); got != "" {
		t.Fatalf("empty session id: SessionIDFromContext should return \"\", got %q", got)
	}
}

func TestWithSessionID_OverridesParent(t *testing.T) {
	ctx := WithSessionID(context.Background(), "outer")
	ctx = WithSessionID(ctx, "inner")
	if got := SessionIDFromContext(ctx); got != "inner" {
		t.Fatalf("nested override: want \"inner\", got %q", got)
	}
}
