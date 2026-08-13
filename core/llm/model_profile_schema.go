package llm

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kameas-ai/kenaz-harness/core/compactionpolicy"
)

// ValidateModelProfile applies the versioned-model-profile-01PMDL04 WP01
// schema rules to a parsed ModelProfile. It mirrors ValidateProfile's
// style (core/llm/profile_schema.go) but validates the *behavioral*
// shape rather than connection/credential config.
//
// The zero value (IsZero()==true) is always valid — an absent profile
// means "today's behavior, unchanged", never a forced default, so it
// short-circuits before any field rule runs. This is the type/schema
// half of the mission only: nothing here yet rejects Cedar governance
// actions or budget-policy fields smuggled into a profile (WP06 extends
// this function to add that layering check) and nothing here resolves
// family inheritance or (model_id, version) lookups (WP02).
//
// Returns nil on success or a typed error describing the first failure.
func ValidateModelProfile(p ModelProfile) error {
	if p.IsZero() {
		return nil
	}
	if strings.TrimSpace(p.ID) == "" {
		return errors.New("llm: model profile id is required")
	}
	if strings.TrimSpace(p.Version) == "" {
		return fmt.Errorf("llm: model profile %q: version is required", p.ID)
	}
	if p.PromptTemplate != nil {
		if err := validatePromptTemplateRef(p.ID, p.PromptTemplate); err != nil {
			return err
		}
	}
	if p.ToolDialect != nil {
		if p.ToolDialect.MaxToolDescriptionBytes < 0 {
			return fmt.Errorf("llm: model profile %q: tool_dialect.max_tool_description_bytes must be >= 0", p.ID)
		}
	}
	if p.Context != nil {
		if err := validateContextPolicy(p.ID, p.Context); err != nil {
			return err
		}
	}
	if p.Retry != nil {
		if err := validateRetryPolicy(p.ID, p.Retry); err != nil {
			return err
		}
	}
	if p.EvalManifest != nil {
		if strings.TrimSpace(p.EvalManifest.ID) == "" {
			return fmt.Errorf("llm: model profile %q: eval_manifest.id is required when eval_manifest is set", p.ID)
		}
	}
	return nil
}

var knownPromptFormats = map[string]struct{}{
	"":         {}, // unset — default renderer
	"xml":      {},
	"markdown": {},
}

func validatePromptTemplateRef(profileID string, ref *PromptTemplateRef) error {
	if strings.TrimSpace(ref.ID) == "" {
		return fmt.Errorf("llm: model profile %q: prompt_template.id is required when prompt_template is set", profileID)
	}
	if _, ok := knownPromptFormats[ref.Format]; !ok {
		return fmt.Errorf("llm: model profile %q: unknown prompt_template.format %q (must be xml|markdown|\"\")", profileID, ref.Format)
	}
	return nil
}

// knownAggressiveness enumerates core/compactionpolicy's canonical tier
// constants. Built from the exported constants (not a locally-invented
// list) so this validator can't drift from compactionpolicy.Tier's own set.
var knownAggressiveness = map[compactionpolicy.CompactionAggressiveness]struct{}{
	"":                                 {}, // unset — session/app default
	compactionpolicy.AggressivenessOff: {},
	compactionpolicy.AggressivenessConservative: {},
	compactionpolicy.AggressivenessBalanced:     {},
	compactionpolicy.AggressivenessAggressive:   {},
	compactionpolicy.AggressivenessMaximal:      {},
}

func validateContextPolicy(profileID string, c *ContextPolicy) error {
	if _, ok := knownAggressiveness[c.Aggressiveness]; !ok {
		return fmt.Errorf("llm: model profile %q: unknown context.aggressiveness %q", profileID, c.Aggressiveness)
	}
	if c.ContextWindowOverride < 0 {
		return fmt.Errorf("llm: model profile %q: context.context_window_override must be >= 0", profileID)
	}
	return nil
}

// --- WP06: bundle-build layering validator -------------------------------
//
// The doc's tenet (versioned-model-profile-01PMDL04 spec §2 goal 6): "a
// model profile carries behaviour, never governance." Cedar authorization
// actions and budget/spend caps must live in core/policy/cedar and
// agentgraph.Budget respectively — never inside this hot-swappable
// behavioral artifact, because promoting a ModelProfile version must
// never be able to silently widen authorization or lift a spend cap and
// bypass the review path those subsystems have.
//
// ValidateModelProfile (above) cannot enforce this: ModelProfile has no
// free-form/map/any-typed field for governance data to occupy, so by the
// time a bundle payload has been narrowed into the typed struct, a
// "cedar:" or "budget:" block appended to the source YAML/JSON has
// already been silently dropped by encoding/json and gopkg.in/yaml.v3's
// default (tolerant-of-unknown-fields) unmarshal. A struct-level check
// against ModelProfile's declared fields would therefore never see the
// smuggled data and would pass trivially — a vacuous check that gives
// false confidence.
//
// The only place this can be enforced meaningfully is at the parse
// boundary, before unknown keys are dropped. ValidateModelProfileBundle
// is that boundary: it decodes raw bytes into a generic key tree and
// rejects any key (at any nesting depth reachable through ModelProfile's
// own declared object fields) that ModelProfile does not declare. This
// is strict-unknown-field rejection, derived by reflection from the
// struct's own yaml/json tags so the allow-list can't drift out of sync
// as fields are added.
//
// A side effect of strict-unknown-field rejection is that it also catches
// a typo'd legitimate key (e.g. "verison" for "version") with the same
// mechanism — that is intentional, not a bug: the boundary that stops
// governance-smuggling is the same boundary that would stop any other
// undeclared key. Bundle authors get a fail-closed, exact-schema
// experience; the error names the offending key either way, with a
// governance-specific hint when the key looks like a Cedar/budget/spend
// term so the message points authors at the right subsystem instead of
// making them guess.
func ValidateModelProfileBundle(raw []byte) (ModelProfile, error) {
	var generic any
	if err := yaml.Unmarshal(raw, &generic); err != nil {
		return ModelProfile{}, fmt.Errorf("llm: model profile bundle: %w", err)
	}
	if err := rejectForeignKeys("", generic, reflect.TypeOf(ModelProfile{})); err != nil {
		return ModelProfile{}, err
	}

	var p ModelProfile
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return ModelProfile{}, fmt.Errorf("llm: model profile bundle: %w", err)
	}
	if err := ValidateModelProfile(p); err != nil {
		return ModelProfile{}, err
	}
	return p, nil
}

