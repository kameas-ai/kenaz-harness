// Package smoke is the runnable one-shot check the future CLI / chat
// UI will use to validate an Anthropic (key, profile) pair end to end.
//
// This package lives in a sub-directory rather than inside core/llm/anthropic
// because it imports core/llm/registry which already imports the
// adapter — putting Smoke in the parent package would create an import
// cycle. The seam is intentional: the adapter is the leaf provider
// implementation; smoke wires the adapter through the full Registry
// pipeline without leaking a registry import back into the leaf.
package smoke

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/llm/anthropic"
	"github.com/sigil-tech/kaneaz-harness/core/llm/credref"
	llmregistry "github.com/sigil-tech/kaneaz-harness/core/llm/registry"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
)

// Config configures a one-shot live-API check. All fields are optional
// except Prompt and the API key referenced by EnvVar.
type Config struct {
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

// Result carries the round-trip outcome.
type Result struct {
	Text         string
	FinishReason string
	Usage        corellm.Usage
}

// Run is the runnable one-shot check. It resolves an API key from env,
// builds a fresh Registry, registers the adapter (with optional
// endpoint override), sends a single user message, and returns the
// assembled text. The function is intentionally synchronous so a CLI
// subcommand like `harness probe llm` can wire it without goroutine
// plumbing.
func Run(ctx context.Context, cfg Config) (Result, error) {
	if cfg.Prompt == "" {
		return Result{}, errors.New("smoke: empty prompt")
	}
	if cfg.EnvVar == "" {
		cfg.EnvVar = "ANTHROPIC_API_KEY"
	}
	if os.Getenv(cfg.EnvVar) == "" {
		return Result{}, fmt.Errorf("smoke: %s not set", cfg.EnvVar)
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
		return Result{}, fmt.Errorf("smoke: registry: %w", err)
	}
	if cfg.Endpoint != "" {
		// Override the auto-registered default adapter so the smoke
		// run can target a staging endpoint without touching prod.
		reg.RegisterAdapter(anthropic.New(anthropic.WithEndpoint(cfg.Endpoint)))
	}

	prof := corellm.ProviderProfile{
		ID:    "smoke-anthropic",
		Kind:  anthropic.Kind,
		Model: cfg.Model,
		Cred:  corellm.CredentialReference{Kind: "env", Locator: cfg.EnvVar},
	}
	if err := reg.LoadProfiles([]corellm.ProviderProfile{prof}); err != nil {
		return Result{}, fmt.Errorf("smoke: load profile: %w", err)
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
		return Result{}, fmt.Errorf("smoke: stream: %w", err)
	}

	var b strings.Builder
	for ev := range stream.Events() {
		if ev.Kind == corellm.StreamText {
			b.WriteString(ev.Text)
		}
	}
	resp, err := stream.Final()
	if err != nil {
		return Result{}, fmt.Errorf("smoke: final: %w", err)
	}
	return Result{
		Text:         b.String(),
		FinishReason: resp.FinishReason,
		Usage:        resp.Usage,
	}, nil
}
