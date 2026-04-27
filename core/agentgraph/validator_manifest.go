package agentgraph

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/sigil-tech/kaneaz-harness/core/agentgraph/nodes"
)

// This file implements the manifest-driven half of the validator
// (mission agent-kernel-graph-node-catalog-01KQ7JDZ, WP03).
//
// Background: prior to WP03 the validator hand-coded every per-kind rule
// (required attrs, min/max ranges, enums, port types) inline in
// validator.go. WP03 replaces those checks with consultation of the
// resolved manifest catalog (`core/agentgraph/nodes`).
//
// Phasing: WP03 lands the manifest-driven checks as an ADDITIVE layer.
// The catalog presently ships only archetype manifests; callable kinds
// (`llm`, `tool`, `branch`, ...) do NOT yet have kind manifests
// registered. WP04 lands those manifests. Until then:
//   - When a kind has a registered manifest, the manifest-driven path
//     fires and emits FR-015-shaped errors.
//   - When a kind has NO registered manifest, the validator falls back
//     to the existing hand-coded per-attr Validate() methods.
// This keeps the cutover safe: parent-mission tests continue to pass
// while WP04 staggers the kind manifests in.
//
// The validator also gains the FR-016 archetype-non-callable rule:
// when the catalog reports a manifest with `IsCallable() == false`, a
// graph that names that kind directly is rejected with a message
// pointing at the offending node + the archetype id.

// CatalogProvider is the narrow interface validator-manifest checks
// consult to resolve a kind to its manifest. *nodes.Catalog satisfies
// this interface; tests can supply a fake without importing the
// concrete type.
//
// We declare the interface here (not in seams.go) because it is
// validator-private — the kernel does not consult it.
type CatalogProvider interface {
	Get(id string) (*nodes.ResolvedManifest, error)
	IsCallable(id string) bool
}

// catalogOnce + defaultCatalog memoise the package-level fallback
// catalog so the validator can call validateAgainstManifest without an
// explicit injection. The first call loads shipped manifests once;
// failures are recorded but never panic — callers that don't supply a
// catalog continue to get the hand-coded validation path.
var (
	catalogOnce sync.Once
	defaultCat  *nodes.Catalog
)

// defaultCatalog returns the lazily-initialised package catalog. Loads
// shipped manifests on first call. Returns nil + an error on load
// failure; the validator treats nil as "no manifest-driven path
// available, fall back to hand-coded checks".
func defaultCatalog() *nodes.Catalog {
	catalogOnce.Do(func() {
		cat, err := nodes.LoadCatalog(nodes.LoadOptions{})
		if err != nil {
			// Best-effort: keep whatever the loader returned (it may be
			// non-nil with a partial set even on resolver error). The
			// validator falls back to hand-coded checks for any kind
			// not registered.
			defaultCat = cat
			return
		}
		defaultCat = cat
	})
	return defaultCat
}

// WithCatalog plugs in an explicit manifest catalog for the duration
// of one Validate call. Tests use this to drive deterministic catalogs
// (no embedded fixture coupling). Passing a nil CatalogProvider
// disables the manifest-driven path outright (useful for tests
// asserting hand-coded behaviour in isolation).
func WithCatalog(c CatalogProvider) ValidateOption {
	return func(cfg *validateConfig) {
		cfg.catalog = c
		cfg.catalogExplicit = true
	}
}

