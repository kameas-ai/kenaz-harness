package llm

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ModelProfileStore is the in-memory (model_id, version) -> ModelProfile
// resolver (versioned-model-profile-01PMDL04 WP02). It is deliberately a
// SEPARATE type from registry.Registry's ProviderProfile map: connection
// config (credentials, endpoint, region) and behavioral config (prompt
// template, tool dialect, context policy, retry/recovery ladder) must
// resolve independently, per spec §5 — rotating a credential must never
// require re-promoting a ModelProfile, and activating a new ModelProfile
// version must never touch a ProviderProfile. Keeping this as its own
// type with its own mutex and its own storage makes that independence
// structural rather than a discipline someone has to remember.
//
// Family inheritance mirrors the proven pattern in
// core/llm/capabilities/loader.go (applyRichEntry / matchGlob): a
// ModelProfile.ID may itself be a glob (e.g. "claude-sonnet-*", WP01 doc),
// so a single flat list of entries — ranked by glob specificity rather
// than a separate "defaults" concept — reproduces "specific overrides
// family glob overrides provider-level default" once a catch-all "*"
// entry is registered as the least-specific match. core/llm/capabilities
// already imports core/llm, so this package cannot import capabilities
// back (import cycle) — the glob-matching logic below is a deliberate,
// small mirror of loader.go's matchGlob, not a restructuring of it.
type ModelProfileStore struct {
	mu      sync.RWMutex
	entries []modelProfileEntry
	seq     int
}

type modelProfileEntry struct {
	profile ModelProfile
	seq     int // load order; used only to break literal ties deterministically
}

// NewModelProfileStore returns an empty store. An empty store resolves
// every (model_id, version) to "not found" (ModelProfile{}, false, nil) —
// i.e. today's behavior, unchanged, per the mission's zero-behavior-change
// requirement.
func NewModelProfileStore() *ModelProfileStore {
	return &ModelProfileStore{}
}

