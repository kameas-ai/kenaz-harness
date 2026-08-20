// Package manifest implements the kenaz.yaml bundle manifest format.
//
// The manifest is YAML 1.2-shaped (per research D1) with a JSON Schema that
// is embedded at build time and is the source of truth for the manifest
// contract. Validation is implemented in Go directly rather than through a
// dynamic JSON-Schema engine to keep the v1 dependency surface small; the
// embedded schema document remains the canonical specification and is
// surfaced to operators via `harness bundle schema`.
//
// YAML library decision: this package uses gopkg.in/yaml.v3 because it is
// already a transitive dependency of the harness's existing modules and
// because it is the most widely-used parser in the Go ecosystem. The
// research-recommended goccy/go-yaml is preferable for strict YAML 1.2
// semantics, but the Norway-problem mitigation in this package is enforced
// at the *schema* layer (regex-pattern restrictions on short identifiers
// and `schema_version` parsed as integer first), not via parser quirks.
// See R2 / R6 in plan.md.
//
// The package has no compile-time dependency on any other core/bundle/
// subpackage other than the parent core/bundle errors package.
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
)

// Manifest is the parsed kenaz.yaml descriptor.
type Manifest struct {
	SchemaVersion int                  `yaml:"schema_version" json:"schema_version"`
	Name          string               `yaml:"name" json:"name"`
	Version       string               `yaml:"version" json:"version"`
	License       string               `yaml:"license" json:"license"`
	Metadata      map[string]string    `yaml:"metadata,omitempty" json:"metadata,omitempty"`
	Dependencies  []BundleReference    `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
	Artifacts     []ArtifactDescriptor `yaml:"artifacts" json:"artifacts"`
	Signatures    []SignatureRef       `yaml:"signatures,omitempty" json:"signatures,omitempty"`
}

// BundleReference is one entry in a manifest's dependencies list.
type BundleReference struct {
	Name    string       `yaml:"name" json:"name"`
	Version string       `yaml:"version" json:"version"`
	Source  *ChannelSpec `yaml:"source,omitempty" json:"source,omitempty"`
}

// ChannelSpec is the YAML form of a distribution-channel reference.
type ChannelSpec struct {
	Kind string       `yaml:"kind" json:"kind"`
	Ref  string       `yaml:"ref,omitempty" json:"ref,omitempty"`
	URL  string       `yaml:"url,omitempty" json:"url,omitempty"`
	Path string       `yaml:"path,omitempty" json:"path,omitempty"`
	Auth *ChannelAuth `yaml:"auth,omitempty" json:"auth,omitempty"`
}

// ChannelAuth carries an indirect credential reference (resolved by
// secrets-keychain at fetch time, never inlined).
type ChannelAuth struct {
	Keychain string `yaml:"keychain,omitempty" json:"keychain,omitempty"`
}

// ArtifactDescriptor is one named artifact within a bundle.
type ArtifactDescriptor struct {
	Name         string         `yaml:"name" json:"name"`
	Kind         string         `yaml:"kind" json:"kind"`
	Path         string         `yaml:"path" json:"path"`
	ContentHash  string         `yaml:"content_hash" json:"content_hash"`
	KindMetadata map[string]any `yaml:"kind_metadata,omitempty" json:"kind_metadata,omitempty"`
}

// SignatureRef is a pointer to a signature attached to a bundle or artifact.
type SignatureRef struct {
	Kind      string `yaml:"kind" json:"kind"`
	Locator   string `yaml:"locator" json:"locator"`
	Algorithm string `yaml:"algorithm,omitempty" json:"algorithm,omitempty"`
	KeyID     string `yaml:"key_id,omitempty" json:"key_id,omitempty"`
}

// CurrentSchemaVersion is the highest manifest schema version this harness
// supports. Newer manifests are rejected with ErrSchemaUnsupported.
const CurrentSchemaVersion = 1

// MinSchemaVersion is the lowest manifest schema version this harness can
// still read (forward-compatibility floor).
const MinSchemaVersion = 1

// ContentHash returns the SHA-256 of the canonical serialization of the
// manifest. The canonical form is a stable, sort-key-ordered ASCII-only
// representation; it is independent of the YAML or JSON marshaller used to
// read the manifest, so two semantically-equal manifests produce the same
// hash regardless of source whitespace, key order, or quoting style.
func (m *Manifest) ContentHash() string {
	h := sha256.New()
	writeCanonical(h, m)
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// CanonicalBytes returns the canonical, sorted, ASCII-only serialization
// used by ContentHash. Exposed for golden-file tests and for signature
// payloads (when the signed-payload definition is "the manifest's canonical
// bytes").
func (m *Manifest) CanonicalBytes() []byte {
	buf := &byteBuffer{}
	writeCanonical(buf, m)
	return buf.bytes()
}

// SigningPayload returns the canonical bytes that ARE the signed
// content: CanonicalBytes() computed as if m.Signatures were empty.
//
// Both the signer (test fixtures per plan §4 non-goal on
// TrustEngine.Sign) and the verifier (core/bundle/integrity/signature.go)
// MUST call this — never CanonicalBytes() — for any signature-related
// use. CanonicalBytes() serializes every entry of m.Signatures
// (sig[i].kind/locator/algorithm/key_id, see writeCanonical below),
// which includes the very locator that names where the signature bytes
// themselves will be written. Signing over the full canonical bytes
// would make the payload depend on a locator that cannot be known until
// after signing — a self-reference that cannot converge. Excluding
// Signatures from the signed payload removes that coupling entirely.
func (m *Manifest) SigningPayload() []byte {
	clone := *m
	clone.Signatures = nil
	return clone.CanonicalBytes()
}

// byteBuffer is a tiny io.Writer-like target. Avoids pulling bytes.Buffer
// just to keep the dependency footprint of this file at a glance.
type byteBuffer struct {
	b []byte
}

func (b *byteBuffer) Write(p []byte) (int, error) {
	b.b = append(b.b, p...)
	return len(p), nil
}
func (b *byteBuffer) bytes() []byte { return b.b }

type writer interface {
	Write(p []byte) (int, error)
}

func writeCanonical(w writer, m *Manifest) {
	wf(w, "schema_version", strconv.Itoa(m.SchemaVersion))
	wf(w, "name", m.Name)
	wf(w, "version", m.Version)
	wf(w, "license", m.License)

	metaKeys := make([]string, 0, len(m.Metadata))
	for k := range m.Metadata {
		metaKeys = append(metaKeys, k)
	}
	sort.Strings(metaKeys)
	for _, k := range metaKeys {
		wf(w, "metadata."+k, m.Metadata[k])
	}

	deps := append([]BundleReference(nil), m.Dependencies...)
	sort.SliceStable(deps, func(i, j int) bool {
		if deps[i].Name != deps[j].Name {
			return deps[i].Name < deps[j].Name
		}
		return deps[i].Version < deps[j].Version
	})
	for i, d := range deps {
		prefix := "dep[" + strconv.Itoa(i) + "]."
		wf(w, prefix+"name", d.Name)
		wf(w, prefix+"version", d.Version)
		if d.Source != nil {
			wf(w, prefix+"source.kind", d.Source.Kind)
			wf(w, prefix+"source.ref", d.Source.Ref)
			wf(w, prefix+"source.url", d.Source.URL)
			wf(w, prefix+"source.path", d.Source.Path)
			if d.Source.Auth != nil {
				wf(w, prefix+"source.auth.keychain", d.Source.Auth.Keychain)
			}
		}
	}

	arts := append([]ArtifactDescriptor(nil), m.Artifacts...)
	sort.SliceStable(arts, func(i, j int) bool {
		if arts[i].Kind != arts[j].Kind {
			return arts[i].Kind < arts[j].Kind
		}
		return arts[i].Name < arts[j].Name
	})
	for i, a := range arts {
		prefix := "art[" + strconv.Itoa(i) + "]."
		wf(w, prefix+"name", a.Name)
		wf(w, prefix+"kind", a.Kind)
		wf(w, prefix+"path", a.Path)
		wf(w, prefix+"content_hash", a.ContentHash)
		if len(a.KindMetadata) > 0 {
			mks := make([]string, 0, len(a.KindMetadata))
			for k := range a.KindMetadata {
				mks = append(mks, k)
			}
			sort.Strings(mks)
			for _, k := range mks {
				wf(w, prefix+"meta."+k, scalar(a.KindMetadata[k]))
			}
		}
	}

	sigs := append([]SignatureRef(nil), m.Signatures...)
	sort.SliceStable(sigs, func(i, j int) bool {
		if sigs[i].Kind != sigs[j].Kind {
			return sigs[i].Kind < sigs[j].Kind
		}
		return sigs[i].Locator < sigs[j].Locator
	})
	for i, s := range sigs {
		prefix := "sig[" + strconv.Itoa(i) + "]."
		wf(w, prefix+"kind", s.Kind)
		wf(w, prefix+"locator", s.Locator)
		wf(w, prefix+"algorithm", s.Algorithm)
		wf(w, prefix+"key_id", s.KeyID)
	}
}

func wf(w writer, key, val string) {
	_, _ = w.Write([]byte(key))
	_, _ = w.Write([]byte("="))
	_, _ = w.Write([]byte(val))
	_, _ = w.Write([]byte("\n"))
}

func scalar(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", v)
	}
}
