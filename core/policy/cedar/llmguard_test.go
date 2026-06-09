package cedar_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

func TestLLMPolicyGuard_AllowsByDefault(t *testing.T) {
	t.Parallel()
	e, err := cedar.NewEngine(cedar.Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	g := cedar.NewLLMPolicyGuard(e)
	err = g.Allow(
		context.Background(),
		llm.GenerationRequest{Model: "gpt-4o"},
		llm.ProviderProfile{Kind: "openai"},
	)
	if err != nil {
		t.Fatalf("default policy should allow model_select, got %v", err)
	}
}

func TestLLMPolicyGuard_Denies(t *testing.T) {
	t.Parallel()
	const src = `
permit (principal, action, resource);

forbid (
    principal == User::"local",
    action == Action::"model_select",
    resource == Model::"openai:gpt-4o"
);
`
	e, err := cedar.NewEngine(cedar.Options{LoadFromDisk: false, IncludeEmbedded: false})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err := e.SetPolicyText("custom.cedar", []byte(src)); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}
	g := cedar.NewLLMPolicyGuard(e)
	err = g.Allow(
		context.Background(),
		llm.GenerationRequest{Model: "gpt-4o"},
		llm.ProviderProfile{Kind: "openai"},
	)
	if err == nil {
		t.Fatal("expected ErrPolicyDenied, got nil")
	}
	var pd *llm.ErrPolicyDenied
	if !errors.As(err, &pd) {
		t.Fatalf("expected *llm.ErrPolicyDenied, got %T", err)
	}
	if pd.Reason == "" {
		t.Fatalf("expected non-empty Reason")
	}
}

func TestLLMPolicyGuard_NilGate_Allows(t *testing.T) {
	t.Parallel()
	g := cedar.NewLLMPolicyGuard(nil)
	err := g.Allow(
		context.Background(),
		llm.GenerationRequest{Model: "x"},
		llm.ProviderProfile{Kind: "y"},
	)
	if err != nil {
		t.Fatalf("nil gate should allow, got %v", err)
	}
}