// Load validates and installs profs into the store. On any validation
// failure, or on a duplicate (ID, Version) pair — either within profs or
// against an already-loaded entry — the store is left completely
// unchanged and a descriptive error is returned. This mirrors
// registry.Registry.LoadProfiles' collision handling for ProviderProfile,
// applied to the (ID, Version) composite key ModelProfile resolution
// actually keys off.
func (s *ModelProfileStore) Load(profs []ModelProfile) error {
	type key struct{ id, version string }
	seen := map[key]struct{}{}
	for _, p := range profs {
		if err := ValidateModelProfile(p); err != nil {
			return err
		}
		if p.IsZero() {
			return errors.New("llm: cannot load a zero-value model profile")
		}
		k := key{p.ID, p.Version}
		if _, ok := seen[k]; ok {
			return fmt.Errorf("llm: duplicate model profile (id=%q, version=%q) in input", p.ID, p.Version)
		}
		seen[k] = struct{}{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range seen {
		for _, e := range s.entries {
			if e.profile.ID == k.id && e.profile.Version == k.version {
				return fmt.Errorf("llm: model profile (id=%q, version=%q) already loaded", k.id, k.version)
			}
		}
	}
	for _, p := range profs {
		s.seq++
		s.entries = append(s.entries, modelProfileEntry{profile: p, seq: s.seq})
	}
	return nil
}

// Evict removes the entry matching the exact (id, version) pair, if any.
// It is the seam a bundle re-promotion uses to replace a stale version
// without tripping Load's "already loaded" collision (mirrors
// registry.Registry.Evict for ProviderProfile.ID). Idempotent: evicting a
// pair that was never loaded is a no-op.
func (s *ModelProfileStore) Evict(id, version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.entries[:0]
	for _, e := range s.entries {
		if e.profile.ID == id && e.profile.Version == version {
			continue
		}
		out = append(out, e)
	}
	s.entries = out
}

// Resolve returns the effective ModelProfile for (modelID, version).
//
// Matching entries — those whose Version equals the requested version
// exactly AND whose ID glob-matches modelID — are merged from least to
// most specific: a catch-all "*" entry (provider-level default) first,
// then a family-glob entry (e.g. "claude-sonnet-*"), then an exact
// (non-glob) entry last. Each layer's explicitly-set fields override
// the layer(s) beneath; fields left unset (nil pointer / zero / empty)
// fall through to whatever the less-specific layer already established.
// This is the WP08 merge fix applied to ModelProfile: pointer-nilness
// (or zero/empty for the plain-typed fields WP01 already documents as
// "empty means unset") distinguishes "this layer didn't mention it" from
// "this layer explicitly set it to false/zero", so an explicit override
// always wins and an unset field never wipes out an inherited value.
//
// Malformed input — an empty modelID or an empty version — is a clean
// error, never a silent fallback. When no entry matches (the family is
// unknown, OR entries exist for the family but not for the exact
// requested version), Resolve returns the zero ModelProfile with
// found=false and a nil error: WP02 must never silently substitute a
// different version's config, and "no profile configured" is required to
// mean exactly today's behavior (spec "zero behaviour change"), not an
// error condition.
func (s *ModelProfileStore) Resolve(modelID, version string) (profile ModelProfile, found bool, err error) {
	if strings.TrimSpace(modelID) == "" {
		return ModelProfile{}, false, errors.New("llm: model profile resolve: model_id is required")
	}
	if strings.TrimSpace(version) == "" {
		return ModelProfile{}, false, errors.New("llm: model profile resolve: version is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	type match struct {
		tier       int
		literalLen int
		seq        int
		profile    ModelProfile
	}
	var matches []match
	for _, e := range s.entries {
		if e.profile.Version != version {
			continue
		}
		if !modelProfileMatchGlob(e.profile.ID, modelID) {
			continue
		}
		tier, literalLen := modelProfileSpecificity(e.profile.ID)
		matches = append(matches, match{tier: tier, literalLen: literalLen, seq: e.seq, profile: e.profile})
	}
	if len(matches) == 0 {
		return ModelProfile{}, false, nil
	}

	// Least specific first: catch-all default (tier 0) < family glob
	// (tier 1, shorter literal before longer literal) < exact (tier 2).
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].tier != matches[j].tier {
			return matches[i].tier < matches[j].tier
		}
		if matches[i].literalLen != matches[j].literalLen {
			return matches[i].literalLen < matches[j].literalLen
		}
		return matches[i].seq < matches[j].seq
	})

	resolved := matches[0].profile
	for _, m := range matches[1:] {
		resolved = mergeModelProfile(resolved, m.profile)
	}
	// The resolved artifact describes exactly the requested
	// (model_id, version) — not whichever glob pattern happened to win —
	// so callers see a concrete, unambiguous identity.
	resolved.ID = modelID
	resolved.Version = version
	return resolved, true, nil
}

// modelProfileMatchGlob is a deliberate mirror of
// core/llm/capabilities/loader.go's matchGlob (same semantics: "*"
// matches everything, a trailing/leading "*" is a prefix/suffix glob,
// anything else is exact string equality). It cannot be imported instead
// of mirrored: capabilities already imports core/llm, so core/llm
// importing capabilities back would be a cycle. Do not restructure
// loader.go to work around this — mirroring a ~15-line helper is the
// deliberate, mission-directed tradeoff.
func modelProfileMatchGlob(pattern, s string) bool {
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(s, prefix)
	}
	if strings.HasPrefix(pattern, "*") {
		suffix := strings.TrimPrefix(pattern, "*")
		return strings.HasSuffix(s, suffix)
	}
	return pattern == s
}

// modelProfileSpecificity ranks a ModelProfile.ID pattern for merge
// ordering: tier 0 is the catch-all "*" (provider-level default), tier 1
// is any other glob (ranked by literal-character length so a narrower
// family glob outranks a broader one), tier 2 is an exact, non-glob ID
// (the most specific — always wins over any glob).
func modelProfileSpecificity(pattern string) (tier int, literalLen int) {
	if pattern == "*" {
		return 0, 0
	}
	if strings.Contains(pattern, "*") {
		return 1, len(strings.ReplaceAll(pattern, "*", ""))
	}
	return 2, len(pattern)
}