// rejectForeignKeys recursively compares the keys actually present in a
// generically-decoded payload against the key set t (a ModelProfile or
// one of its nested object-typed fields) declares via yaml/json struct
// tags. Any key absent from that declared set fails closed.
func rejectForeignKeys(path string, node any, t reflect.Type) error {
	m, ok := asStringMap(node)
	if !ok {
		// Not an object at this level (scalar, list, or nil) — nothing
		// further to check; ValidateModelProfile handles value-shape
		// rules for fields that *are* declared.
		return nil
	}
	allowed := declaredKeys(t)
	for k, v := range m {
		full := k
		if path != "" {
			full = path + "." + k
		}
		childType, ok := allowed[k]
		if !ok {
			if hint, isGovernance := governanceKeyHint(k); isGovernance {
				return fmt.Errorf(
					"llm: model profile bundle: field %q is not permitted: %s (governance/budget policy must never live inside a ModelProfile)",
					full, hint,
				)
			}
			return fmt.Errorf("llm: model profile bundle: unknown field %q", full)
		}
		if childType != nil {
			if err := rejectForeignKeys(full, v, childType); err != nil {
				return err
			}
		}
	}
	return nil
}

// declaredKeys builds the set of yaml/json field names t's struct fields
// declare, mapping each to the (dereferenced) struct type of that field
// when the field is itself an object worth recursing into, or nil when
// it is a scalar/slice/other leaf. Built via reflection so it can never
// drift from ModelProfile's own declared shape as fields are added.
func declaredKeys(t reflect.Type) map[string]reflect.Type {
	out := make(map[string]reflect.Type)
	if t == nil || t.Kind() != reflect.Struct {
		return out
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}
		name, ok := yamlOrJSONTagName(f)
		if !ok {
			continue
		}
		ft := f.Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			out[name] = ft
		} else {
			out[name] = nil
		}
	}
	return out
}

// yamlOrJSONTagName extracts the wire field name from a struct field's
// yaml tag (preferred — bundles are YAML) falling back to its json tag.
// Returns ok==false for fields with no tag or an explicit "-".
func yamlOrJSONTagName(f reflect.StructField) (string, bool) {
	tag := f.Tag.Get("yaml")
	if tag == "" {
		tag = f.Tag.Get("json")
	}
	if tag == "" || tag == "-" {
		return "", false
	}
	name := strings.SplitN(tag, ",", 2)[0]
	if name == "" {
		return "", false
	}
	return name, true
}

// asStringMap normalizes the two shapes gopkg.in/yaml.v3 produces for a
// mapping node decoded into `any` (map[string]any is the common case;
// map[any]any / map[interface{}]interface{} can appear for non-string
// keys) into a single map[string]any for uniform key-set comparison.
func asStringMap(node any) (map[string]any, bool) {
	switch m := node.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, v := range m {
			ks, ok := k.(string)
			if !ok {
				ks = fmt.Sprintf("%v", k)
			}
			out[ks] = v
		}
		return out, true
	default:
		return nil, false
	}
}

// governanceKeyHint reports whether key looks like it is trying to carry
// Cedar authorization or budget/spend policy, returning a subsystem hint
// for the error message when so. Matching is substring/case-insensitive
// so both a bare "cedar"/"budget" key and compound variants (e.g.
// "cedar_actions", "budget_cap", "spend_limit") are caught by the same
// rule — the exact key is still named in the caller's error message.
func governanceKeyHint(key string) (string, bool) {
	lk := strings.ToLower(key)
	switch {
	case strings.Contains(lk, "cedar"):
		return "Cedar authorization policy belongs in core/policy/cedar", true
	case strings.Contains(lk, "budget"), strings.Contains(lk, "spend"), strings.Contains(lk, "cost_cap"):
		return "budget/spend policy belongs in agentgraph.Budget", true
	case strings.Contains(lk, "governance"), strings.Contains(lk, "authoriz"), strings.Contains(lk, "entitlement"):
		return "authorization/governance policy must live in its own subsystem, not a model profile", true
	default:
		return "", false
	}
}
