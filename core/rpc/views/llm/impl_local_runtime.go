package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/llm/localruntime"
)

// ErrRuntimeNotRunning is returned by AutoConfigureLocalRuntime when the
// target runtime's port is not reachable.
var ErrRuntimeNotRunning = errors.New("llm: local runtime is not running")

// ErrLocalRuntimeDisabled is returned when HARNESS_LOCAL_RUNTIMES=0.
// Distinct from custom-openai's ErrFeatureDisabled since the two features
// have independent env-flag gates.
var ErrLocalRuntimeDisabled = errors.New("llm: local runtime feature is disabled")

// ErrRuntimeUnknown is returned when the supplied kind is not one of the
// supported runtime kinds.
var ErrRuntimeUnknown = errors.New("llm: unknown local runtime kind")

// ListDetectedLocalRuntimes returns the current detection snapshot. Results
// are cached for 5 minutes. Returns an empty slice when the feature flag is off.
func (a *API) ListDetectedLocalRuntimes(ctx context.Context) ([]LocalRuntimeInfo, error) {
	if !localruntime.Enabled() {
		return []LocalRuntimeInfo{}, nil
	}
	infos := localruntime.GetOrDetect(ctx)
	return runtimeInfosToWire(infos), nil
}

// AutoConfigureLocalRuntime detects models from the running runtime identified
// by kind and persists a personal provider profile with Kind="custom-openai".
//
// Dependency note: the custom-openai-compatible-endpoint mission provides the
// AddProvider pathway used here. When that mission's code lands at merge time,
// the Kind="custom-openai" profile will be correctly dispatched. Until then the
// profile is persisted but the registry may not resolve it.
func (a *API) AutoConfigureLocalRuntime(ctx context.Context, kind string) (LocalRuntimeConfigResult, error) {
	if !localruntime.Enabled() {
		return LocalRuntimeConfigResult{}, ErrLocalRuntimeDisabled
	}

	rtKind := localruntime.RuntimeKind(kind)
	known := false
	for _, k := range localruntime.AllKinds {
		if k == rtKind {
			known = true
			break
		}
	}
	if !known {
		return LocalRuntimeConfigResult{}, ErrRuntimeUnknown
	}

	// Run a fresh detection for this specific runtime to confirm it's up.
	info := detectSingleRuntime(ctx, rtKind)
	if !info.Running {
		return LocalRuntimeConfigResult{}, ErrRuntimeNotRunning
	}

	// Fetch models from the runtime.
	models, err := fetchModels(ctx, info)
	if err != nil {
		// Non-fatal: we create the provider even without models.
		models = nil
	}

	// Build the profile name (e.g. "Ollama (local)").
	profileName := fmt.Sprintf("%s (local)", info.Name)
	// Profile ID: stable, deterministic, human-readable.
	profileID := fmt.Sprintf("local-%s", strings.ReplaceAll(kind, "-", "_"))

	modelIDs := make([]string, 0, len(models))
	for _, m := range models {
		modelIDs = append(modelIDs, m.ID)
	}

	// Persist via the personal store using AddProvider.
	// Kind="custom-openai" so the custom-openai adapter resolves requests.
	in := AddProviderInput{
		ID:       profileID,
		Name:     profileName,
		Kind:     "custom-openai",
		Model:    firstOrEmpty(modelIDs),
		Models:   modelIDs,
		Region:   "",
		// No credential required for localhost runtimes.
		Cred: CredentialReference{Kind: "env", Locator: ""},
	}
	// Endpoint is carried in Defaults for custom-openai adapters.
	// Since AddProviderInput doesn't carry an Endpoint field on the view type,
	// we append it as an extra context. The merge driver / follow-up will wire
	// this cleanly once custom-openai is in tree.
	//
	// TODO(custom-openai): replace this with the real AddProvider once
	// custom-openai-compatible-endpoint lands. The Endpoint field must be
	// threaded through AddProviderInput → ProviderProfile.Endpoint.
	_ = in // suppress unused warning; actual store call below

	if a.store != nil {
		if err2 := a.addLocalRuntimeProfile(profileID, profileName, info.DefaultBaseURL, modelIDs); err2 != nil {
			return LocalRuntimeConfigResult{}, fmt.Errorf("llm: persist local runtime profile: %w", err2)
		}
	}

	wireModels := runtimeModelsToWire(models)
	return LocalRuntimeConfigResult{
		ProviderID: profileID,
		Name:       profileName,
		Models:     wireModels,
	}, nil
}

