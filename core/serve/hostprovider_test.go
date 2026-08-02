package serve

import (
	"testing"

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
