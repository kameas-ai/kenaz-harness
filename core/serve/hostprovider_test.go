package serve

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/envprovider"
)

func getenvFrom(kv map[string]string) envprovider.Getenv {
	return func(k string) string { return kv[k] }
}

// The workbench case the operator hit: Kenaz grants ANTHROPIC_API_KEY, the
// unit exports it, and the served harness must boot WITH a usable provider.
func TestHostProviders_SeedsProfileFromGrantedKey(t *testing.T) {
	got := HostProviders(getenvFrom(map[string]string{
		"ANTHROPIC_API_KEY": "sk-not-a-real-key",
	}), nil)

	if len(got) != 1 {
		t.Fatalf("want exactly one host profile, got %d", len(got))
	}
	p := got[0]
	if p.ID != HostProviderProfileID {
		t.Fatalf("profile id = %q, want %q", p.ID, HostProviderProfileID)
	}
	if p.Kind != "anthropic" {
		t.Fatalf("kind = %q, want anthropic", p.Kind)
	}
	if p.Model == "" {
		t.Fatal("profile has no model — StartStream would have nothing to call")
	}
	if p.Cred.Kind != "env" || p.Cred.Locator != "ANTHROPIC_API_KEY" {
		t.Fatalf("credential must be an indirect env reference, got %+v", p.Cred)
	}
	// The whole point of the indirect reference: the profile that gets
	// persisted/serialised anywhere must not carry key material.
	if p.Cred.Locator == "sk-not-a-real-key" {
		t.Fatal("credential VALUE leaked into the profile")
	}
}

// KENAZ_HARNESS_PROVIDER is what Spec 078 delivers alongside the grant.
func TestHostProviders_HonoursExplicitProviderVar(t *testing.T) {
	got := HostProviders(getenvFrom(map[string]string{
		EnvHostProvider:     "openai",
		"OPENAI_API_KEY":    "k",
		"ANTHROPIC_API_KEY": "k",
	}), nil)
	if len(got) != 1 || got[0].Kind != "openai" {
		t.Fatalf("explicit provider var ignored: %+v", got)
	}
}

func TestHostProviders_ModelOverride(t *testing.T) {
	got := HostProviders(getenvFrom(map[string]string{
		"ANTHROPIC_API_KEY": "k",
		EnvHostModel:        "claude-opus-4-1",
	}), nil)
	if len(got) != 1 || got[0].Model != "claude-opus-4-1" {
		t.Fatalf("model override ignored: %+v", got)
	}
}

func TestHostProviders_CredVarOverride(t *testing.T) {
	got := HostProviders(getenvFrom(map[string]string{
		EnvHostProvider: "anthropic",
		EnvHostCredVar:  "WORKBENCH_KEY",
		"WORKBENCH_KEY": "k",
	}), nil)
	if len(got) != 1 || got[0].Cred.Locator != "WORKBENCH_KEY" {
		t.Fatalf("cred var override ignored: %+v", got)
	}
}

// No grant must never be fatal — the workbench still has to boot so it can
// TELL the user what to do.
func TestHostProviders_NoCredentialYieldsNilNotPanic(t *testing.T) {
	if got := HostProviders(getenvFrom(nil), nil); got != nil {
		t.Fatalf("want nil with no credential, got %+v", got)
	}
}

// An empty value is the same as absent — an EnvGrant the operator declared
// but left blank must not produce a provider that fails on first use.
func TestHostProviders_EmptyValueIsUnconfigured(t *testing.T) {
	if got := HostProviders(getenvFrom(map[string]string{"ANTHROPIC_API_KEY": ""}), nil); got != nil {
		t.Fatalf("want nil for an empty grant, got %+v", got)
	}
}