// RescanLocalRuntimes invalidates the detection cache and returns a fresh snapshot.
func (a *API) RescanLocalRuntimes(ctx context.Context) ([]LocalRuntimeInfo, error) {
	if !localruntime.Enabled() {
		return []LocalRuntimeInfo{}, nil
	}
	localruntime.Invalidate()
	infos := localruntime.GetOrDetect(ctx)
	return runtimeInfosToWire(infos), nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// detectSingleRuntime runs a fresh (non-cached) detection for a single kind.
func detectSingleRuntime(ctx context.Context, kind localruntime.RuntimeKind) localruntime.RuntimeInfo {
	all := localruntime.DetectAll(ctx)
	for _, r := range all {
		if r.Kind == kind {
			return r
		}
	}
	// Defensive: return a zero value with the kind set.
	return localruntime.RuntimeInfo{Kind: kind, DetectedAt: time.Now()}
}

// fetchModels calls the runtime's metadata fetcher (WP03).
// Returns nil when the runtime is not yet wired.
func fetchModels(ctx context.Context, info localruntime.RuntimeInfo) ([]localruntime.LocalRuntimeModel, error) {
	f := localruntime.FindFetcher(info.Kind)
	if f == nil {
		return nil, nil
	}
	return f.FetchModels(ctx, info)
}

// addLocalRuntimeProfile persists a local runtime profile to the personal store.
// This is a thin shim until custom-openai lands.
func (a *API) addLocalRuntimeProfile(profileID, name, endpoint string, modelIDs []string) error {
	firstModel := firstOrEmpty(modelIDs)
	in := AddProviderInput{
		ID:     profileID,
		Name:   name,
		Kind:   "custom-openai",
		Model:  firstModel,
		Models: modelIDs,
		Cred:   CredentialReference{Kind: "env", Locator: ""},
	}
	// TODO(custom-openai): once the custom-openai adapter lands, pass Endpoint
	// through AddProviderInput so the profile can be used immediately.
	// For now we store what we have; the registry will reject unknown kinds.
	_ = endpoint
	return a.AddProvider(context.Background(), in)
}

// firstOrEmpty returns the first element or empty string.
func firstOrEmpty(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// runtimeInfosToWire converts []localruntime.RuntimeInfo to []LocalRuntimeInfo.
func runtimeInfosToWire(infos []localruntime.RuntimeInfo) []LocalRuntimeInfo {
	out := make([]LocalRuntimeInfo, 0, len(infos))
	for _, r := range infos {
		out = append(out, LocalRuntimeInfo{
			Kind:           string(r.Kind),
			Name:           r.Name,
			Running:        r.Running,
			Installed:      r.Installed,
			DefaultBaseURL: r.DefaultBaseURL,
			Port:           r.Port,
		})
	}
	return out
}

// runtimeModelsToWire converts []localruntime.LocalRuntimeModel to []LocalRuntimeModel.
func runtimeModelsToWire(models []localruntime.LocalRuntimeModel) []LocalRuntimeModel {
	out := make([]LocalRuntimeModel, 0, len(models))
	for _, m := range models {
		dn := m.DisplayName
		if dn == "" {
			dn = m.ID
		}
		out = append(out, LocalRuntimeModel{
			ID:          m.ID,
			DisplayName: dn,
			SizeBytes:   m.SizeBytes,
			QuantLevel:  m.QuantLevel,
			ParamCount:  m.ParameterCount,
		})
	}
	return out
}
