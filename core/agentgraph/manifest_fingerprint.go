// Package agentgraph — manifest fingerprint helpers.
//
// A behavior_fingerprint is a stable sha256 hex digest over the
// behavior-affecting subset of a resolved manifest:
//
//   - port names, types, and required flags (sorted by name)
//   - attribute names, types, and defaults (sorted by name)
//   - the BudgetSignature
//
// Display-only fields (description, display_name, category, aliases,
// executor symbol) are intentionally excluded so cosmetic edits never
// invalidate stored graphs.
//
// Mission: manifest-versioning-01NDFSEX02, WP01.
package agentgraph

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/nodes"
)

// fingerprintInput is the canonical, JSON-serializable shape we hash.
// Field order is fixed; go/json marshals struct fields in declaration order.
type fingerprintInput struct {
	Kind    string            `json:"kind"`
	Budget  string            `json:"budget"`
	Inputs  []fingerprintPort `json:"inputs"`
	Outputs []fingerprintPort `json:"outputs"`
	Attrs   []fingerprintAttr `json:"attrs"`
}

type fingerprintPort struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
}

type fingerprintAttr struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Default any    `json:"default,omitempty"`
}

// ComputeManifestFingerprint returns the sha256 hex digest for the
// behavior-affecting subset of rm. The digest is stable across process
// restarts and Go versions because the input is deterministically
// serialized via encoding/json (struct fields in declaration order,
// keys sorted in maps).
//
// Returns an error only if JSON marshaling fails (not expected in
// practice; the types are all JSON-safe).
func ComputeManifestFingerprint(rm *nodes.ResolvedManifest) (string, error) {
	if rm == nil {
		return "", fmt.Errorf("manifest_fingerprint: nil ResolvedManifest")
	}
	m := &rm.Manifest

	// Canonicalize the kind name (mirrors codegen convention).
	kind := m.KindName
	if kind == "" {
		kind = m.ID
	}

	// Ports — sorted by name for stability.
	inputs := sortedPorts(m.Ports.Inputs)
	outputs := sortedPorts(m.Ports.Outputs)

	// Attrs — sorted by name for stability.
	attrNames := make([]string, 0, len(m.Attrs))
	for name := range m.Attrs {
		attrNames = append(attrNames, name)
	}
	sort.Strings(attrNames)

	attrs := make([]fingerprintAttr, 0, len(attrNames))
	for _, name := range attrNames {
		spec := m.Attrs[name]
		// Pull default from the Defaults map if not in the AttrSpec.
		var dflt any
		if spec.Default != nil {
			dflt = spec.Default
		} else if m.Defaults != nil {
			if v, ok := m.Defaults[name]; ok {
				dflt = v
			}
		}
		attrs = append(attrs, fingerprintAttr{
			Name:    name,
			Type:    string(spec.Type),
			Default: dflt,
		})
	}

	budget := string(m.Budget)
	inp := fingerprintInput{
		Kind:    kind,
		Budget:  budget,
		Inputs:  inputs,
		Outputs: outputs,
		Attrs:   attrs,
	}

	data, err := json.Marshal(inp)
	if err != nil {
		return "", fmt.Errorf("manifest_fingerprint: marshal: %w", err)
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:]), nil
}

func sortedPorts(specs []nodes.PortSpec) []fingerprintPort {
	out := make([]fingerprintPort, len(specs))
	for i, p := range specs {
		out[i] = fingerprintPort{Name: p.Name, Type: p.Type, Required: p.Required}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
