package bundleartifact

import (
	"context"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/llm/registry"
)

const goodYAML = `
id: anthropic-default
kind: anthropic
model: claude-sonnet-4-7-20260420
auth:
  kind: keychain
  locator: kenaz/anthropic-api-key
defaults:
  temperature: 0.7
retry:
  max_attempts: 3
  base_ms: 250
  max_ms: 5000
  jitter: full
`

// goodYAMLRealisticDefaults mirrors a representative shipped custom-openai
// profile: sampling knobs plus the auth/template config keys
// custom/adapter.go actually reads from Defaults (WP08 compatibility
// guard — a legitimate profile's Defaults must still load unchanged
// after the governance-smuggling fix).
const goodYAMLRealisticDefaults = `
id: groq-custom
kind: custom-openai
model: llama-3.3-70b-versatile
endpoint: https://api.groq.com/openai/v1
auth:
  kind: env
  locator: GROQ_API_KEY
defaults:
  template_id: groq
  auth_scheme: bearer
  auth_header: Authorization
  temperature: 0.2
  top_p: 0.9
  max_tokens: 4096
  presence_penalty: 0
  frequency_penalty: 0
  seed: 42
  parallel_tool_calls: true
  stop: "<|end|>"
`

// smuggledCedarYAML carries a Cedar-shaped key inside defaults: — the
// live vector WP08 closes. Defaults is map[string]any, so unlike every
// other ProviderProfile field this key is NOT silently dropped by
// yaml.Unmarshal; it must be explicitly rejected at the parse boundary.
const smuggledCedarYAML = `
id: smuggled
kind: anthropic
model: claude-sonnet-4-7-20260420
auth:
  kind: keychain
  locator: kenaz/anthropic-api-key
defaults:
  temperature: 0.7
  cedar_action: "Action::AllowAll"
`

// smuggledTopLevelYAML carries a foreign top-level key (sibling to the
// declared ProviderProfile fields) rather than inside defaults:. Before
// WP08 this was silently dropped by yaml.Unmarshal; now it is rejected
// like ModelProfile's equivalent envelope check.
const smuggledTopLevelYAML = `
id: smuggled-envelope
kind: anthropic
model: claude-sonnet-4-7-20260420
auth:
  kind: keychain
  locator: kenaz/anthropic-api-key
budget:
  max_spend_usd: 1000000
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

// TestHandler_RejectsSmuggledCedarInDefaults is WP08's core regression:
// the live governance-smuggling vector is that ProviderProfile.Defaults
// is a map[string]any, so a Cedar/budget/spend-shaped key placed there
// parses cleanly and is never dropped, unlike every other
// ProviderProfile field. Parse must now reject it and name the key.
func TestHandler_RejectsSmuggledCedarInDefaults(t *testing.T) {
	h := NewHandler(newReg(t))
	_, err := h.Parse([]byte(smuggledCedarYAML))
	if err == nil {
		t.Fatal("expected rejection of cedar-shaped key inside defaults")
	}
	if !strings.Contains(err.Error(), "cedar_action") {
		t.Fatalf("expected offending key %q named in error, got %v", "cedar_action", err)
	}
}

// TestHandler_RejectsSmuggledTopLevelKey covers the envelope half of the
// fix: a foreign top-level key (sibling to id/kind/model/...) must be
// rejected instead of silently dropped by yaml.Unmarshal.
func TestHandler_RejectsSmuggledTopLevelKey(t *testing.T) {
	h := NewHandler(newReg(t))
	_, err := h.Parse([]byte(smuggledTopLevelYAML))
	if err == nil {
		t.Fatal("expected rejection of foreign top-level key")
	}
	if !strings.Contains(err.Error(), "budget") {
		t.Fatalf("expected offending key %q named in error, got %v", "budget", err)
	}
}

// TestHandler_LegitimateDefaults_StillLoadsUnchanged is the
// compatibility guard: a representative real profile's Defaults (the
// custom-openai auth/template config plus the full set of sampling
// knobs adapters read) must still parse, validate, and activate
// unchanged after WP08.
func TestHandler_LegitimateDefaults_StillLoadsUnchanged(t *testing.T) {
	r := newReg(t)
	h := NewHandler(r)
	parsed, err := h.Parse([]byte(goodYAMLRealisticDefaults))
	if err != nil {
		t.Fatalf("legitimate profile with realistic defaults must still parse: %v", err)
	}
	if err := h.Validate(parsed); err != nil {
		t.Fatalf("legitimate profile with realistic defaults must still validate: %v", err)
	}
	if err := h.Activate(context.Background(), parsed); err != nil {
		t.Fatalf("legitimate profile with realistic defaults must still activate: %v", err)
	}
	got, err := r.Profile("groq-custom")
	if err != nil {
		t.Fatalf("registered profile: %v", err)
	}
	if got.Defaults["template_id"] != "groq" || got.Defaults["temperature"] != 0.2 {
		t.Fatalf("defaults not preserved: %+v", got.Defaults)
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
