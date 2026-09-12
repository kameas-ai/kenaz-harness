package serve

import (
	"context"
	"log/slog"
	"time"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/custom"
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
	// EnvHostBaseURL is the OpenAI-compatible endpoint base URL. Required
	// (and only consulted) when EnvHostProvider=custom-openai — the
	// in-workbench path to a host-side llama-server (e.g.
	// http://192.168.64.1:8081/v1 over the vmnet gateway).
	EnvHostBaseURL = "KENAZ_HARNESS_BASE_URL"
	// EnvHostAuthScheme pins the custom-openai auth scheme
	// (none|bearer|api-key-header|custom). Optional; see envprovider.
	EnvHostAuthScheme = "KENAZ_HARNESS_AUTH_SCHEME"
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
	BaseURLVar:       EnvHostBaseURL,
	AuthSchemeVar:    EnvHostAuthScheme,
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
			"endpoint", res.BaseURL,
			"auth_scheme", res.AuthScheme,
		)
	}
	return []corellm.ProviderProfile{res.Profile(HostProviderProfileID)}
}

// hostProbeTimeout bounds the boot-time capability probe. The prober's own
// per-step budget is 5s × 3 steps; this is the outer ceiling so a dead
// endpoint cannot hold the served boot for longer than that.
const hostProbeTimeout = 20 * time.Second

// ProbeHostProviders runs the custom-openai three-step capability probe
// (streaming / tool calling / streaming usage) against every custom-openai
// host profile and records the verdict on the profile's CapabilityHints.
// Desktop users get this from the Providers screen's Probe button; served
// mode has no settings surface, so the served boot is the only place it can
// happen. Conventional kinds are returned untouched.
//
// Best-effort: an unreachable endpoint leaves the hints unset (the adapter
// then operates conservatively) and the reason is logged once. The probe
// reads the credential env var named by the profile — the bytes go straight
// to the prober and are zeroed on return; nothing is logged.
func ProbeHostProviders(ctx context.Context, get envprovider.Getenv, profiles []corellm.ProviderProfile, log *slog.Logger) []corellm.ProviderProfile {
	for i := range profiles {
		p := &profiles[i]
		if p.Kind != envprovider.CustomOpenAIKind || p.Endpoint == "" {
			continue
		}
		scheme, _ := p.Defaults["auth_scheme"].(string)
		var cred []byte
		if p.Cred.Kind == "env" && p.Cred.Locator != "" && get != nil {
			cred = []byte(get(p.Cred.Locator))
		}
		pctx, cancel := context.WithTimeout(ctx, hostProbeTimeout)
		res := custom.NewProber(nil).Probe(pctx, custom.ProbeRequest{
			BaseURL:    p.Endpoint,
			Model:      p.Model,
			AuthScheme: custom.AuthScheme(scheme),
			Cred:       cred,
		})
		cancel()
		for j := range cred {
			cred[j] = 0
		}
		if res.Err != nil {
			if log != nil {
				log.Warn("harness.serve.host_provider.probe_failed",
					"profile_id", p.ID, "endpoint", p.Endpoint, "reason", res.Err.Error())
			}
			continue
		}
		if p.CapabilityHints == nil {
			p.CapabilityHints = map[corellm.Capability]bool{}
		}
		applyHint(p.CapabilityHints, corellm.CapStreaming, res.Matrix.Streaming)
		applyHint(p.CapabilityHints, corellm.CapToolCalling, res.Matrix.ToolCalling)
		applyHint(p.CapabilityHints, corellm.CapUsageReporting, res.Matrix.StreamingUsage)
		if log != nil {
			log.Info("harness.serve.host_provider.probed",
				"profile_id", p.ID,
				"endpoint", p.Endpoint,
				"streaming", string(res.Matrix.Streaming),
				"tool_calling", string(res.Matrix.ToolCalling),
				"streaming_usage", string(res.Matrix.StreamingUsage),
			)
		}
	}
	return profiles
}

// applyHint records a definite probe verdict; "unknown" leaves the hint
// unset so the adapter's conservative default applies.
func applyHint(h map[corellm.Capability]bool, c corellm.Capability, v custom.CapabilityValue) {
	switch v {
	case custom.CapabilityValueTrue:
		h[c] = true
	case custom.CapabilityValueFalse:
		h[c] = false
	}
}