// validateAgainstManifest runs every attr-level rule the manifest
// declares against the node's typed attrs. Emits FR-015-shaped errors:
// `node "<id>" violates manifest <kind>.attrs.<name>.<rule>: got <v>,
// want <bound>`.
//
// The function is tolerant: a missing attr (when the manifest demands
// it) is reported once; range/enum violations on a present attr are
// reported per-attr. Unknown attr names on the node payload are NOT
// reported here — the typed Attrs struct discards unknown keys at JSON
// decode, so by the time we reach this function the node carries only
// the attrs the typed struct knows about.
func validateAgainstManifest(node *Node, rm *nodes.ResolvedManifest) []string {
	if rm == nil {
		return nil
	}
	values, err := attrsToMap(node.Attrs)
	if err != nil {
		return []string{fmt.Sprintf(
			"schema: node %q: cannot inspect attrs for manifest validation: %v",
			node.ID, err,
		)}
	}
	var issues []string
	// Walk attrs in declaration-stable order (map iteration is random;
	// sorting keeps error output deterministic).
	for _, name := range sortedAttrNames(rm.Manifest.Attrs) {
		spec := rm.Manifest.Attrs[name]
		issues = append(issues, validateAttr(node.ID, rm.Manifest.ID, name, spec, values)...)
	}
	return issues
}

// validateAttr applies one AttrSpec against the value present (if any)
// on the node. Returns a list of FR-015-shaped error strings.
func validateAttr(nodeID, kindID, attrName string, spec nodes.AttrSpec, values map[string]any) []string {
	// Determine the wire-name for the attr in the values map. AttrSpec
	// may override via JSONField; fall back to the attr-map key.
	wireName := attrName
	if spec.JSONField != "" {
		wireName = spec.JSONField
	}
	raw, present := values[wireName]
	// Null in the values map is treated as "absent".
	if raw == nil {
		present = false
	}
	// Zero values count as "absent" for required-attr purposes. Without
	// this rule, a typed struct's zero-value field (e.g. Model="") would
	// silently satisfy the manifest's `required: true` because JSON
	// marshalling preserves it as "" rather than dropping it. The
	// hand-coded path treats "" as "missing"; we mirror that here.
	if present && isZeroValue(spec.Type, raw) {
		present = false
	}

	if !present {
		if spec.Required {
			return []string{manifestErrf(nodeID, kindID, attrName, "required",
				"attr is missing", "value to be supplied")}
		}
		// Nothing to check on an absent optional attr.
		return nil
	}

	var issues []string

	// Type-shape checks first; downstream range/enum checks assume the
	// value is the right shape.
	if msg := checkAttrType(spec.Type, raw); msg != "" {
		issues = append(issues, manifestErrf(nodeID, kindID, attrName, "type",
			fmt.Sprintf("got %T", raw), msg))
		// If the type is wrong we cannot meaningfully continue.
		return issues
	}

	// Numeric min/max.
	if spec.Min != nil || spec.Max != nil {
		if asNum, ok := numericValue(raw); ok {
			if spec.Min != nil && asNum < *spec.Min {
				issues = append(issues, manifestErrf(nodeID, kindID, attrName, "min",
					formatNum(asNum), fmt.Sprintf(">= %s", formatNum(*spec.Min))))
			}
			if spec.Max != nil && asNum > *spec.Max {
				issues = append(issues, manifestErrf(nodeID, kindID, attrName, "max",
					formatNum(asNum), fmt.Sprintf("<= %s", formatNum(*spec.Max))))
			}
		}
	}

	// Enum.
	if spec.Type == nodes.AttrTypeEnum && len(spec.Enum) > 0 {
		s, ok := raw.(string)
		if ok {
			if !containsString(spec.Enum, s) {
				issues = append(issues, manifestErrf(nodeID, kindID, attrName, "enum",
					fmt.Sprintf("%q", s),
					fmt.Sprintf("one of {%s}", strings.Join(spec.Enum, "|"))))
			}
		}
	}

	// String length.
	if spec.Type == nodes.AttrTypeString {
		if s, ok := raw.(string); ok {
			if spec.MinLength != nil && len(s) < *spec.MinLength {
				issues = append(issues, manifestErrf(nodeID, kindID, attrName, "min_length",
					fmt.Sprintf("len=%d", len(s)),
					fmt.Sprintf(">= %d", *spec.MinLength)))
			}
			if spec.MaxLength != nil && len(s) > *spec.MaxLength {
				issues = append(issues, manifestErrf(nodeID, kindID, attrName, "max_length",
					fmt.Sprintf("len=%d", len(s)),
					fmt.Sprintf("<= %d", *spec.MaxLength)))
			}
		}
	}

	// List length.
	if isListAttrType(spec.Type) {
		if list, ok := raw.([]any); ok {
			if spec.MinLength != nil && len(list) < *spec.MinLength {
				issues = append(issues, manifestErrf(nodeID, kindID, attrName, "min_length",
					fmt.Sprintf("len=%d", len(list)),
					fmt.Sprintf(">= %d", *spec.MinLength)))
			}
			if spec.MaxLength != nil && len(list) > *spec.MaxLength {
				issues = append(issues, manifestErrf(nodeID, kindID, attrName, "max_length",
					fmt.Sprintf("len=%d", len(list)),
					fmt.Sprintf("<= %d", *spec.MaxLength)))
			}
		}
	}

	return issues
}

