package registry

import (
	"context"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// segmentsAdapter is a fakeAdapter that places SystemVolatile itself.
type segmentsAdapter struct{ *fakeAdapter }

func (segmentsAdapter) SendsSystemSegments() bool { return true }

// An adapter that sends one system string receives SystemVolatile folded
// into System; an adapter that places the segments itself receives them
// apart.
func TestRegistry_Stream_FoldsSystemSegmentsPerAdapter(t *testing.T) {
	req := llm.GenerationRequest{ProfileID: "p", System: "stable", SystemVolatile: "Current date: 2026-10-09."}

	r, plain := newRegWithResolvedCred(t)
	s, err := r.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_, _ = s.Final()
	if plain.gotReq.System != "stable\n\nCurrent date: 2026-10-09." || plain.gotReq.SystemVolatile != "" {
		t.Errorf("plain adapter got System=%q SystemVolatile=%q, want the folded prompt", plain.gotReq.System, plain.gotReq.SystemVolatile)
	}

	r2, inner := newRegWithResolvedCred(t)
	r2.RegisterAdapter(segmentsAdapter{inner})
	s, err = r2.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_, _ = s.Final()
	if inner.gotReq.System != "stable" || inner.gotReq.SystemVolatile != "Current date: 2026-10-09." {
		t.Errorf("segments adapter got System=%q SystemVolatile=%q, want them apart", inner.gotReq.System, inner.gotReq.SystemVolatile)
	}
}
