package nodes

import (
	"fmt"
	"sort"
)

// resolveAll walks every parsed manifest, resolves the `extends:` chain
// for each, and returns a map keyed by ID of the merged ResolvedManifest.
//
// Resolution rules (FR-004):
//  1. Single-parent inheritance only. A manifest declares zero or one
//     `extends:`.
//  2. Cycle detection: an `extends:` cycle is a load-time error. The
//     resolver maintains a per-walk stack and reports the offending
//     chain in the error message.
//  3. Deep-merge:
//     - scalars at the child level override the parent
//     - list fields (Aliases, Ports.Inputs, Ports.Outputs) replace whole
//     - the `Attrs` map merges key-by-key (attr-level deep merge)
//     - the `Defaults` map merges key-by-key
//  4. Archetype not-callable invariant (FR-007): an archetype manifest
//     keeps Callable=false; a kind that does NOT declare an executor is
//     left as-is here (the validator surfaces the error in WP03).
//
// The resolver does NOT itself surface `ErrArchetypeNotCallable` — that
// fires when a runtime tries to instantiate an archetype, not when one
// is loaded. We only emit sentinel errors for cycles and missing
// parents at this layer.
func resolveAll(byID map[string]*Manifest) (map[string]*ResolvedManifest, error) {
	out := make(map[string]*ResolvedManifest, len(byID))
	// Resolve in deterministic order so error messages are stable
	// across runs.
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		resolved, err := resolveOne(id, byID, nil)
		if err != nil {
			return nil, err
		}
		out[id] = resolved
	}
	return out, nil
}

// resolveOne resolves a single manifest by walking its extends chain
// root-first (the root archetype contributes first; the kind layer
// overrides last). The `stack` argument is used for cycle detection.
func resolveOne(id string, byID map[string]*Manifest, stack []string) (*ResolvedManifest, error) {
	// Cycle detection: walk the stack first.
	for _, prior := range stack {
		if prior == id {
			chain := append([]string{}, stack...)
			chain = append(chain, id)
			return nil, fmt.Errorf("%w: %s", ErrCycleDetected, formatChain(chain))
		}
	}
	m, ok := byID[id]
	if !ok {
		return nil, fmt.Errorf("manifest %q: extends references missing parent", id)
	}

	stack = append(stack, id)

	// Recursively resolve the parent first (root-first walk).
	if m.Extends != "" {
		parentResolved, err := resolveOne(m.Extends, byID, stack)
		if err != nil {
			return nil, err
		}
		// Merge parent layer first, then overlay this manifest.
		merged := cloneManifest(parentResolved.Manifest)
		prov := clonProvenance(parentResolved.Provenance)
		mergeOverlay(&merged, prov, m, id)
		// Pin canonical metadata sourced from this layer regardless of
		// merge rules.
		merged.ID = id
		// The Extends field on the merged manifest points at the
		// child's parent (matches authoring expectation).
		merged.Extends = m.Extends
		// The KindName defaults to the leaf ID.
		if merged.KindName == "" {
			merged.KindName = id
		}
		// Apply the callable default after the overlay so an explicit
		// `callable: false` on the parent does not silently propagate to
		// a kind layer that left it unset.
		applyCallableDefault(&merged, m)

		chain := append([]string{}, parentResolved.Chain...)
		chain = append(chain, id)
		return &ResolvedManifest{
			Manifest:   merged,
			Chain:      chain,
			Provenance: prov,
			Hash:       "",
			Source:     "",
		}, nil
	}
	// Root of the chain: produce a self-resolved manifest.
	root := cloneManifest(*m)
	prov := Provenance{}
	stampSelfProvenance(&root, prov, id)
	if root.KindName == "" {
		root.KindName = id
	}
	applyCallableDefault(&root, m)
	return &ResolvedManifest{
		Manifest:   root,
		Chain:      []string{id},
		Provenance: prov,
		Hash:       "",
		Source:     "",
	}, nil
}

// applyCallableDefault encodes the rule "archetype manifests default to
// non-callable; kind manifests default to callable, unless they
// explicitly set `callable: false`". Called after merging so we honour
// the leaf manifest's intent rather than inheriting a parent's value.
//
// We also normalise the `Archetype` field on the merged shape: for a
// kind leaf, the merged Archetype is cleared so `IsArchetype()` returns
// false (the parent archetype's name otherwise leaks through via
// cloneManifest copying every field). For an archetype leaf, the
// Archetype field is set to the leaf's id so callers can identify
// the layer.
func applyCallableDefault(merged *Manifest, leaf *Manifest) {
	switch {
	case leaf.IsArchetype():
		// Archetype manifests are non-callable; preserve the leaf's
		// archetype-name discriminator on the merged shape.
		merged.Callable = boolPtr(false)
		merged.Archetype = leaf.Archetype
	case leaf.Callable != nil:
		merged.Callable = boolPtr(*leaf.Callable)
		// Kind layers explicitly clear the inherited archetype name.
		merged.Archetype = ""
	default:
		// Concrete kind, no explicit callable: default to true.
		merged.Callable = boolPtr(true)
		merged.Archetype = ""
	}
}

