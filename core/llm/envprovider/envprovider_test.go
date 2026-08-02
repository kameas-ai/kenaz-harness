package envprovider

import (
	"errors"
	"strings"
	"testing"
)

// env builds a Getenv over a fixed map so no test mutates process state.
func env(kv map[string]string) Getenv {
	return func(k string) string { return kv[k] }
}

const (
	agentProviderVar = "KENAZ_AGENT_PROVIDER"
	agentCredVar     = "KENAZ_AGENT_CRED_ENV"
	agentModelVar    = "KENAZ_AGENT_MODEL"
)

var agentOpts = Options{
	ProviderVar: agentProviderVar,
	CredVar:     agentCredVar,
	ModelVar:    agentModelVar,
}

func TestResolve_AutoDetectPriorityOrder(t *testing.T) {
	// Every credential present: the table's first entry must win. This is
	// the invariant that keeps the agent executor and the served harness
	// choosing the SAME provider from the same VM environment.
	got, err := Resolve(env(map[string]string{
		"ANTHROPIC_API_KEY":  "k",
		"OPENROUTER_API_KEY": "k",
		"OPENAI_API_KEY":     "k",
		"GEMINI_API_KEY":     "k",
	}), agentOpts)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Kind != "anthropic" || got.CredEnv != "ANTHROPIC_API_KEY" {
		t.Fatalf("want anthropic/ANTHROPIC_API_KEY, got %+v", got)
	}
	if got.Model != DefaultModels["anthropic"] {
		t.Fatalf("want default model %q, got %q", DefaultModels["anthropic"], got.Model)
	}
	if got.Explicit {
		t.Fatal("auto-detected resolution must not be marked Explicit")
	}
}

func TestResolve_AutoDetectEachKind(t *testing.T) {
	for _, d := range Detections {
		d := d
		t.Run(d.Kind, func(t *testing.T) {
			got, err := Resolve(env(map[string]string{d.CredEnv: "k"}), agentOpts)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got.Kind != d.Kind || got.CredEnv != d.CredEnv {
				t.Fatalf("want %s/%s, got %+v", d.Kind, d.CredEnv, got)
			}
			if got.Model == "" {
				t.Fatalf("kind %q resolved with an empty model", d.Kind)
			}
		})
	}
}

func TestResolve_NoCredential(t *testing.T) {
	_, err := Resolve(env(nil), agentOpts)
	if !errors.Is(err, ErrNoCredential) {
		t.Fatalf("want ErrNoCredential, got %v", err)
	}
	if !strings.HasPrefix(err.Error(), "no_model_credential:") {
		t.Fatalf("error must lead with the stable wire name, got %q", err.Error())
	}
}

func TestResolve_NoCredentialHintIsCallerSupplied(t *testing.T) {
	opts := agentOpts
	opts.NoCredentialHint = "configure a provider in Kenaz → profile → provider, then restart the workbench"
	_, err := Resolve(env(nil), opts)
	if !errors.Is(err, ErrNoCredential) {
		t.Fatalf("want ErrNoCredential, got %v", err)
	}
	if !strings.Contains(err.Error(), "restart the workbench") {
		t.Fatalf("caller hint missing from %q", err.Error())
	}
}

func TestResolve_ExplicitKindUsesConventionalCredVar(t *testing.T) {
	got, err := Resolve(env(map[string]string{
		agentProviderVar: "openai",
		"OPENAI_API_KEY": "k",
		// An anthropic key is also present; the explicit override must win.
		"ANTHROPIC_API_KEY": "k",
	}), agentOpts)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Kind != "openai" || got.CredEnv != "OPENAI_API_KEY" {
		t.Fatalf("want openai/OPENAI_API_KEY, got %+v", got)
	}
	if !got.Explicit {
		t.Fatal("explicit override must be marked Explicit")
	}
}

func TestResolve_ExplicitCredVarOverride(t *testing.T) {
	got, err := Resolve(env(map[string]string{
		agentProviderVar: "anthropic",
		agentCredVar:     "MY_KEY",
		"MY_KEY":         "k",
	}), agentOpts)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.CredEnv != "MY_KEY" {
		t.Fatalf("want MY_KEY, got %+v", got)
	}
}

func TestResolve_ExplicitKindWithEmptyCredentialIsAnError(t *testing.T) {
	// An operator who named a provider must NOT be silently downgraded to a
	// different auto-detected one.
	_, err := Resolve(env(map[string]string{
		agentProviderVar:    "openai",
		"ANTHROPIC_API_KEY": "k",
	}), agentOpts)
	if err == nil {
		t.Fatal("want an error when the named provider has no credential")
	}
	if errors.Is(err, ErrNoCredential) {
		t.Fatal("an explicit-kind failure must not masquerade as the auto-detect sentinel")
	}
	if !strings.HasPrefix(err.Error(), "no_model_credential:") {
		t.Fatalf("want the stable wire name prefix, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("error must name the missing var, got %q", err.Error())
	}
}

func TestResolve_UnknownKindNeedsExplicitCredVar(t *testing.T) {
	_, err := Resolve(env(map[string]string{agentProviderVar: "mystery"}), agentOpts)
	if err == nil || !strings.Contains(err.Error(), agentCredVar) {
		t.Fatalf("want an error naming %s, got %v", agentCredVar, err)
	}
}

func TestResolve_ModelOverride(t *testing.T) {
	got, err := Resolve(env(map[string]string{
		"ANTHROPIC_API_KEY": "k",
		agentModelVar:       "claude-opus-4-1",
	}), agentOpts)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Model != "claude-opus-4-1" {
		t.Fatalf("want the override model, got %q", got.Model)
	}
}

func TestResolve_ZeroOptionsIsPureAutoDetection(t *testing.T) {
	// A caller with no override namespace passes Options{} and still gets a
	// working resolution — the override vars are simply never consulted.
	got, err := Resolve(env(map[string]string{
		"OPENAI_API_KEY":  "k",
		agentProviderVar:  "anthropic", // present but not declared → ignored
		"HARNESS_UNKNOWN": "x",
	}), Options{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Kind != "openai" {
		t.Fatalf("want openai from pure detection, got %+v", got)
	}
}

func TestResolve_NilGetenv(t *testing.T) {
	if _, err := Resolve(nil, agentOpts); err == nil {
		t.Fatal("want an error for a nil Getenv")
	}
}

func TestResolution_ProfileIsIndirectAndSecretFree(t *testing.T) {
	r := Resolution{Kind: "anthropic", Model: "claude-sonnet-4-5", CredEnv: "ANTHROPIC_API_KEY"}
	p := r.Profile("host-env")
	if p.ID != "host-env" || p.Kind != "anthropic" || p.Model != "claude-sonnet-4-5" {
		t.Fatalf("unexpected profile: %+v", p)
	}
	if p.Cred.Kind != "env" {
		t.Fatalf("credential reference must be env-backed, got %q", p.Cred.Kind)
	}
	if p.Cred.Locator != "ANTHROPIC_API_KEY" {
		t.Fatalf("locator must be the var NAME, got %q", p.Cred.Locator)
	}
}

func TestTables_AreConsistent(t *testing.T) {
	// Every detectable kind must have a default model, or auto-detection can
	// succeed and then fail on the model lookup.
	for _, d := range Detections {
		if DefaultModelFor(d.Kind) == "" {
			t.Errorf("kind %q is detectable but has no default model", d.Kind)
		}
		if CredEnvFor(d.Kind) != d.CredEnv {
			t.Errorf("CredEnvFor(%q) = %q, want %q", d.Kind, CredEnvFor(d.Kind), d.CredEnv)
		}
	}
}
