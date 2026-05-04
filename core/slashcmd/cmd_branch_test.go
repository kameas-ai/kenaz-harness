package slashcmd

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// fakeBranches is a deterministic BranchGateway for tests.
type fakeBranches struct {
	mu sync.Mutex

	// CreateHandle is the BranchHandle CreateBranch returns on success.
	CreateHandle BranchHandle
	// CreateErr is returned by CreateBranch when non-nil.
	CreateErr error
	// CreateCalls records each (parentSessionID, modelID) tuple.
	CreateCalls []struct{ Parent, Model string }

	// Recs is returned by RecommendModels.
	Recs BranchRecommendations
	// RecsErr is returned by RecommendModels when non-nil.
	RecsErr error
	// RecCalls records each parentSessionID passed to RecommendModels.
	RecCalls []string
}

func (f *fakeBranches) CreateBranch(_ context.Context, parent, model string) (BranchHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.CreateCalls = append(f.CreateCalls, struct{ Parent, Model string }{parent, model})
	if f.CreateErr != nil {
		return BranchHandle{}, f.CreateErr
	}
	return f.CreateHandle, nil
}

func (f *fakeBranches) RecommendModels(_ context.Context, parent string) (BranchRecommendations, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.RecCalls = append(f.RecCalls, parent)
	if f.RecsErr != nil {
		return BranchRecommendations{}, f.RecsErr
	}
	return f.Recs, nil
}

func TestBranch_CreatesWithModelID(t *testing.T) {
	t.Parallel()
	br := &fakeBranches{
		CreateHandle: BranchHandle{
			BranchID:       "br-1",
			ChildSessionID: "sess-child",
			ProviderID:     "openai",
			ModelID:        "gpt-4o-mini",
		},
	}
	r, _ := NewRegistry(Deps{Branches: br})
	res, err := r.Execute(context.Background(), "sess-parent", "/branch gpt-4o-mini")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Kind != ResultKindInfo {
		t.Errorf("Kind = %q, want info", res.Kind)
	}
	for _, want := range []string{"br-1", "sess-child", "gpt-4o-mini"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("Text missing %q: %q", want, res.Text)
		}
	}
	if got := res.Metadata["branchId"]; got != "br-1" {
		t.Errorf("Metadata branchId = %v, want br-1", got)
	}
	if got := res.Metadata["childSessionId"]; got != "sess-child" {
		t.Errorf("Metadata childSessionId = %v, want sess-child", got)
	}
	if got := res.Metadata[MetaKeyProviderID]; got != "openai" {
		t.Errorf("Metadata providerId = %v, want openai", got)
	}
	if got := res.Metadata[MetaKeyModelID]; got != "gpt-4o-mini" {
		t.Errorf("Metadata modelId = %v, want gpt-4o-mini", got)
	}
	if len(br.CreateCalls) != 1 {
		t.Fatalf("CreateBranch calls = %d, want 1", len(br.CreateCalls))
	}
	if br.CreateCalls[0].Parent != "sess-parent" || br.CreateCalls[0].Model != "gpt-4o-mini" {
		t.Errorf("CreateCalls[0] = %+v", br.CreateCalls[0])
	}
}

func TestBranch_NoArgsListsRecommendations(t *testing.T) {
	t.Parallel()
	br := &fakeBranches{
		Recs: BranchRecommendations{
			Smaller: BranchModel{ProviderID: "openai", ModelID: "gpt-4o-mini", Reason: "cheaper"},
			Larger:  BranchModel{ProviderID: "anthropic", ModelID: "claude-opus-4", Reason: "harder reasoning"},
		},
	}
	r, _ := NewRegistry(Deps{Branches: br})
	res, err := r.Execute(context.Background(), "sess-1", "/branch")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Kind != ResultKindInfo {
		t.Errorf("Kind = %q, want info", res.Kind)
	}
	for _, want := range []string{"smaller", "gpt-4o-mini", "larger", "claude-opus-4"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("Text missing %q: %q", want, res.Text)
		}
	}
	if len(br.CreateCalls) != 0 {
		t.Errorf("listing path should not call CreateBranch: %v", br.CreateCalls)
	}
	if len(br.RecCalls) != 1 || br.RecCalls[0] != "sess-1" {
		t.Errorf("RecCalls = %v", br.RecCalls)
	}
}

func TestBranch_NoRecommendations(t *testing.T) {
	t.Parallel()
	br := &fakeBranches{}
	r, _ := NewRegistry(Deps{Branches: br})
	res, _ := r.Execute(context.Background(), "sess-1", "/branch")
	if res.Kind != ResultKindInfo {
		t.Errorf("Kind = %q, want info", res.Kind)
	}
	if !strings.Contains(res.Text, "no recommended models") {
		t.Errorf("Text missing fallback: %q", res.Text)
	}
}

func TestBranch_GatewayUnwired(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(Deps{})
	res, _ := r.Execute(context.Background(), "s", "/branch claude-3")
	if res.Kind != ResultKindError {
		t.Errorf("Kind = %q, want error", res.Kind)
	}
	if !strings.Contains(res.Text, "not wired") {
		t.Errorf("Text missing not-wired hint: %q", res.Text)
	}
}

func TestBranch_NoSession(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(Deps{Branches: &fakeBranches{}})
	res, _ := r.Execute(context.Background(), "", "/branch claude-3")
	if res.Kind != ResultKindError {
		t.Errorf("Kind = %q, want error", res.Kind)
	}
	if !strings.Contains(res.Text, "no active session") {
		t.Errorf("Text missing no-session hint: %q", res.Text)
	}
}

func TestBranch_BackendError(t *testing.T) {
	t.Parallel()
	boom := errors.New("storage offline")
	br := &fakeBranches{CreateErr: boom}
	r, _ := NewRegistry(Deps{Branches: br})
	res, err := r.Execute(context.Background(), "s", "/branch x")
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want %v", err, boom)
	}
	if res.Kind != ResultKindError {
		t.Errorf("Kind = %q, want error", res.Kind)
	}
}