// validatePortsAgainstManifest enforces port surface rules against the
// manifest: every declared input/output port the manifest names must
// exist on the node (after default-fallback), and any port the manifest
// marks `required: true` (via PortSpec.Description heuristics — note:
// the WP01 PortSpec lacks a Required field; we honour the underlying
// agentgraph Port.Required after substitution). The validator already
// covers required-input-must-be-connected via checkEdges; this
// function reports manifest-port mismatches that the legacy hand-coded
// path silently accepted.
//
// The function emits FR-015-shaped errors for type mismatches between
// the declared port type and the manifest's expectation, and for ports
// the manifest names but the node's effective surface omits.
func validatePortsAgainstManifest(node *Node, rm *nodes.ResolvedManifest) []string {
	if rm == nil {
		return nil
	}
	var issues []string
	effectiveIns, effectiveOuts := portSurface(node)

	// Inputs: every manifest-declared input port should be present on
	// the effective surface with a compatible type.
	for _, mp := range rm.Manifest.Ports.Inputs {
		mpName := strings.TrimSpace(mp.Name)
		if mpName == "" {
			continue
		}
		actual, ok := lookupPort(effectiveIns, mpName)
		if !ok {
			issues = append(issues, manifestPortErrf(node.ID, rm.Manifest.ID,
				"inputs."+mpName, "missing",
				"effective surface omits port", "port to be declared"))
			continue
		}
		// Type compatibility: PortSpec.Type is the manifest's expectation;
		// PortType.Compatible runs the existing `any`-aware check.
		if !PortType(mp.Type).Compatible(actual.Type) {
			issues = append(issues, manifestPortErrf(node.ID, rm.Manifest.ID,
				"inputs."+mpName, "type",
				fmt.Sprintf("got %s", actual.Type),
				fmt.Sprintf("compatible with %s", mp.Type)))
		}
	}
	for _, mp := range rm.Manifest.Ports.Outputs {
		mpName := strings.TrimSpace(mp.Name)
		if mpName == "" {
			continue
		}
		actual, ok := lookupPort(effectiveOuts, mpName)
		if !ok {
			issues = append(issues, manifestPortErrf(node.ID, rm.Manifest.ID,
				"outputs."+mpName, "missing",
				"effective surface omits port", "port to be declared"))
			continue
		}
		if !PortType(mp.Type).Compatible(actual.Type) {
			issues = append(issues, manifestPortErrf(node.ID, rm.Manifest.ID,
				"outputs."+mpName, "type",
				fmt.Sprintf("got %s", actual.Type),
				fmt.Sprintf("compatible with %s", mp.Type)))
		}
	}
	return issues
}

// archetypeNotCallableMsg builds the FR-016 error message for a graph
// that names an archetype directly. The message points at the offending
// node + the archetype id and (when WP04 lands the kind manifests)
// suggests concrete alternatives by listing every kind whose chain
// includes the archetype.
func archetypeNotCallableMsg(nodeID string, rm *nodes.ResolvedManifest, alternatives []string) string {
	suffix := ""
	if len(alternatives) > 0 {
		suffix = "; use a concrete kind: " + strings.Join(alternatives, " | ")
	}
	return fmt.Sprintf(
		"schema: node %q: archetype %q is not directly callable in v1%s",
		nodeID, rm.Manifest.ID, suffix,
	)
}

