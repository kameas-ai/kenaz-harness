// Package envprovider is the SINGLE source of truth for resolving an LLM
// provider from process environment variables.
//
// Two callers need exactly this logic and used to carry private copies:
//
//   - cmd/harness-vm (Spec 058 agent-exec) — resolves the model the in-VM
//     task dispatcher runs against.
//   - the served harness (--serve, :7880) — seeds a read-only "host"
//     provider profile so a workbench that the Kenaz control plane
//     configured (Spec 078 EnvGrant delivery) boots with a usable provider
//     instead of an empty Providers screen.
//
// Both paths read the SAME conventional credential env vars, in the SAME
// priority order, and fall back to the SAME per-provider default models.
// Keeping one table here is what makes "kenaz delivered a key" and "the
// harness found a key" the same predicate.
//
// # Env contract
//
// Callers supply the NAMES of their own override variables via Options, so
// the two callers keep their historical, distinct namespaces
// (KENAZ_AGENT_* vs KENAZ_HARNESS_*) while sharing the resolution rules:
//
//	<ProviderVar>  optional adapter kind override (anthropic, openrouter,
//	               openai, gemini, ...). Unset → auto-detect.
//	<CredVar>      optional name of the env var holding the API key.
//	               Defaults to the kind's conventional variable.
//	<ModelVar>     optional model id override. Unset → DefaultModels[kind].
//
// # Privacy
//
// This package reads credential VALUES only to test them for emptiness. It
// never stores, returns, or logs them. A Resolution carries the credential
// env var NAME (which is not secret — it is the same name the operator typed
// into the Kenaz grant editor), never its bytes.
package envprovider

