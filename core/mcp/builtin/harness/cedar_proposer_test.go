package harness

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// stubSnippetWriter records the most-recent WritePolicySnippet call.
type stubSnippetWriter struct {
	name string
	body string
	err  error
}

func (s *stubSnippetWriter) WritePolicySnippet(_ context.Context, name, body string) error {
	s.name = name
	s.body = body
	return s.err
}

// stubProposalDispatcher captures dispatched payloads. Thread-safe.
type stubProposalDispatcher struct {
	mu       sync.Mutex
	payloads []CedarProposalPayload
}

func (s *stubProposalDispatcher) Dispatch(_ context.Context, _ string, payload CedarProposalPayload) {
	s.mu.Lock()
	s.payloads = append(s.payloads, payload)
	s.mu.Unlock()
}

func (s *stubProposalDispatcher) first() (CedarProposalPayload, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.payloads) == 0 {
		return CedarProposalPayload{}, false
	}
	return s.payloads[0], true
}

// TestCedarProposer_Accept verifies the happy path: the proposer
// dispatches the payload, the user accepts, and the snippet is written.
func TestCedarProposer_Accept(t *testing.T) {
	t.Parallel()
	writer := &stubSnippetWriter{}
	dispatcher := &stubProposalDispatcher{}

	p := NewCedarProposer(CedarProposerOptions{
		Dispatcher: dispatcher,
		Writer:     writer,
		Timeout:    2 * time.Second,
	})

	ctx := context.Background()
	policyBody := `permit (principal, action, resource);`

	// Run Propose in a goroutine; resolve from the main goroutine.
	errCh := make(chan error, 1)
	go func() {
		errCh <- p.Propose(ctx, "my_policy.cedar", policyBody)
	}()

	// Wait for the dispatcher to receive the payload.
	deadline := time.Now().Add(500 * time.Millisecond)
	var id string
	for time.Now().Before(deadline) {
		if p, ok := dispatcher.first(); ok {
			id = p.RequestID
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("proposer did not dispatch within 500ms")
	}
	if err := p.ResolveProposeRequest(id, CedarProposeAccept); err != nil {
		t.Fatalf("ResolveProposeRequest: %v", err)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("Propose: unexpected error: %v", err)
	}
	if writer.name != "my_policy.cedar" {
		t.Errorf("snippet name = %q, want %q", writer.name, "my_policy.cedar")
	}
	if writer.body != policyBody {
		t.Errorf("snippet body mismatch")
	}
}

// TestCedarProposer_Reject verifies that rejection returns a non-nil error.
func TestCedarProposer_Reject(t *testing.T) {
	t.Parallel()
	writer := &stubSnippetWriter{}
	dispatcher := &stubProposalDispatcher{}

	p := NewCedarProposer(CedarProposerOptions{
		Dispatcher: dispatcher,
		Writer:     writer,
		Timeout:    2 * time.Second,
	})

	ctx := context.Background()
	errCh := make(chan error, 1)
	go func() {
		errCh <- p.Propose(ctx, "my_policy.cedar", `permit(principal,action,resource);`)
	}()

	deadline := time.Now().Add(500 * time.Millisecond)
	var id string
	for time.Now().Before(deadline) {
		if p2, ok := dispatcher.first(); ok {
			id = p2.RequestID
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("proposer did not dispatch within 500ms")
	}
	if err := p.ResolveProposeRequest(id, CedarProposeReject); err != nil {
		t.Fatalf("ResolveProposeRequest: %v", err)
	}

	err := <-errCh
	if err == nil {
		t.Fatal("Propose: expected rejection error, got nil")
	}
	if writer.name != "" {
		t.Error("snippet should NOT be written on rejection")
	}
}

// TestCedarProposer_SyntaxValidation verifies that an invalid Cedar body
// is rejected before dispatching.
func TestCedarProposer_SyntaxValidation(t *testing.T) {
	t.Parallel()
	writer := &stubSnippetWriter{}

	validateErr := errors.New("parse error: unexpected token")
	p := NewCedarProposer(CedarProposerOptions{
		Writer:  writer,
		Timeout: 2 * time.Second,
		Validate: func(_, _ string) error {
			return validateErr
		},
	})

	err := p.Propose(context.Background(), "bad.cedar", "this is not cedar")
	if err == nil {
		t.Fatal("expected error from syntax validation, got nil")
	}
	if !errors.Is(err, validateErr) {
		t.Errorf("error does not wrap validateErr: %v", err)
	}
}

// TestCedarProposer_UnknownResolve verifies that resolving an unknown id
// returns ErrUnknownProposeRequest.
func TestCedarProposer_UnknownResolve(t *testing.T) {
	t.Parallel()
	p := NewCedarProposer(CedarProposerOptions{
		Writer: &stubSnippetWriter{},
	})
	err := p.ResolveProposeRequest("cprop-does-not-exist", CedarProposeAccept)
	if !errors.Is(err, ErrUnknownProposeRequest) {
		t.Errorf("expected ErrUnknownProposeRequest, got %v", err)
	}
}

// TestCedarProposer_NilWriter returns errNotConfigured.
func TestCedarProposer_NilWriter(t *testing.T) {
	t.Parallel()
	p := NewCedarProposer(CedarProposerOptions{})
	err := p.Propose(context.Background(), "x.cedar", `permit(principal,action,resource);`)
	if !errors.Is(err, errNotConfigured) {
		t.Errorf("expected errNotConfigured, got %v", err)
	}
}
