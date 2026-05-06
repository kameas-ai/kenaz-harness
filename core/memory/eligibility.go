// Package memory — embedder eligibility check.
//
// EmbedderEligibility walks a slice of ProviderProfile records (same
// logic as newEmbedderFromProfiles in core/rpc/api.go) but READ-ONLY:
// it collects metadata about which profiles are eligible and which kinds
// are present but skipped, without constructing an Embedder.
//
// Eligible kinds (OpenAI-compatible /v1/embeddings shape):
//
//	openai, openrouter, custom_openai_compatible, azure
//
// Skipped kinds (no embeddings endpoint or different wire shape):
//
//	anthropic — no /v1/embeddings endpoint
//	bedrock   — AWS Titan uses a different wire shape; deferred
package memory

// EmbedderEligibility reports which provider profiles are capable of
// supplying embeddings. It is designed to be called cheaply at settings
// load time so the frontend can surface a contextual banner when the
// user has only Anthropic-direct or Bedrock profiles.
type EmbedderEligibility struct {
	// HasEligible is true when at least one profile in AllProfiles is
	// eligible for use as an embedder.
	HasEligible bool `json:"hasEligible"`
	// AllProfiles is the total count of profiles that were examined.
	AllProfiles int `json:"allProfiles"`
	// EligibleProfiles is the count of profiles that are eligible.
	EligibleProfiles int `json:"eligibleProfiles"`
	// SkippedKinds holds the unique provider kinds that were present in
	// the profile list but are not eligible (e.g. "anthropic", "bedrock").
	// The frontend uses this list to render per-provider explanations in
	// the "no memory provider" banner.
	SkippedKinds []string `json:"skippedKinds"`
}

// ProfileEligibilityInput is the minimal profile fields CheckEligibility
// needs. Keeping this separate from core/llm.ProviderProfile avoids a
// circular import; the rpc layer maps ProviderProfile → ProfileEligibilityInput
// before calling CheckEligibility (see core/rpc/views/memory/impl.go).
type ProfileEligibilityInput struct {
	Kind     string
	Endpoint string // only relevant for custom_openai_compatible and azure
	// AzureComplete mirrors the Defaults map check in newEmbedderFromProfiles:
	// true when deployment_id, api_version, and resource_name are all non-empty.
	AzureComplete bool
}

// CheckEligibility walks profiles and returns an EmbedderEligibility
// summary. The logic mirrors newEmbedderFromProfiles's eligibleEmbedder
// inner function — but without constructing any network objects.
func CheckEligibility(profiles []ProfileEligibilityInput) EmbedderEligibility {
	skippedKindsSet := map[string]struct{}{}
	eligible := 0

	for _, p := range profiles {
		if isEligible(p) {
			eligible++
		} else if isKnownSkippedKind(p.Kind) {
			skippedKindsSet[p.Kind] = struct{}{}
		}
		// Unknown or misconfigured profiles (e.g. azure without required
		// fields) are silently ignored — they appear in AllProfiles but
		// neither as eligible nor as explicitly skipped.
	}

	skippedKinds := make([]string, 0, len(skippedKindsSet))
	for k := range skippedKindsSet {
		skippedKinds = append(skippedKinds, k)
	}
	// Sort for deterministic output.
	sortStrings(skippedKinds)

	return EmbedderEligibility{
		HasEligible:      eligible > 0,
		AllProfiles:      len(profiles),
		EligibleProfiles: eligible,
		SkippedKinds:     skippedKinds,
	}
}

// isEligible mirrors the eligibleEmbedder switch in newEmbedderFromProfiles.
func isEligible(p ProfileEligibilityInput) bool {
	switch p.Kind {
	case "openai", "openrouter":
		return true
	case "custom_openai_compatible":
		// Eligible only when an Endpoint is set.
		return p.Endpoint != ""
	case "azure":
		// Eligible only when all three Azure-specific fields are present.
		return p.AzureComplete
	default:
		return false
	}
}

// isKnownSkippedKind returns true for provider kinds that are explicitly
// excluded from embeddings support by design (not due to misconfiguration).
func isKnownSkippedKind(kind string) bool {
	return kind == "anthropic" || kind == "bedrock"
}

// sortStrings sorts a slice of strings in-place using a simple insertion
// sort. Avoids importing the sort package for a trivially-small slice
// (at most a handful of provider kinds).
func sortStrings(ss []string) {
	for i := 1; i < len(ss); i++ {
		key := ss[i]
		j := i - 1
		for j >= 0 && ss[j] > key {
			ss[j+1] = ss[j]
			j--
		}
		ss[j+1] = key
	}
}
