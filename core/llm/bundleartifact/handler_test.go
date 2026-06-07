package bundleartifact

import (
	"context"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/llm/registry"
)

const goodYAML = `
schema_version: 1
id: anthropic-default
kind: anthropic
model: claude-sonnet-4-7-20260420
auth:
  kind: keychain
  locator: kaneaz/anthropic-api-key
defaults:
  temperature: 0.7
retry:
  max_attempts: 3
  base_ms: 250
  max_ms: 5000
  jitter: full
`

const badPlaintext = `
id: bad
kind: anthropic
model: x
auth:
  kind: env
  locator: sk-ant-secret-leaked-here
`

const badBedrockNoRegion = `
id: b
kind: bedrock
model: anthropic.claude-3-sonnet
auth:
  kind: aws_profile
  locator: default
`

func newReg(t *testing.T) *registry.Registry {
	t.Helper()
	r, err := registry.New(registry.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestHandler_KindLabel(t *testing.T) {
	h := NewHandler(newReg(t))
	if h.Kind() != "llm_provider" {
		t.Fatalf("kind: %s", h.Kind())
	}
}

func TestHandler_ParseValidateActivate_Happy(t *testing.T) {
	r := newReg(t)
	h := NewHandler(r)
	parsed, err := h.Parse([]byte(goodYAML))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Validate(parsed); err != nil {
		t.Fatal(err)
	}
	if err := h.Activate(context.Background(), parsed); err != nil {
		t.Fatal(err)
	}
	got, err := r.Profile("anthropic-default")
	if err != nil || got.Kind != "anthropic" || got.Model == "" {
		t.Fatalf("registered profile: %+v err=%v", got, err)
	}
}

func TestHandler_RejectsPlaintextCredential(t *testing.T) {
	h := NewHandler(newReg(t))
	parsed, err := h.Parse([]byte(badPlaintext))
	if err != nil {
		t.Fatal(err)
	}
	err = h.Validate(parsed)
	if err == nil {
		t.Fatal("expected plaintext rejection")
	}
	if !strings.Contains(err.Error(), "indirect reference") {
		t.Fatalf("expected 'indirect reference' in err, got %v", err)
	}
}

func TestHandler_RejectsBedrockMissingRegion(t *testing.T) {
	h := NewHandler(newReg(t))
	parsed, err := h.Parse([]byte(badBedrockNoRegion))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Validate(parsed); err == nil {
		t.Fatal("expected region rejection")
	}
}

func TestHandler_ActivateCollision(t *testing.T) {
	r := newReg(t)
	h := NewHandler(r)
	parsed, err := h.Parse([]byte(goodYAML))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Activate(context.Background(), parsed); err != nil {
		t.Fatal(err)
	}
	if err := h.Activate(context.Background(), parsed); err == nil {
		t.Fatal("expected collision on duplicate Activate")
	}
}
