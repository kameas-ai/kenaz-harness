package capabilities

import (
	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// Gate enforces FR-013: a request that opts into a capability the
// (provider, model) does not support fails before any wire call.
type Gate struct {
	cat *Catalog
}

// NewGate returns a Gate bound to cat.
func NewGate(cat *Catalog) *Gate {
	return &Gate{cat: cat}
}

// Check returns ErrCapabilityUnsupported when req opts into any
// capability the descriptor for (profile.Kind, profile.Model) does
// not advertise. The descriptor is also returned so callers can
// reuse it (e.g., adapters that only emit cache markers when the
// model supports caching).
func (g *Gate) Check(req llm.GenerationRequest, prof llm.ProviderProfile) (llm.CapabilityDescriptor, error) {
	desc := g.cat.Describe(prof.Kind, prof.Model)
	want := req.RequestedCapabilities()
	missing := make([]llm.Capability, 0)
	for _, c := range want {
		if !desc.Has(c) {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		return desc, &llm.ErrCapabilityUnsupported{
			Provider:     prof.Kind,
			Model:        prof.Model,
			Capabilities: missing,
		}
	}
	return desc, nil
}