import (
	"errors"
	"fmt"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// Getenv is the environment-lookup seam. Production callers pass os.Getenv;
// tests pass a map-backed closure. Taking it as a parameter is what makes
// this package testable without mutating process state.
type Getenv func(string) string

// Detection maps a conventional credential env var to an adapter kind.
type Detection struct {
	// Kind is the core/llm adapter kind (registry key).
	Kind string
	// CredEnv is the conventional env var name holding that kind's API key.
	CredEnv string
}

// Detections is the auto-detection table, in priority order. The first entry
// whose CredEnv is non-empty in the environment wins.
//
// Order is a product decision, not an implementation detail: Anthropic first
// because it is the provider the Kenaz control plane grants by default
// (KENAZ_ENVGRANT_ANTHROPIC_API_KEY), then the aggregator, then the other
// first-party APIs.
var Detections = []Detection{
	{Kind: "anthropic", CredEnv: "ANTHROPIC_API_KEY"},
	{Kind: "openrouter", CredEnv: "OPENROUTER_API_KEY"},
	{Kind: "openai", CredEnv: "OPENAI_API_KEY"},
	{Kind: "gemini", CredEnv: "GEMINI_API_KEY"},
}

// DefaultModels is the per-kind model used when no model override is set.
// Each id matches its provider's capabilities-catalog entry.
var DefaultModels = map[string]string{
	"anthropic":  "claude-sonnet-4-5",
	"openrouter": "anthropic/claude-sonnet-4-5",
	"openai":     "gpt-4o",
	"gemini":     "gemini-2.5-flash",
}

// CredEnvFor returns the conventional credential env var for kind, or "" when
// the kind has no convention (the caller must then supply an explicit
// cred-env override).
func CredEnvFor(kind string) string {
	for _, d := range Detections {
		if d.Kind == kind {
			return d.CredEnv
		}
	}
	return ""
}

// DefaultModelFor returns the default model id for kind, or "" when unknown.
func DefaultModelFor(kind string) string { return DefaultModels[kind] }

// ErrNoCredential is the named sentinel returned when nothing in Detections
// resolves. The leading token ("no_model_credential") is a stable wire name —
// the harness-vm task surface truncates error messages to 64 runes and relies
// on the name coming first.
//
// Callers match with errors.Is; the returned error additionally carries the
// caller's Options.NoCredentialHint so each surface gives advice its own user
// can act on.
var ErrNoCredential = errors.New(
	"no_model_credential: no provider API key in this process's environment")

// Options names the caller's override env vars. Any field left empty is
// simply not consulted, so a caller with no override namespace can pass the
// zero Options and get pure auto-detection.
type Options struct {
	// ProviderVar is the env var naming an explicit adapter kind.
	ProviderVar string
	// CredVar is the env var naming the credential env var to read.
	CredVar string
	// ModelVar is the env var naming an explicit model id.
	ModelVar string
	// NoCredentialHint is appended to ErrNoCredential so each caller can
	// tell ITS user how to fix the problem. The in-VM agent executor points
	// at KENAZ_AGENT_EXEC=stub; the served harness points at the Kenaz
	// profile editor. Empty → a generic "grant one" hint.
	NoCredentialHint string
}

// noCredentialError wraps ErrNoCredential with a caller-supplied hint while
// keeping errors.Is(err, ErrNoCredential) true.
type noCredentialError struct{ hint string }

func (e *noCredentialError) Error() string {
	return ErrNoCredential.Error() + "; " + e.hint
}
func (e *noCredentialError) Unwrap() error { return ErrNoCredential }

// Resolution is the structural outcome of a successful resolve. Every field
// is safe to log: a kind, a model id, and an env var NAME.
type Resolution struct {
	// Kind is the resolved adapter kind.
	Kind string
	// Model is the resolved model id.
	Model string
	// CredEnv is the NAME of the env var holding the API key. Never its value.
	CredEnv string
	// Explicit is true when Kind came from the caller's ProviderVar override
	// rather than from auto-detection. Surfaced for logging only.
	Explicit bool
}

// Profile renders the Resolution as a ProviderProfile with an env-backed
// credential reference. id is the caller's structural profile id.
//
// The credential reference is indirect (kind "env", locator = the var NAME):
// the registry's credref resolver reads the bytes per-request and zeroizes
// them afterwards, so key material never lands on this struct or on disk.
func (r Resolution) Profile(id string) llm.ProviderProfile {
	return llm.ProviderProfile{
		ID:    id,
		Kind:  r.Kind,
		Model: r.Model,
		Cred:  llm.CredentialReference{Kind: "env", Locator: r.CredEnv},
	}
}

// Resolve picks a provider kind, model, and credential env var from the
// environment.
//
// Precedence:
//  1. An explicit kind from opts.ProviderVar. Its credential var comes from
//     opts.CredVar when set, else the kind's convention. An explicit kind
//     whose credential var is EMPTY is an error, never a silent fallback to
//     auto-detection — the operator asked for that provider by name.
//  2. Auto-detection over Detections, first non-empty credential wins.
//
// A resolution failure is always a named error, never a zero Resolution with
// a nil error.
func Resolve(get Getenv, opts Options) (Resolution, error) {
	if get == nil {
		return Resolution{}, errors.New("envprovider: nil Getenv")
	}

	lookup := func(name string) string {
		if name == "" {
			return ""
		}
		return get(name)
	}

	kind := lookup(opts.ProviderVar)
	credEnv := lookup(opts.CredVar)
	explicit := kind != ""

	if explicit {
		if credEnv == "" {
			credEnv = CredEnvFor(kind)
		}
		if credEnv == "" {
			return Resolution{}, fmt.Errorf(
				"no_model_credential: provider %q has no conventional key var; set %s",
				kind, orPlaceholder(opts.CredVar, "an explicit credential env var"))
		}
		if get(credEnv) == "" {
			return Resolution{}, fmt.Errorf(
				"no_model_credential: %s is empty (provider %q); grant the credential or pick another provider",
				credEnv, kind)
		}
	} else {
		for _, d := range Detections {
			if get(d.CredEnv) != "" {
				kind, credEnv = d.Kind, d.CredEnv
				break
			}
		}
		if kind == "" {
			hint := opts.NoCredentialHint
			if hint == "" {
				hint = "grant one (e.g. ANTHROPIC_API_KEY)"
			}
			return Resolution{}, &noCredentialError{hint: hint}
		}
	}

	model := lookup(opts.ModelVar)
	if model == "" {
		model = DefaultModelFor(kind)
	}
	if model == "" {
		return Resolution{}, fmt.Errorf(
			"no_model_default: provider %q has no default model; set %s",
			kind, orPlaceholder(opts.ModelVar, "an explicit model env var"))
	}

	return Resolution{Kind: kind, Model: model, CredEnv: credEnv, Explicit: explicit}, nil
}

func orPlaceholder(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