// manifestErrf builds a FR-015-shaped error string.
//
//	node "<id>" violates manifest <kind>.attrs.<name>.<rule>: got <v>, want <bound>
func manifestErrf(nodeID, kindID, attrName, rule, got, want string) string {
	return fmt.Sprintf(
		"schema: node %q violates manifest %s.attrs.%s.%s: got %s, want %s",
		nodeID, kindID, attrName, rule, got, want,
	)
}

// manifestPortErrf is the port-rule counterpart to manifestErrf.
//
//	node "<id>" violates manifest <kind>.ports.<inputs|outputs>.<name>.<rule>: got <v>, want <bound>
func manifestPortErrf(nodeID, kindID, portPath, rule, got, want string) string {
	return fmt.Sprintf(
		"port: node %q violates manifest %s.ports.%s.%s: got %s, want %s",
		nodeID, kindID, portPath, rule, got, want,
	)
}

// attrsToMap re-encodes a typed NodeAttrs through its JSON tags, then
// decodes back to a map[string]any. This mirrors the wire decoder's
// path without reaching into the typed struct's reflect surface — the
// JSON-tag-based map view is the canonical "what the manifest knows
// about" projection.
//
// Returns an empty map (not nil) when attrs is nil; the manifest's
// required-attr check then sees every required attr as missing.
func attrsToMap(attrs NodeAttrs) (map[string]any, error) {
	if attrs == nil {
		return map[string]any{}, nil
	}
	buf, err := json.Marshal(attrs)
	if err != nil {
		return nil, err
	}
	if len(buf) == 0 || string(buf) == "null" {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(buf, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

// checkAttrType returns "" when raw matches the AttrType, otherwise a
// human-readable hint of the expected type for the FR-015 message.
//
// JSON / YAML promote integers to float64; we accept whole-number
// float64 for AttrTypeInt to match the dial-validation rules in
// validator.go.
func checkAttrType(t nodes.AttrType, raw any) string {
	switch t {
	case nodes.AttrTypeString, nodes.AttrTypeEnum,
		nodes.AttrTypeModelRef, nodes.AttrTypeToolRef,
		nodes.AttrTypeActivityRef, nodes.AttrTypeCorpusRef,
		nodes.AttrTypeAttachmentRef, nodes.AttrTypeNodeIDRef,
		nodes.AttrTypePortRef, nodes.AttrTypeMessagesRef,
		nodes.AttrTypeDuration:
		if _, ok := raw.(string); ok {
			return ""
		}
		return "string"
	case nodes.AttrTypeInt:
		switch v := raw.(type) {
		case int, int8, int16, int32, int64:
			return ""
		case uint, uint8, uint16, uint32, uint64:
			return ""
		case float64:
			if v == float64(int64(v)) {
				return ""
			}
			return "int"
		case float32:
			if float64(v) == float64(int64(v)) {
				return ""
			}
			return "int"
		}
		return "int"
	case nodes.AttrTypeFloat:
		switch raw.(type) {
		case int, int8, int16, int32, int64:
			return ""
		case uint, uint8, uint16, uint32, uint64:
			return ""
		case float32, float64:
			return ""
		}
		return "number"
	case nodes.AttrTypeBool:
		if _, ok := raw.(bool); ok {
			return ""
		}
		return "bool"
	case nodes.AttrTypeArray, nodes.AttrTypeStringSlice:
		if _, ok := raw.([]any); ok {
			return ""
		}
		// Pre-typed slices sometimes survive json round-trip as
		// []interface{} (mostly), but accept []string too just in case.
		if _, ok := raw.([]string); ok {
			return ""
		}
		return "array"
	case nodes.AttrTypeMap, nodes.AttrTypeObject:
		if _, ok := raw.(map[string]any); ok {
			return ""
		}
		return "object"
	}
	// Unknown types: pass through. Meta-validation should have caught
	// these at manifest load.
	return ""
}

// numericValue extracts a float64 from int or float types. Used for
// min/max bounds checks. Returns ok=false for non-numeric inputs.
func numericValue(raw any) (float64, bool) {
	switch v := raw.(type) {
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	}
	return 0, false
}

// formatNum renders a float64 as int when it's a whole number, else
// with up to 3 decimals — keeps error messages readable.
func formatNum(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%g", f)
}

// containsString reports whether s appears in xs.
func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// isListAttrType reports whether the AttrType is array-shaped.
func isListAttrType(t nodes.AttrType) bool {
	return t == nodes.AttrTypeArray || t == nodes.AttrTypeStringSlice
}

// isZeroValue reports whether raw is the zero value for the AttrType.
// Used by the required-attr rule: a typed struct's zero field round-
// trips through JSON as a present-but-empty value (e.g. ""/0/false),
// and the manifest's `required: true` should reject those just like
// the hand-coded path does. Numeric attrs with min/max bounds whose
// range admits zero (e.g. min=0) get a separate min-rule violation if
// the value is below the stated minimum; the required-rule fires only
// when the field is genuinely absent at the typed-struct layer.
//
// The function is intentionally conservative: only string and list
// types are treated as "empty == absent". For numeric attrs, a zero
// value may be a legal in-range setting, and we let the min/max rules
// adjudicate. For bool, zero (false) is a valid value.
func isZeroValue(t nodes.AttrType, raw any) bool {
	switch t {
	case nodes.AttrTypeString, nodes.AttrTypeEnum,
		nodes.AttrTypeModelRef, nodes.AttrTypeToolRef,
		nodes.AttrTypeActivityRef, nodes.AttrTypeCorpusRef,
		nodes.AttrTypeAttachmentRef, nodes.AttrTypeNodeIDRef,
		nodes.AttrTypePortRef, nodes.AttrTypeMessagesRef,
		nodes.AttrTypeDuration:
		s, ok := raw.(string)
		return ok && s == ""
	case nodes.AttrTypeArray, nodes.AttrTypeStringSlice:
		switch v := raw.(type) {
		case []any:
			return len(v) == 0
		case []string:
			return len(v) == 0
		}
		return false
	case nodes.AttrTypeMap, nodes.AttrTypeObject:
		switch v := raw.(type) {
		case map[string]any:
			return len(v) == 0
		}
		return false
	}
	return false
}

// sortedAttrNames returns the attr-map keys in alphabetical order so
// validation error output is deterministic across runs.
func sortedAttrNames(m map[string]nodes.AttrSpec) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Tiny insertion sort — keeps the function dependency-free.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// archetypeAlternatives returns concrete-kind IDs whose chain includes
// the given archetype. Used by the FR-016 error message to suggest
// alternatives. Returns nil if no catalog is configured.
func archetypeAlternatives(c CatalogProvider, archetypeID string) []string {
	cat, ok := c.(*nodes.Catalog)
	if !ok {
		return nil
	}
	out := []string{}
	for _, rm := range cat.ListByArchetype(archetypeID) {
		if rm.Manifest.IsCallable() {
			out = append(out, rm.Manifest.ID)
		}
	}
	return out
}

// resolveCatalogManifest looks up a manifest by kind ID. Returns
// (rm, true, nil) on success; (nil, false, nil) when the kind is not
// registered (caller falls back to hand-coded path); (nil, false, err)
// on lookup error other than ErrUnknownKind.
func resolveCatalogManifest(c CatalogProvider, kind NodeKind) (*nodes.ResolvedManifest, bool, error) {
	if c == nil {
		return nil, false, nil
	}
	rm, err := c.Get(string(kind))
	if err == nil {
		return rm, true, nil
	}
	if errors.Is(err, nodes.ErrUnknownKind) {
		return nil, false, nil
	}
	return nil, false, err
}
