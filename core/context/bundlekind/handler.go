package bundlekind

import (
	"context"
	"errors"
	"fmt"

	pack "github.com/sigil-tech/kaneaz-harness/core/context/pack"
)

// KindID is the spec-declared artifact-kind identifier registered against
// the bundle resolver (plan §5.2).
const KindID = "context-pack"

// LockfileEntry is the projection of a context-pack lockfile entry.
// Mirrors plan §5.2's TOML shape.
type LockfileEntry struct {
	Kind        string `toml:"kind" json:"kind"`
	Name        string `toml:"name" json:"name"`
	Version     string `toml:"version" json:"version"`
	Layer       string `toml:"layer" json:"layer"`
	ContentHash string `toml:"content_hash" json:"content_hash"`
	Source      string `toml:"source" json:"source"`
	Signature   string `toml:"signature,omitempty" json:"signature,omitempty"`
	Required    bool   `toml:"required" json:"required"`
}

// FetchedArtifact is the bundle resolver's view of a successfully-fetched
// context-pack: it carries the on-disk root the handler should parse.
// In production this is what the bundle resolver materialises from its
// content-addressable cache before invoking the handler.
type FetchedArtifact struct {
	Root        string            // absolute path of the unpacked pack root
	ContentHash string            // sha256:hex aggregate from the bundle resolver
	Source      string            // origin URL/ref recorded in the lockfile
	Channel     string            // git | oci | http_mirror | localpath
	Annotations map[string]string // bundle-resolver-attached metadata
}

// Handler is the artifact-kind contract the bundle resolver expects.
// The bundle-format-resolver mission owns the canonical interface; this
// package's [DefaultHandler] satisfies it by parsing through
// [pack.ParseAndValidate].
type Handler interface {
	Kind() string
	Parse(ctx context.Context, art FetchedArtifact) (*pack.ContextPack, error)
	Validate(ctx context.Context, p *pack.ContextPack) error
	Lockfile(p *pack.ContextPack, src, signature string) LockfileEntry
}

// Registry is the bundle resolver's artifact-kind registry surface this
// package consumes. The shared-context-distribution mission itself never
// implements a registry — it only registers against one.
type Registry interface {
	Register(h Handler) error
}

// DefaultHandler implements [Handler] using the WP01 parser and validator.
type DefaultHandler struct {
	// Validator captures the validation policy the bundle resolver should
	// apply to every context-pack on parse (NFR-002 size budget,
	// signature requirement).
	Validator pack.ValidatorOptions
}

// Kind returns the registered kind identifier.
func (h *DefaultHandler) Kind() string { return KindID }

// Parse delegates to [pack.ParseDir]; validation runs separately so
// validation policy stays tunable by the bundle resolver per call.
func (h *DefaultHandler) Parse(_ context.Context, art FetchedArtifact) (*pack.ContextPack, error) {
	if art.Root == "" {
		return nil, errors.New("bundlekind: artifact root is empty")
	}
	p, err := pack.ParseDir(art.Root)
	if err != nil {
		return nil, fmt.Errorf("bundlekind: parse %s: %w", art.Root, err)
	}
	// The bundle resolver provides the authoritative content hash from
	// its content-addressable cache; surface a mismatch as a hard error
	// so a tampered or wrong-version artifact never proceeds.
	if art.ContentHash != "" && p.Ref.ContentHash != art.ContentHash {
		return p, fmt.Errorf("bundlekind: content-hash mismatch for %s: parser=%s bundle=%s",
			p.Ref.Name, p.Ref.ContentHash, art.ContentHash)
	}
	return p, nil
}

// Validate runs the configured validator. Errors are typed [pack.Error]s.
func (h *DefaultHandler) Validate(_ context.Context, p *pack.ContextPack) error {
	if p == nil {
		return errors.New("bundlekind: nil pack")
	}
	return pack.Validate(p, h.Validator)
}

// Lockfile builds the lockfile projection of a parsed pack. Source and
// signature reference the bundle-resolver-side identifiers (e.g.
// `oci://...` URI; `sigstore-bundle:...` envelope id) so this handler
// never has to know how channels encode them.
func (h *DefaultHandler) Lockfile(p *pack.ContextPack, src, signature string) LockfileEntry {
	if p == nil {
		return LockfileEntry{Kind: KindID}
	}
	return LockfileEntry{
		Kind:        KindID,
		Name:        p.Ref.Name,
		Version:     p.Ref.Version,
		Layer:       string(p.Ref.Layer),
		ContentHash: p.Ref.ContentHash,
		Source:      src,
		Signature:   signature,
		Required:    p.Required,
	}
}

// Register attaches the default handler to a bundle artifact-kind
// registry. This is the single entry point the bundle resolver invokes
// during boot per plan §6.
func Register(reg Registry, opts pack.ValidatorOptions) error {
	if reg == nil {
		return errors.New("bundlekind: nil registry")
	}
	return reg.Register(&DefaultHandler{Validator: opts})
}
