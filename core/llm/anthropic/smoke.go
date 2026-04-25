package anthropic

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/llm/credref"
	llmregistry "github.com/sigil-tech/kaneaz-harness/core/llm/registry"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
)

// SmokeConfig configures a one-shot live-API check. All fields are
// optional except EnvVar (resolves the API key) and Prompt.
type SmokeConfig struct {
	// Model is the Anthropic model id. Defaults to claude-haiku-4-5.
	Model string
	// EnvVar names the environment variable holding the API key.
	// Defaults to ANTHROPIC_API_KEY.
	EnvVar string
	// Prompt is the single user message sent to the model.
	Prompt string
	// MaxTokens caps generation length. Defaults to 64.
	MaxTokens int
	// Timeout caps the entire round-trip. Defaults to 30s.
	Timeout time.Duration
	// Endpoint overrides the Messages API URL. Useful for staging
	// proxies; production callers leave it blank.
	Endpoint string
}

// SmokeResult carries the round-trip outcome.
type SmokeResult struct {
	Text         string
	FinishReason string
	Usage        corellm.Usage
}

// Smoke is a runnable one-shot check that resolves an API key from
// env, registers the adapter against a fresh Registry, sends a single
// user message, and returns the assembled text. It is the callable
// the future CLI / chat UI will reuse to validate a new
// (key, profile) pair without booting the full harness.
//
// The function is intentionally synchronous and returns Result rather
// than streaming so a caller can wire it into a `harness probe llm`
// CLI subcommand without any goroutine plumbing.
func Smoke(ctx context.Context, cfg SmokeConfig) (SmokeResult, error) {
	if cfg.Prompt == "" {
		return SmokeResult{}, errors.New("anthropic.Smoke: empty prompt")
	}
	if cfg.EnvVar == "" {
		cfg.EnvVar = "ANTHROPIC_API_KEY"
	}
	if os.Getenv(cfg.EnvVar) == "" {
		return SmokeResult{}, fmt.Errorf("anthropic.Smoke: %s not set", cfg.EnvVar)
	}
	if cfg.Model == "" {
		cfg.Model = "claude-haiku-4-5"
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 64
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}

	reg, err := llmregistry.New(llmregistry.Options{
		Resolver: credref.New(secrets.NewMemoryBackend()),
	})
	if err != nil {
		return SmokeResult{}, fmt.Errorf("anthropic.Smoke: registry: %w", err)
	}
	opts := []Option{}
	if cfg.Endpoint != "" {
		opts = append(opts, WithEndpoint(cfg.Endpoint))
	}
	// Re-register so the smoke function can target a non-default endpoint
	// even though registry.New auto-registered the default adapter.
	reg.RegisterAdapter(New(opts...))

	prof := corellm.ProviderProfile{
		ID:    "smoke-anthropic",
		Kind:  Kind,
		Model: cfg.Model,
		Cred:  corellm.CredentialReference{Kind: "env", Locator: cfg.EnvVar},
	}
	if err := reg.LoadProfiles([]corellm.ProviderProfile{prof}); err != nil {
		return SmokeResult{}, fmt.Errorf("anthropic.Smoke: load profile: %w", err)
	}

	callCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	stream, err := reg.Stream(callCtx, corellm.GenerationRequest{
		ProfileID: prof.ID,
		Messages: []corellm.Message{
			{Role: corellm.RoleUser, Content: []corellm.ContentPart{{Type: "text", Text: cfg.Prompt}}},
		},
		Params: map[string]any{"max_tokens": cfg.MaxTokens},
	})
	if err != nil {
		return SmokeResult{}, fmt.Errorf("anthropic.Smoke: stream: %w", err)
	}

	var b strings.Builder
	for ev := range stream.Events() {
		if ev.Kind == corellm.StreamText {
			b.WriteString(ev.Text)
		}
	}
	resp, err := stream.Final()
	if err != nil {
		return SmokeResult{}, fmt.Errorf("anthropic.Smoke: final: %w", err)
	}
	return SmokeResult{
		Text:         b.String(),
		FinishReason: resp.FinishReason,
		Usage:        resp.Usage,
	}, nil
}