// The served harness and the in-VM agent executor must never disagree about
// which provider a given environment implies.
func TestHostProviders_AgreesWithAgentExecDetectionTable(t *testing.T) {
	for _, d := range envprovider.Detections {
		d := d
		t.Run(d.Kind, func(t *testing.T) {
			got := HostProviders(getenvFrom(map[string]string{d.CredEnv: "k"}), nil)
			if len(got) != 1 {
				t.Fatalf("kind %q: want one profile, got %d", d.Kind, len(got))
			}
			if got[0].Kind != d.Kind {
				t.Fatalf("kind %q resolved as %q", d.Kind, got[0].Kind)
			}
			if got[0].Model != envprovider.DefaultModelFor(d.Kind) {
				t.Fatalf("kind %q: model %q != shared default %q",
					d.Kind, got[0].Model, envprovider.DefaultModelFor(d.Kind))
			}
		})
	}
}

// A workbench granted a host-side llama-server (the local-inference path):
// no key, the endpoint on the vmnet gateway, an explicit model.
func TestHostProviders_CustomOpenAIEndpointWithoutCredential(t *testing.T) {
	got := HostProviders(getenvFrom(map[string]string{
		EnvHostProvider: "custom-openai",
		EnvHostBaseURL:  "http://192.168.64.1:8081/v1",
		EnvHostModel:    "qwen3.8-27b-q4_k_m",
	}), nil)
	if len(got) != 1 {
		t.Fatalf("want exactly one host profile, got %d", len(got))
	}
	p := got[0]
	if p.Kind != "custom-openai" || p.Endpoint != "http://192.168.64.1:8081/v1" || p.Model != "qwen3.8-27b-q4_k_m" {
		t.Fatalf("unexpected profile %+v", p)
	}
	if p.Defaults["auth_scheme"] != "none" {
		t.Fatalf("auth_scheme = %v, want none", p.Defaults["auth_scheme"])
	}
	if p.Cred.Kind != "" {
		t.Fatalf("no credential was granted; profile must carry no reference, got %+v", p.Cred)
	}
}

func TestHostProviders_CustomOpenAIWithoutBaseURLIsUnconfigured(t *testing.T) {
	got := HostProviders(getenvFrom(map[string]string{
		EnvHostProvider: "custom-openai",
		EnvHostModel:    "m",
	}), nil)
	if got != nil {
		t.Fatalf("missing base URL must yield no profile (boot unconfigured), got %+v", got)
	}
}

// ProbeHostProviders records the three-step probe verdict on the profile's
// CapabilityHints, reading the credential (if any) from the env and never
// touching conventional-kind profiles.
func TestProbeHostProviders_RecordsMatrixAsHints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Stream bool              `json:"stream"`
			Tools  []json.RawMessage `json:"tools"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Tools) > 0 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"noop","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
			return
		}
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n")
			_, _ = io.WriteString(w, "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer srv.Close()

	env := map[string]string{
		EnvHostProvider: "custom-openai",
		EnvHostBaseURL:  srv.URL + "/v1",
		EnvHostModel:    "m",
	}
	profiles := HostProviders(getenvFrom(env), nil)
	other := corellm.ProviderProfile{ID: "anth", Kind: "anthropic", Model: "x"}
	profiles = append(profiles, other)

	got := ProbeHostProviders(context.Background(), getenvFrom(env), profiles, nil)
	if len(got) != 2 {
		t.Fatalf("want 2 profiles back, got %d", len(got))
	}
	h := got[0].CapabilityHints
	if !h[corellm.CapStreaming] || !h[corellm.CapToolCalling] || !h[corellm.CapUsageReporting] {
		t.Fatalf("expected all three hints true from the fake endpoint, got %+v", h)
	}
	if got[1].CapabilityHints != nil {
		t.Fatalf("conventional kinds must be untouched, got %+v", got[1].CapabilityHints)
	}
}

func TestProbeHostProviders_UnreachableEndpointLeavesHintsUnset(t *testing.T) {
	env := map[string]string{
		EnvHostProvider: "custom-openai",
		EnvHostBaseURL:  "http://127.0.0.1:1/v1",
		EnvHostModel:    "m",
	}
	got := ProbeHostProviders(context.Background(), getenvFrom(env), HostProviders(getenvFrom(env), nil), nil)
	if len(got) != 1 || got[0].CapabilityHints != nil {
		t.Fatalf("unreachable endpoint must leave hints unset (conservative), got %+v", got)
	}
}
