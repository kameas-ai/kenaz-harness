package kinds_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	bundle "github.com/kameas-ai/kenaz-harness/core/bundle"
	"github.com/kameas-ai/kenaz-harness/core/bundle/kinds"
	"github.com/kameas-ai/kenaz-harness/core/bundle/kinds/testkind"
)

func TestRegisterAndLookup(t *testing.T) {
	r := kinds.NewRegistry()
	h := &testkind.Handler{}
	if err := r.Register(h); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := r.Lookup(testkind.Kind)
	if !ok {
		t.Fatalf("Lookup not found")
	}
	if got.Kind() != testkind.Kind {
		t.Errorf("Lookup kind=%s, want %s", got.Kind(), testkind.Kind)
	}
}

func TestRegisterDuplicate(t *testing.T) {
	r := kinds.NewRegistry()
	if err := r.Register(&testkind.Handler{}); err != nil {
		t.Fatalf("Register 1: %v", err)
	}
	err := r.Register(&testkind.Handler{})
	if !errors.Is(err, bundle.ErrDuplicateKindRegistration) {
		t.Errorf("err=%v, want ErrDuplicateKindRegistration", err)
	}
}

func TestLookupUnknown(t *testing.T) {
	r := kinds.NewRegistry()
	if h, ok := r.Lookup("never-registered"); ok || h != nil {
		t.Errorf("Lookup returned %v,%v for unknown kind", h, ok)
	}
}

func TestList(t *testing.T) {
	r := kinds.NewRegistry()
	if err := r.Register(&testkind.Handler{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got := r.List()
	if len(got) != 1 || got[0] != testkind.Kind {
		t.Errorf("List=%v, want [%s]", got, testkind.Kind)
	}
}

func TestRegisterNil(t *testing.T) {
	r := kinds.NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Errorf("Register(nil) returned nil error")
	}
}

func TestConcurrentRegister(t *testing.T) {
	// Multiple goroutines register handlers with distinct kind ids; the
	// race detector should be clean. Run via `go test -race`.
	r := kinds.NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = r.Register(&renamedHandler{name: kindIDForIdx(n)})
		}(i)
	}
	wg.Wait()
	if len(r.List()) != 10 {
		t.Errorf("List=%d entries, want 10", len(r.List()))
	}
}

func kindIDForIdx(n int) string {
	return "kind_" + string(rune('a'+n))
}

// renamedHandler is a minimal handler that just supplies a custom Kind()
// name. Used by the concurrent-registration test.
type renamedHandler struct {
	name string
}

func (r *renamedHandler) Kind() string                                                          { return r.name }
func (r *renamedHandler) ParamSchema() []byte                                                   { return nil }
func (r *renamedHandler) Parse(_ context.Context, _ kinds.ArtifactSource) (kinds.Parsed, error) { return nil, nil }
func (r *renamedHandler) Validate(_ context.Context, _ kinds.Parsed) error                      { return nil }
func (r *renamedHandler) Activate(_ context.Context, _ kinds.Parsed, _ kinds.Environment) (kinds.Activation, error) {
	return nil, nil
}
func (r *renamedHandler) Deactivate(_ context.Context, _ kinds.Activation) error { return nil }
