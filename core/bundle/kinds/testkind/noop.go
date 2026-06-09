// Package testkind provides a no-op artifact-kind handler used by the
// bundle layer's integration tests. It is intentionally trivial — the
// kind id is "noop", Parse just records that the bytes were read,
// Activate appends to an in-memory journal, and Deactivate pops from
// the same journal.
//
// Downstream missions add real kind handlers in their own packages
// (core/llm/profilekind/, core/mcp/serverkind/, etc.). This package
// proves the registry plumbing without those dependencies — confirming
// the SC-005 contract that adding a kind requires no edit outside its
// own package.
package testkind

import (
	"context"
	"io"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/bundle/kinds"
)

// Kind is the registered kind id.
const Kind = "noop"

// Handler is a kinds.ArtifactKindHandler that records every Activate
// in an in-memory journal so tests can assert ordering.
type Handler struct {
	mu      sync.Mutex
	Journal []ActivationEvent
}

// ActivationEvent is one entry in the test journal.
type ActivationEvent struct {
	Bundle    string
	Artifact  string
	Activated bool // false means deactivated
}

func (h *Handler) Kind() string         { return Kind }
func (h *Handler) ParamSchema() []byte  { return nil }

// parsedRecord is the opaque value returned from Parse.
type parsedRecord struct {
	Bytes      []byte
	Bundle     string
	BundleVer  string
	Name       string
	BundleRoot string
}

func (h *Handler) Parse(_ context.Context, src kinds.ArtifactSource) (kinds.Parsed, error) {
	if src.Reader == nil {
		return parsedRecord{Bundle: src.BundleName, Name: src.ArtifactName}, nil
	}
	b, err := io.ReadAll(src.Reader)
	if err != nil {
		return nil, err
	}
	return parsedRecord{Bytes: b, Bundle: src.BundleName, BundleVer: src.BundleVersion, Name: src.ArtifactName, BundleRoot: src.BundleRoot}, nil
}

func (h *Handler) Validate(_ context.Context, _ kinds.Parsed) error { return nil }

// activationToken is the opaque handle returned from Activate.
type activationToken struct {
	Bundle   string
	Artifact string
}

func (h *Handler) Activate(_ context.Context, p kinds.Parsed, _ kinds.Environment) (kinds.Activation, error) {
	rec, ok := p.(parsedRecord)
	if !ok {
		return nil, nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Journal = append(h.Journal, ActivationEvent{Bundle: rec.Bundle, Artifact: rec.Name, Activated: true})
	return activationToken{Bundle: rec.Bundle, Artifact: rec.Name}, nil
}

func (h *Handler) Deactivate(_ context.Context, a kinds.Activation) error {
	tok, ok := a.(activationToken)
	if !ok {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Journal = append(h.Journal, ActivationEvent{Bundle: tok.Bundle, Artifact: tok.Artifact, Activated: false})
	return nil
}
