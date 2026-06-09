package manifest

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	bundle "github.com/kameas-ai/kenaz-harness/core/bundle"
)

// ValidateOpts controls optional validation policy.
type ValidateOpts struct {
	// AllowEmptyArtifacts skips the "manifests must declare at least one
	// artifact" rule. Useful for scaffolding tools and tests; default false.
	AllowEmptyArtifacts bool
}

// pre-compiled patterns mirror the JSON Schema regexes.
var (
	nameRe          = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	artifactNameRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	kindRe          = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	contentHashRe   = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	semverLooseRe   = regexp.MustCompile(`^v?\d+\.\d+\.\d+(?:-[0-9A-Za-z.\-]+)?(?:\+[0-9A-Za-z.\-]+)?$`)
	channelKindRe   = regexp.MustCompile(`^(git|oci|http_mirror|local_path)$`)
)

// Validate enforces the manifest's structural and semantic invariants:
//   - schema_version is in the supported range
//   - name, version, license are non-empty and well-formed
//   - artifact (kind,name) tuples are unique within the bundle
//   - artifact.path does not escape the bundle root
//   - content_hash is a valid sha256 digest
//   - signature kinds are recognized
//
// Errors returned wrap one of the typed bundle.* sentinels so callers can
// match on err == bundle.ErrPathTraversal etc.
func (m *Manifest) Validate(opts ValidateOpts) error {
	if m.SchemaVersion < MinSchemaVersion || m.SchemaVersion > CurrentSchemaVersion {
		return fmt.Errorf("%w: schema_version=%d", bundle.ErrSchemaUnsupported, m.SchemaVersion)
	}
	if m.Name == "" || !nameRe.MatchString(m.Name) {
		return fmt.Errorf("%w: name %q does not match %s", bundle.ErrManifestInvalid, m.Name, nameRe)
	}
	if m.Version == "" || !semverLooseRe.MatchString(m.Version) {
		return fmt.Errorf("%w: version %q is not semver", bundle.ErrManifestInvalid, m.Version)
	}
	if strings.TrimSpace(m.License) == "" {
		return fmt.Errorf("%w: license is required (SPDX id)", bundle.ErrManifestInvalid)
	}

	if !opts.AllowEmptyArtifacts && len(m.Artifacts) == 0 {
		return fmt.Errorf("%w: artifacts must contain at least one entry", bundle.ErrManifestInvalid)
	}

	// Dependencies: name + version mandatory; channel kind validated if set.
	for i, d := range m.Dependencies {
		if d.Name == "" {
			return fmt.Errorf("%w: dependencies[%d].name is required", bundle.ErrManifestInvalid, i)
		}
		if d.Version == "" {
			return fmt.Errorf("%w: dependencies[%d].version is required", bundle.ErrManifestInvalid, i)
		}
		if d.Source != nil && !channelKindRe.MatchString(d.Source.Kind) {
			return fmt.Errorf("%w: dependencies[%d].source.kind=%q unrecognized",
				bundle.ErrManifestInvalid, i, d.Source.Kind)
		}
	}

	// Artifacts: shape + uniqueness + path-traversal.
	seen := make(map[string]struct{}, len(m.Artifacts))
	for i, a := range m.Artifacts {
		if !artifactNameRe.MatchString(a.Name) {
			return fmt.Errorf("%w: artifacts[%d].name %q is invalid",
				bundle.ErrManifestInvalid, i, a.Name)
		}
		if !kindRe.MatchString(a.Kind) {
			return fmt.Errorf("%w: artifacts[%d].kind %q is invalid",
				bundle.ErrManifestInvalid, i, a.Kind)
		}
		if !contentHashRe.MatchString(a.ContentHash) {
			return fmt.Errorf("%w: artifacts[%d].content_hash %q is not sha256",
				bundle.ErrManifestInvalid, i, a.ContentHash)
		}
		key := a.Kind + "\x00" + a.Name
		if _, dup := seen[key]; dup {
			return fmt.Errorf("%w: (kind=%s,name=%s)", bundle.ErrDuplicateArtifact, a.Kind, a.Name)
		}
		seen[key] = struct{}{}

		if err := validateArtifactPath(a.Path); err != nil {
			return fmt.Errorf("%w: artifacts[%d]: %v", bundle.ErrPathTraversal, i, err)
		}
	}

	// Signatures: recognized kinds only.
	for i, s := range m.Signatures {
		switch s.Kind {
		case "sigstore_referrer", "ed25519_detached":
			// ok
		default:
			return fmt.Errorf("%w: signatures[%d].kind=%q unrecognized",
				bundle.ErrManifestInvalid, i, s.Kind)
		}
		if strings.TrimSpace(s.Locator) == "" {
			return fmt.Errorf("%w: signatures[%d].locator empty",
				bundle.ErrManifestInvalid, i)
		}
	}

	return nil
}

// validateArtifactPath confirms `path` is a clean, relative, in-tree path.
// Returns nil if the path is safe, an error otherwise.
//
// Rules (mirrors NFR-004 path-traversal containment):
//   - path must not be empty
//   - path must not be absolute
//   - path must not contain `..` segments (after Clean)
//   - path must not begin with `/`, `./`, or a Windows drive letter
//
// We use filepath.ToSlash to normalize Windows separators before checks so
// the same rule fires regardless of the host OS the manifest was authored
// on.
func validateArtifactPath(p string) error {
	if p == "" {
		return fmt.Errorf("path is empty")
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("path %q is absolute", p)
	}
	// Reject Windows drive-letter prefix (filepath.IsAbs misses this on
	// non-Windows hosts).
	if len(p) >= 2 && p[1] == ':' {
		return fmt.Errorf("path %q has drive-letter prefix", p)
	}

	cleaned := filepath.ToSlash(filepath.Clean(p))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") || strings.HasSuffix(cleaned, "/..") {
		return fmt.Errorf("path %q contains parent traversal", p)
	}
	if strings.HasPrefix(cleaned, "/") {
		return fmt.Errorf("path %q is rooted", p)
	}
	return nil
}
