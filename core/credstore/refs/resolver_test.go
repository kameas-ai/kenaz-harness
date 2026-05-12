package refs_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/credstore/refs"
	"github.com/sigil-tech/kaneaz-harness/core/event/secretref"
	"github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
)

// fakeEmitter captures audit events for assertions.
type fakeEmitter struct {
	mu     sync.Mutex
	events []secretref.ResolvedEvent
}

func (f *fakeEmitter) EmitSecretResolved(_ context.Context, evt secretref.ResolvedEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, evt)
}

func (f *fakeEmitter) snapshot() []secretref.ResolvedEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]secretref.ResolvedEvent, len(f.events))
	copy(out, f.events)
	return out
}

// fakeLookup is a simple SecretLookup backed by an in-memory map.
type fakeLookup struct {
	data map[string][]byte
}

func (f *fakeLookup) Use(_ context.Context, locator string, op func([]byte) error) error {
	v, ok := f.data[locator]
	if !ok {
		return refs.ErrUnknownLocator
	}
	buf := make([]byte, len(v))
	copy(buf, v)
	defer func() {
		for i := range buf {
			buf[i] = 0
		}
	}()
	return op(buf)
}

func makeResolver(lookup *fakeLookup, gate cedar.Gate, budget *refs.Budget, emitter refs.AuditEmitter) *refs.Resolver {
	return refs.NewResolver(refs.ResolverOptions{
		Lookup:    lookup,
		Gate:      gate,
		Budget:    budget,
		Emitter:   emitter,
		SessionID: "ses_test",
		Agent:     "chat",
	})
}

// TestResolve_HappyPath verifies a straightforward resolution.
func TestResolve_HappyPath(t *testing.T) {
	em := &fakeEmitter{}
	lookup := &fakeLookup{data: map[string][]byte{
		"user:foo": []byte("super-secret"),
	}}
	b := refs.NewBudget(10)
	r := makeResolver(lookup, nil, b, em)

	ref := refs.Reference{Locator: "user:foo"}
	rctx := cedar.ResolveContext{ToolName: "web_fetch", DestinationHost: "api.example.com"}
	resolved, dec, err := r.Resolve(context.Background(), ref, rctx)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(string(resolved), "super-secret") {
		t.Errorf("resolved bytes wrong: %q", resolved)
	}
	// Cleanup
	refs.ZeroBuffer(resolved)

	// Decision should be allow (nil gate = default-allow).
	if dec.Outcome != cedar.Allow && dec.Outcome != cedar.NotApplicable {
		t.Errorf("unexpected outcome: %v", dec.Outcome)
	}

	// Exactly one audit event emitted.
	evts := em.snapshot()
	if len(evts) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(evts))
	}
	if evts[0].Locator != "user:foo" {
		t.Errorf("event.Locator = %q", evts[0].Locator)
	}
	if evts[0].Outcome != secretref.OutcomePermit {
		t.Errorf("event.Outcome = %q", evts[0].Outcome)
	}
}

// TestResolve_UnknownLocator verifies ErrUnknownLocator path.
func TestResolve_UnknownLocator(t *testing.T) {
	em := &fakeEmitter{}
	lookup := &fakeLookup{data: map[string][]byte{}}
	r := makeResolver(lookup, nil, nil, em)

	ref := refs.Reference{Locator: "user:nonexistent"}
	_, _, err := r.Resolve(context.Background(), ref, cedar.ResolveContext{ToolName: "bash"})
	if !errors.Is(err, refs.ErrUnknownLocator) {
		t.Fatalf("expected ErrUnknownLocator, got %v", err)
	}

	evts := em.snapshot()
	if len(evts) != 1 {
		t.Fatalf("expected 1 audit event on unknown locator, got %d", len(evts))
	}
	if evts[0].Outcome != secretref.OutcomeUnknownLocator {
		t.Errorf("expected OutcomeUnknownLocator, got %q", evts[0].Outcome)
	}
}

