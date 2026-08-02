package serve

import (
	"log/slog"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/envprovider"
)

// HostProviderProfileID is the stable id of the profile the served harness
// derives from its environment.
//
// Stable — deliberately NOT derived from the resolved provider kind — so a
// session that recorded this profile id keeps resolving after the operator
// swaps the granted key from one provider to another in Kenaz. The kind and
// model are surfaced as their own fields on the Provider row.
const HostProviderProfileID = "kenaz-host"

// Env vars the served harness consults, in addition to the conventional
// credential vars in envprovider.Detections.
//
// EnvHostProvider is the one the Kenaz control plane writes today (Spec 078
// delivers it alongside KENAZ_ENVGRANT_<NAME> on the KENAZMETA disk); the
// other two exist so an operator can pin a model or a non-conventional key
// var without a harness release.
const (
	// EnvHostProvider names the adapter kind explicitly (anthropic, ...).
	EnvHostProvider = "KENAZ_HARNESS_PROVIDER"
	// EnvHostCredVar names the env var holding the API key, overriding the
	// kind's conventional variable.
	EnvHostCredVar = "KENAZ_HARNESS_CRED_ENV"
	// EnvHostModel pins the model id, overriding the kind's default.
	EnvHostModel = "KENAZ_HARNESS_MODEL"
)

// hostNoCredentialHint is the served harness's user-actionable advice. It
// points at the ONLY place a workbench user can actually fix this — the host
// control plane — because there is no desktop harness inside the VM.
const hostNoCredentialHint = "configure a provider in Kenaz → profile → provider, then reopen the workbench"

// HostProviderOptions binds the served harness's override env vars to the
// shared resolver.
var HostProviderOptions = envprovider.Options{
	ProviderVar:      EnvHostProvider,
	CredVar:          EnvHostCredVar,
	ModelVar:         EnvHostModel,
	NoCredentialHint: hostNoCredentialHint,
}

// HostProviders resolves the control-plane-supplied provider profile from the
// process environment, for passing to rpc.WithHostProviders.
//
// It returns nil (not an error) when nothing resolves: a workbench with no
// granted credential must still BOOT — it just boots into an honest
// "no provider configured, here is how to fix it" state rather than failing
// to start. The reason is logged at Info so the journal explains the empty
// Providers screen.
//
// Privacy: only the provider kind, the model id, and the credential env var
// NAME are logged. Credential bytes are never read here — envprovider tests
// the variable for emptiness and nothing more.
func HostProviders(get envprovider.Getenv, log *slog.Logger) []corellm.ProviderProfile {
	res, err := envprovider.Resolve(get, HostProviderOptions)
	if err != nil {
		if log != nil {
			log.Info("harness.serve.host_provider.unconfigured", "reason", err.Error())
		}
		return nil
	}
	if log != nil {
		log.Info("harness.serve.host_provider.configured",
			"profile_id", HostProviderProfileID,
			"provider", res.Kind,
			"model", res.Model,
			"cred_env", res.CredEnv,
			"explicit", res.Explicit,
		)
	}
	return []corellm.ProviderProfile{res.Profile(HostProviderProfileID)}
}