// mergeOverlay overlays `child` on `merged` and records provenance
// for every changed field. Called once per layer in the inheritance
// walk.
func mergeOverlay(merged *Manifest, prov Provenance, child *Manifest, layerID string) {
	if child.SchemaVersion != "" {
		merged.SchemaVersion = child.SchemaVersion
		prov["schema_version"] = layerID
	}
	if child.KindName != "" {
		merged.KindName = child.KindName
		prov["kind_name"] = layerID
	}
	if child.DisplayName != "" {
		merged.DisplayName = child.DisplayName
		prov["display_name"] = layerID
	}
	if child.Description != "" {
		merged.Description = child.Description
		prov["description"] = layerID
	}
	if child.Category != "" {
		merged.Category = child.Category
		prov["category"] = layerID
	}
	if child.Budget != "" {
		merged.Budget = child.Budget
		prov["budget"] = layerID
	}
	if len(child.BudgetConsumes) > 0 {
		merged.BudgetConsumes = append([]string(nil), child.BudgetConsumes...)
		prov["budget_consumes"] = layerID
	}
	if child.Executor != "" {
		merged.Executor = child.Executor
		prov["executor"] = layerID
	}
	if len(child.Aliases) > 0 {
		merged.Aliases = append([]string(nil), child.Aliases...)
		prov["aliases"] = layerID
	}
	if child.Version != "" {
		merged.Version = child.Version
		prov["version"] = layerID
	}
	// Ports: whole-list replace per FR-004.
	if len(child.Ports.Inputs) > 0 {
		merged.Ports.Inputs = clonePorts(child.Ports.Inputs)
		prov["ports.inputs"] = layerID
	}
	if len(child.Ports.Outputs) > 0 {
		merged.Ports.Outputs = clonePorts(child.Ports.Outputs)
		prov["ports.outputs"] = layerID
	}
	// Attrs: key-by-key merge with whole-spec replace at attr-name level.
	if len(child.Attrs) > 0 {
		if merged.Attrs == nil {
			merged.Attrs = map[string]AttrSpec{}
		}
		for name, spec := range child.Attrs {
			merged.Attrs[name] = spec
			prov["attrs."+name] = layerID
		}
	}
	// Defaults: key-by-key merge.
	if len(child.Defaults) > 0 {
		if merged.Defaults == nil {
			merged.Defaults = map[string]interface{}{}
		}
		for name, val := range child.Defaults {
			merged.Defaults[name] = val
			prov["defaults."+name] = layerID
		}
	}
}

// stampSelfProvenance records every field the root manifest carries. The
// root is the only layer where every set field is "self-sourced".
func stampSelfProvenance(m *Manifest, prov Provenance, layerID string) {
	if m.SchemaVersion != "" {
		prov["schema_version"] = layerID
	}
	if m.KindName != "" {
		prov["kind_name"] = layerID
	}
	if m.DisplayName != "" {
		prov["display_name"] = layerID
	}
	if m.Description != "" {
		prov["description"] = layerID
	}
	if m.Category != "" {
		prov["category"] = layerID
	}
	if m.Budget != "" {
		prov["budget"] = layerID
	}
	if len(m.BudgetConsumes) > 0 {
		prov["budget_consumes"] = layerID
	}
	if m.Executor != "" {
		prov["executor"] = layerID
	}
	if len(m.Aliases) > 0 {
		prov["aliases"] = layerID
	}
	if m.Version != "" {
		prov["version"] = layerID
	}
	if len(m.Ports.Inputs) > 0 {
		prov["ports.inputs"] = layerID
	}
	if len(m.Ports.Outputs) > 0 {
		prov["ports.outputs"] = layerID
	}
	for name := range m.Attrs {
		prov["attrs."+name] = layerID
	}
	for name := range m.Defaults {
		prov["defaults."+name] = layerID
	}
}

// cloneManifest returns a deep copy so merges never alias the input.
func cloneManifest(m Manifest) Manifest {
	out := m
	if m.BudgetConsumes != nil {
		out.BudgetConsumes = append([]string(nil), m.BudgetConsumes...)
	}
	if m.Aliases != nil {
		out.Aliases = append([]string(nil), m.Aliases...)
	}
	out.Ports.Inputs = clonePorts(m.Ports.Inputs)
	out.Ports.Outputs = clonePorts(m.Ports.Outputs)
	if m.Attrs != nil {
		out.Attrs = make(map[string]AttrSpec, len(m.Attrs))
		for k, v := range m.Attrs {
			out.Attrs[k] = cloneAttrSpec(v)
		}
	}
	if m.Defaults != nil {
		out.Defaults = make(map[string]interface{}, len(m.Defaults))
		for k, v := range m.Defaults {
			out.Defaults[k] = v
		}
	}
	if m.Callable != nil {
		out.Callable = boolPtr(*m.Callable)
	}
	return out
}

func clonePorts(in []PortSpec) []PortSpec {
	if in == nil {
		return nil
	}
	out := make([]PortSpec, len(in))
	copy(out, in)
	return out
}

func cloneAttrSpec(s AttrSpec) AttrSpec {
	out := s
	if s.Enum != nil {
		out.Enum = append([]string(nil), s.Enum...)
	}
	if s.Min != nil {
		v := *s.Min
		out.Min = &v
	}
	if s.Max != nil {
		v := *s.Max
		out.Max = &v
	}
	if s.MinLength != nil {
		v := *s.MinLength
		out.MinLength = &v
	}
	if s.MaxLength != nil {
		v := *s.MaxLength
		out.MaxLength = &v
	}
	return out
}

func clonProvenance(in Provenance) Provenance {
	out := make(Provenance, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// formatChain renders `[a, b, c, a]` as `a → b → c → a` for cycle errors.
func formatChain(chain []string) string {
	out := ""
	for i, s := range chain {
		if i > 0 {
			out += " → "
		}
		out += s
	}
	return out
}