// mergeModelProfile overlays override's explicitly-set fields onto base,
// per-field, so a less-specific layer's value survives wherever the more
// specific layer left a field unset. See Resolve's doc for why this must
// be pointer-nilness (or zero/empty for WP01's plain-typed fields) rather
// than a wholesale struct replacement — a wholesale replacement is
// exactly the bug WP08 fixed for capability flags and must not be
// reintroduced here.
func mergeModelProfile(base, override ModelProfile) ModelProfile {
	out := base
	out.PromptTemplate = mergePromptTemplateRef(base.PromptTemplate, override.PromptTemplate)
	out.ToolDialect = mergeToolDialectConfig(base.ToolDialect, override.ToolDialect)
	out.Context = mergeContextPolicy(base.Context, override.Context)
	out.Retry = mergeRetryPolicy(base.Retry, override.Retry)
	if override.FallbackChainId != "" {
		out.FallbackChainId = override.FallbackChainId
	}
	out.EvalManifest = mergeEvalManifestRef(base.EvalManifest, override.EvalManifest)
	return out
}

func mergePromptTemplateRef(base, override *PromptTemplateRef) *PromptTemplateRef {
	if override == nil {
		return base
	}
	if base == nil {
		merged := *override
		return &merged
	}
	merged := *base
	if override.ID != "" {
		merged.ID = override.ID
	}
	if override.Version != "" {
		merged.Version = override.Version
	}
	if override.Format != "" {
		merged.Format = override.Format
	}
	// AttentionPlacement is a plain bool (WP01), so explicit-false cannot
	// be distinguished from unset; only an explicit true can override an
	// inherited value, matching the field's own "off by default" doc.
	if override.AttentionPlacement {
		merged.AttentionPlacement = true
	}
	return &merged
}

func mergeToolDialectConfig(base, override *ToolDialectConfig) *ToolDialectConfig {
	if override == nil {
		return base
	}
	if base == nil {
		merged := *override
		return &merged
	}
	merged := *base
	if override.Dialect != "" {
		merged.Dialect = override.Dialect
	}
	// Pointer-nilness, not zero-value: an explicit `false` must still
	// override an inherited `true` (or vice versa). This is the exact
	// shape of the WP08 bug — do not collapse this to a zero-value check.
	if override.ParallelToolCalls != nil {
		merged.ParallelToolCalls = override.ParallelToolCalls
	}
	if override.MaxToolDescriptionBytes != 0 {
		merged.MaxToolDescriptionBytes = override.MaxToolDescriptionBytes
	}
	return &merged
}

func mergeContextPolicy(base, override *ContextPolicy) *ContextPolicy {
	if override == nil {
		return base
	}
	if base == nil {
		merged := *override
		return &merged
	}
	merged := *base
	if override.Aggressiveness != "" {
		merged.Aggressiveness = override.Aggressiveness
	}
	if override.ContextWindowOverride != 0 {
		merged.ContextWindowOverride = override.ContextWindowOverride
	}
	return &merged
}

func mergeRetryPolicy(base, override *RetryPolicy) *RetryPolicy {
	if override == nil {
		return base
	}
	if base == nil {
		merged := *override
		return &merged
	}
	merged := *base
	if override.MaxAttempts != 0 {
		merged.MaxAttempts = override.MaxAttempts
	}
	if override.BaseMS != 0 {
		merged.BaseMS = override.BaseMS
	}
	if override.MaxMS != 0 {
		merged.MaxMS = override.MaxMS
	}
	if override.Jitter != "" {
		merged.Jitter = override.Jitter
	}
	return &merged
}

func mergeEvalManifestRef(base, override *EvalManifestRef) *EvalManifestRef {
	if override == nil {
		return base
	}
	if base == nil {
		merged := *override
		return &merged
	}
	merged := *base
	if override.ID != "" {
		merged.ID = override.ID
	}
	if override.Version != "" {
		merged.Version = override.Version
	}
	return &merged
}