// TestResolve_BudgetExhausted verifies budget enforcement.
func TestResolve_BudgetExhausted(t *testing.T) {
	em := &fakeEmitter{}
	lookup := &fakeLookup{data: map[string][]byte{
		"user:bar": []byte("secret"),
	}}
	b := refs.NewBudget(1)
	r := makeResolver(lookup, nil, b, em)
	ref := refs.Reference{Locator: "user:bar"}
	rctx := cedar.ResolveContext{ToolName: "bash"}

	// First resolve succeeds.
	resolved, _, err := r.Resolve(context.Background(), ref, rctx)
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	refs.ZeroBuffer(resolved)

	// Second resolve hits budget.
	_, _, err = r.Resolve(context.Background(), ref, rctx)
	if !errors.Is(err, refs.ErrBudgetExhausted) {
		t.Fatalf("expected ErrBudgetExhausted, got %v", err)
	}
}

// TestSubstitute_NoReferences fast path.
func TestSubstitute_NoReferences(t *testing.T) {
	r := makeResolver(&fakeLookup{data: nil}, nil, nil, nil)
	out, decs, err := r.Substitute(context.Background(), "no references here", cedar.ResolveContext{})
	if err != nil {
		t.Fatalf("Substitute: %v", err)
	}
	if out != "no references here" {
		t.Errorf("output changed: %q", out)
	}
	if len(decs) != 0 {
		t.Errorf("expected 0 decisions, got %d", len(decs))
	}
}

// TestSubstitute_MultiReference verifies multiple references in one string.
func TestSubstitute_MultiReference(t *testing.T) {
	em := &fakeEmitter{}
	lookup := &fakeLookup{data: map[string][]byte{
		"user:foo": []byte("FOOTOKEN"),
		"user:bar": []byte("BARTOKEN"),
	}}
	r := makeResolver(lookup, nil, nil, em)

	input := "Authorization: @secret:user:foo, X-Bar: @secret:user:bar"
	out, decs, err := r.Substitute(context.Background(), input, cedar.ResolveContext{ToolName: "web_fetch"})
	if err != nil {
		t.Fatalf("Substitute: %v", err)
	}
	if !strings.Contains(out, "FOOTOKEN") {
		t.Errorf("FOOTOKEN missing in: %q", out)
	}
	if !strings.Contains(out, "BARTOKEN") {
		t.Errorf("BARTOKEN missing in: %q", out)
	}
	if len(decs) != 2 {
		t.Errorf("expected 2 decisions, got %d", len(decs))
	}

	// Two audit events.
	evts := em.snapshot()
	if len(evts) != 2 {
		t.Errorf("expected 2 audit events, got %d", len(evts))
	}
}

// TestSubstitute_SanitizerPopulated verifies the sanitizer gets
// fingerprints when resolution succeeds.
func TestSubstitute_SanitizerPopulated(t *testing.T) {
	lookup := &fakeLookup{data: map[string][]byte{
		"user:tok": []byte("MYTOKEN"),
	}}
	r := makeResolver(lookup, nil, nil, nil)

	san := refs.NewSanitizer()
	ctx := refs.WithTurnSanitizer(context.Background(), san)

	out, _, err := r.Substitute(ctx, "Bearer @secret:user:tok", cedar.ResolveContext{ToolName: "web_fetch"})
	if err != nil {
		t.Fatalf("Substitute: %v", err)
	}
	if !strings.Contains(out, "MYTOKEN") {
		t.Errorf("expected MYTOKEN in output, got %q", out)
	}

	// Sanitizer should now have one entry.
	if san.Len() != 1 {
		t.Errorf("expected 1 sanitizer entry, got %d", san.Len())
	}

	// Sanitize should redact.
	redacted := san.Sanitize([]byte("response body contains MYTOKEN here"))
	if strings.Contains(string(redacted), "MYTOKEN") {
		t.Errorf("sanitizer did not redact: %s", redacted)
	}
	if !strings.Contains(string(redacted), "[redacted: user:tok]") {
		t.Errorf("expected redaction placeholder: %s", redacted)
	}
}

// TestHasReference verifies the fast-path helper.
func TestHasReference(t *testing.T) {
	if !refs.HasReference("Bearer @secret:user:foo") {
		t.Error("expected true")
	}
	if refs.HasReference("no references") {
		t.Error("expected false")
	}
}
