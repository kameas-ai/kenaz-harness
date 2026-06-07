package manifest

import (
	"bytes"
	"fmt"

	bundle "github.com/kameas-ai/kenaz-harness/core/bundle"
	"gopkg.in/yaml.v3"
)

// Parse decodes a kaneaz.yaml manifest. It enforces a two-phase parse:
//
//  1. The schema_version field is decoded first as an integer; if it is
//     greater than the current harness's CurrentSchemaVersion or less than
//     MinSchemaVersion, ErrSchemaUnsupported is returned before any deeper
//     parse. This is the defense against the YAML 1.1/1.2 Norway problem
//     biting forward-compatible manifests — older harnesses bail with a
//     structured error rather than mis-interpreting a newer schema.
//
//  2. The full document is decoded into a Manifest struct. If decoding
//     fails, the error wraps ErrManifestInvalid.
//
// Parse does NOT call Validate. Callers (typically the resolver) must call
// Validate after Parse to enforce the post-decode invariants such as
// uniqueness of (kind,name) and path-traversal containment.
func Parse(data []byte) (*Manifest, error) {
	// Phase 1: schema_version probe.
	var probe struct {
		SchemaVersion *int `yaml:"schema_version"`
	}
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("%w: yaml decode (probe): %v", bundle.ErrManifestInvalid, err)
	}
	if probe.SchemaVersion == nil {
		return nil, fmt.Errorf("%w: missing required field schema_version", bundle.ErrManifestInvalid)
	}
	sv := *probe.SchemaVersion
	if sv < MinSchemaVersion || sv > CurrentSchemaVersion {
		return nil, fmt.Errorf("%w: schema_version=%d (supported range [%d,%d])",
			bundle.ErrSchemaUnsupported, sv, MinSchemaVersion, CurrentSchemaVersion)
	}

	// Phase 2: full decode with strict (KnownFields) mode so that unknown
	// top-level fields trigger ErrManifestInvalid rather than being
	// silently dropped.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	m := &Manifest{}
	if err := dec.Decode(m); err != nil {
		return nil, fmt.Errorf("%w: yaml decode: %v", bundle.ErrManifestInvalid, err)
	}

	return m, nil
}
