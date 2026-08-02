package llm

import (
	"fmt"
	"reflect"

	"gopkg.in/yaml.v3"
)

// --- WP08 (versioned-model-profile-01PMDL04): close the ProviderProfile
// governance-smuggling vector -----------------------------------------
//
// WP06 (model_profile_schema.go) closed this class of vector for the new
// ModelProfile bundle kind: ValidateModelProfileBundle decodes into a
// generic key tree and rejects any key ModelProfile's own struct tags
// don't declare, so a "cedar:"/"budget:" block appended to a bundle is
// refused with the offending key named, instead of being silently
// dropped by yaml.Unmarshal's tolerant-of-unknown-fields default.
//
// bundleartifact.Handler (handler.go) — the *shipped* kind: llm_provider
// path — never got the equivalent treatment: Parse does a plain
// yaml.Unmarshal into ProviderProfile. That already means an
// unrecognized top-level key (e.g. a sibling "cedar:" block) is
// silently dropped rather than rejected, the same latent gap
// ModelProfile had pre-WP06. But ProviderProfile has a second, sharper
// problem ModelProfile does not: Defaults is a map[string]any, so
// unlike every other field, keys placed inside it are NOT dropped —
// they parse successfully and are readable by any adapter that chooses
// to look (several already do: temperature/top_p/top_k/max_tokens/...
// for sampling knobs, auth_scheme/auth_header/template_id for
// custom-openai, deployment_id/api_version/resource_name for Azure —
// see providerDefaultsAllowed below, derived by grepping every
// prof.Defaults[...] read site in core/llm/*, not guessed). Nothing
// today stops an operator-authored bundle from also placing a
// "cedar"/"budget"/"spend"-shaped key in Defaults; it would parse
// cleanly and sit there, unvalidated — exactly the smuggling shape
// WP06 closed for ModelProfile's typed fields.
//
// ValidateProviderProfileBundle is the equivalent parse-boundary check
// for llm_provider bundles:
//   - envelope: reuses the same generic rejectForeignKeys reflection
//     walk WP06 built (model_profile_schema.go) against ProviderProfile's
//     own declared fields, so a foreign top-level key is rejected
//     loudly instead of silently vanishing. rejectForeignKeys does not
//     descend into map-kind fields (Defaults is intentionally
//     free-form), so this step never touches Defaults' contents — that
//     is validateProviderDefaults's job.
//   - Defaults: validateProviderDefaults rejects any key not on the
//     explicit allow-list of sampling knobs and provider-specific
//     config keys adapters actually read.
//
// Fail-closed, not fail-open: an unknown key (envelope or Defaults) is
// rejected — the bundle is refused, the offending key is named in the
// error — rather than silently stripped-with-a-warning. This mirrors
// ValidateModelProfileBundle's choice for consistency across the two
// artifact kinds. The compatibility risk of fail-closed is low here:
// kind: llm_provider bundles are not yet wired into any live
// bundle-install RPC path (bundleartifact.Handler is only exercised by
// its own tests today — see handler.go's package doc: "the
// bundle-format-resolver mission is developed separately"), so there is
// no installed base of already-accepted bundles this can break. The
// Defaults allow-list was built by grepping every prof.Defaults[key]
// read across core/llm/{anthropic,openai,openaiwire,azure,gemini,
// openrouter,custom,personal} plus core/rpc/api.go's Azure-embeddings
// special case, not guessed.
func ValidateProviderProfileBundle(raw []byte) (ProviderProfile, error) {
	var generic any
	if err := yaml.Unmarshal(raw, &generic); err != nil {
		return ProviderProfile{}, fmt.Errorf("llm: provider profile bundle: %w", err)
	}
	if err := rejectForeignKeys("", generic, reflect.TypeOf(ProviderProfile{})); err != nil {
		return ProviderProfile{}, err
	}

	var p ProviderProfile
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return ProviderProfile{}, fmt.Errorf("llm: provider profile bundle: %w", err)
	}
	if err := validateProviderDefaults(p.ID, p.Defaults); err != nil {
		return ProviderProfile{}, err
	}
	return p, nil
}

// providerDefaultsAllowed is the exact set of ProviderProfile.Defaults
// keys any shipping adapter reads. Derived from grep, not guessed:
//
//   - sampling knobs (openai/openai.go, openai/openaiwire/body.go,
//     azure/adapter.go, anthropic/anthropic.go, gemini/wire.go,
//     openrouter/openrouter.go, custom/adapter.go): temperature, top_p,
//     top_k, max_tokens, max_output_tokens, presence_penalty,
//     frequency_penalty, seed, parallel_tool_calls, stop
//   - custom-openai template/auth config (custom/adapter.go,
//     personal/personal.go): template_id, auth_scheme, auth_header
//   - Azure embeddings config (core/rpc/api.go eligibleEmbedder /
//     personalProfileLister.ListProfiles): deployment_id, api_version,
//     resource_name
var providerDefaultsAllowed = map[string]struct{}{
	"temperature":         {},
	"top_p":               {},
	"top_k":               {},
	"max_tokens":          {},
	"max_output_tokens":   {},
	"presence_penalty":    {},
	"frequency_penalty":   {},
	"seed":                {},
	"parallel_tool_calls": {},
	"stop":                {},
	"template_id":         {},
	"auth_scheme":         {},
	"auth_header":         {},
	"deployment_id":       {},
	"api_version":         {},
	"resource_name":       {},
}

// validateProviderDefaults fails closed on any Defaults key outside
// providerDefaultsAllowed, naming the offending key (and, for a
// governance-shaped key, the subsystem it actually belongs in) so a
// bundle author gets an actionable error instead of a silently-accepted
// smuggled field.
func validateProviderDefaults(profileID string, defaults map[string]any) error {
	for k := range defaults {
		if _, ok := providerDefaultsAllowed[k]; ok {
			continue
		}
		if hint, isGovernance := governanceKeyHint(k); isGovernance {
			return fmt.Errorf(
				"llm: provider profile %q: defaults.%s is not permitted: %s (governance/budget policy must never live inside a ProviderProfile)",
				profileID, k, hint,
			)
		}
		return fmt.Errorf("llm: provider profile %q: unknown defaults key %q", profileID, k)
	}
	return nil
}
