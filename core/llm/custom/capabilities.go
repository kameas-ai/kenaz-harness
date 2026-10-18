package custom

import (
	"context"
	"errors"
	"fmt"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// ProviderCapabilities implements llm.CapabilitiesProvider.
//
// custom-openai is the mission's designated "probe vehicle" (register
// A-5: "WIRE IT as the probe vehicle") — but unlike ollama (which has a
// process-wide DefaultBaseURL every profile can fall back to), an
// arbitrary custom-openai profile points at a genuinely arbitrary
// endpoint with no sensible universal default. Calling this with no
// endpoint in scope is therefore always an error, not a degrade — the
// caller must use ProviderCapabilitiesAt (registry.Registry.
// refreshCapabilities, the one production caller, always does: it
// prefers llm.EndpointCapabilitiesProvider when the adapter implements
// it — see that method's own H4-fix doc comment).
//
// This method still needs to exist: registry.go's read-path gate
// (`if _, ok := adapter.(llm.CapabilitiesProvider); ok { refresher.
// MaybeRefresh(...) }`) type-asserts against the PLAIN interface before
// deciding whether to bother calling MaybeRefresh at all. Without this
// method, *Adapter would satisfy only EndpointCapabilitiesProvider and
// that gate would never fire for a custom-openai profile.
func (a *Adapter) ProviderCapabilities(ctx context.Context, modelID string) (llm.ProviderCapabilities, error) {
	return a.ProviderCapabilitiesAt(ctx, "", modelID)
}

// ProviderCapabilitiesAt implements llm.EndpointCapabilitiesProvider
// (review finding H4 — see ollama.Adapter.ProviderCapabilitiesAt's own
// doc comment for why the endpoint-aware variant is the one the
// registry actually calls). It reuses the SAME three-step capability
// probe (Prober.Probe, core/llm/custom/probe.go) that
// custom-openai-compatible-endpoint-01KQ8VN0 already built and tested
// for the pre-flight gate in Stream() (gateOutgoing) — this is not a
// second, parallel HTTP probe implementation; it is the shared prober,
// bridged onto the newer llm.CapabilitiesProvider interface WP14
// introduces.
//
// Per A-5's merge order ("probe -> cache -> CapabilityHints reader ...
// the static custom.yaml baseline lands FIRST and stays"), starting
// from a.cat.DescribeRich(Kind, modelID) and overlaying only the fields
// the probe determined (ProbedCapabilities) means a probe failure
// degrades to "no live data yet, the static baseline still decides
// Gate.Check" — never to "everything unsupported".
//
// Known limitation, shared with the plain llm.CapabilitiesProvider
// interface itself: neither this method nor the plain ProviderCapabilities
// above carries a credential parameter, so this probe always runs
// unauthenticated (auth_scheme: none equivalent). A custom-openai
// endpoint that requires auth will fail the probe's step 1 (auth check)
// and this method returns that error — which A-5's own invariant treats
// as a safe degrade, not a fault: the static baseline keeps deciding
// Gate.Check for that profile until a future probe succeeds.
func (a *Adapter) ProviderCapabilitiesAt(ctx context.Context, endpoint, modelID string) (llm.ProviderCapabilities, error) {
	if endpoint == "" {
		return llm.ProviderCapabilities{}, errors.New(
			"custom-openai: capability probe requires a profile endpoint (no process-wide default " +
				"exists for an arbitrary custom-openai endpoint, unlike ollama's DefaultBaseURL)")
	}
	if a.cat == nil {
		return llm.ProviderCapabilities{}, errors.New("custom-openai: no capability catalog loaded")
	}

	prober := NewProber(a.httpc)
	result := prober.Probe(ctx, ProbeRequest{
		BaseURL:    endpoint,
		Model:      modelID,
		AuthScheme: AuthSchemeNone,
	})
	if result.Err != nil {
		return llm.ProviderCapabilities{}, fmt.Errorf("custom-openai: capability probe: %w", result.Err)
	}

	caps := a.cat.DescribeRich(Kind, modelID)
	var probed []llm.Capability
	if result.Matrix.Streaming != CapabilityValueUnknown {
		caps.Streaming = result.Matrix.Streaming == CapabilityValueTrue
		probed = append(probed, llm.CapStreaming)
	}
	if result.Matrix.StreamingUsage != CapabilityValueUnknown {
		caps.UsageReporting = result.Matrix.StreamingUsage == CapabilityValueTrue
		probed = append(probed, llm.CapUsageReporting)
	}
	if result.Matrix.ToolCalling != CapabilityValueUnknown {
		caps.ToolCalling = result.Matrix.ToolCalling == CapabilityValueTrue
		probed = append(probed, llm.CapToolCalling)
	}
	// M5 fix (mirrors ollama.Adapter.ProviderCapabilitiesAt): name
	// exactly the keys THIS probe determined, so a cache hit's overlay
	// (registry.mergeCapabilityHints, via ProbedSupported) never shadows
	// a Supported key the probe never touched.
	caps.ProbedCapabilities = probed
	return caps, nil
}

// Compile-time witnesses: *Adapter satisfies both the plain and the
// endpoint-aware capability-provider interfaces.
var _ llm.CapabilitiesProvider = (*Adapter)(nil)
var _ llm.EndpointCapabilitiesProvider = (*Adapter)(nil)
