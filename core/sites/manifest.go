// Package sites provides manifest parsing, deterministic bundle packaging,
// and a recents index for the Fleet Sites feature.
//
// This package is OSS-boundary safe: it imports nothing from core/fleet.
// core/fleet/sites.go imports this package; never the reverse.
//
// Fork recipe: keep core/sites/ as-is; it works standalone for local
// packaging without any Fleet network calls. Remove core/fleet/sites.go
// to disable the upload path.
//
// Mission: sites-foundation-01NSITE04, WP02.
package sites

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// manifestFilename is the canonical file name at a site root.
const manifestFilename = "kameas-site.json"

// nameRE is the slug grammar from contract-harness-sites.md §0:
//   ^[a-z0-9]+(-[a-z0-9]+)*$
//
// This naturally forbids consecutive hyphens (which would produce "--")
// because a hyphen must be followed by at least one [a-z0-9] char.
var nameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

const maxNameLen = 40

// SiteKind identifies whether a site serves pre-built static assets or
// runs a dynamic server process.
type SiteKind string

const (
	KindStatic  SiteKind = "static"
	KindDynamic SiteKind = "dynamic"
)

// Framework is the harness-side preset hint for dynamic entrypoint wiring.
// The server validates the closed runtime enum; the harness fills in sane
// defaults for entrypoint, health path, etc. from the framework preset.
type Framework string

const (
	FrameworkStreamlit Framework = "streamlit"
	FrameworkDash      Framework = "dash"
	FrameworkCustom    Framework = "custom"
)

// StaticSection is the "static" object in kameas-site.json.
type StaticSection struct {
	// Dir is a relative path (inside the site root) to the directory that
	// contains the pre-built static files served by the gateway.
	Dir string `json:"dir"`
	// SPAFallback instructs the gateway to serve index.html on 404, enabling
	// client-side routing for single-page apps.
	SPAFallback bool `json:"spa_fallback,omitempty"`
}

// BuildConfig is the optional pre-deployment build step for dynamic sites.
type BuildConfig struct {
	// Command runs inside a clean build environment before the image is assembled.
	Command string `json:"command,omitempty"`
}

// EnvVarSpec describes a required or optional environment variable slot.
// Values are intentionally absent: they are injected at runtime via
// PUT /api/v1/sites/{id}/env and never ride in bundles.
//
// If a caller supplies a "value" field in the JSON, Validate rejects it so
// that secrets never get baked into the bundle accidentally.
type EnvVarSpec struct {
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	// value is intentionally unexported and not present in the JSON schema.
	// We detect value fields via rawEnvVarSpec during parsing to reject them.
}

// rawEnvVarSpec is used during unmarshalling to detect forbidden "value" fields.
type rawEnvVarSpec map[string]json.RawMessage

// DynamicSection is the "dynamic" object in kameas-site.json.
type DynamicSection struct {
	// Runtime is a string passed verbatim to the server. The server validates
	// the closed enum (e.g. "python3.12"); the harness accepts any non-empty
	// string and surfaces server 422s verbatim on unknown values.
	Runtime string `json:"runtime,omitempty"`
	// Framework selects a harness-side preset that fills in entrypoint,
	// health path, and other defaults. One of "streamlit", "dash", "custom".
	Framework Framework `json:"framework,omitempty"`
	// Entrypoint is the command the container runs. If empty and Framework is
	// "streamlit" or "dash", the preset fills it in. For "custom" this field
	// is required.
	Entrypoint string `json:"entrypoint,omitempty"`
	// Build is the optional pre-deploy build step.
	Build *BuildConfig `json:"build,omitempty"`
	// Env lists environment variable names and their metadata. Values are
	// absent by design; use PUT /sites/{id}/env to set them.
	Env map[string]EnvVarSpec `json:"env,omitempty"`
	// HealthPath is the HTTP path the gateway checks for liveness.
	// Defaults per framework: streamlit→"/_stcore/health", others→"/".
	HealthPath string `json:"health_path,omitempty"`
	// Size is the task size: "S" (default), "M", or "L".
	Size string `json:"size,omitempty"`
}

// Manifest is the parsed and validated representation of kameas-site.json.
type Manifest struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
	Type    SiteKind `json:"type"`

	Static  *StaticSection  `json:"static,omitempty"`
	Dynamic *DynamicSection `json:"dynamic,omitempty"`

	// Ignore is a list of glob patterns appended to the packager's default
	// ignore set. Relative to the site root.
	Ignore []string `json:"ignore,omitempty"`
}

// rawManifest mirrors Manifest but uses json.RawMessage for env entries so we
// can inspect them for forbidden "value" fields before constructing the final
// Manifest value.
type rawManifest struct {
	Version int             `json:"version"`
	Name    string          `json:"name"`
	Type    SiteKind        `json:"type"`
	Static  *StaticSection  `json:"static,omitempty"`
	Dynamic *rawDynamic     `json:"dynamic,omitempty"`
	Ignore  []string        `json:"ignore,omitempty"`
}

type rawDynamic struct {
	Runtime    string                        `json:"runtime,omitempty"`
	Framework  Framework                     `json:"framework,omitempty"`
	Entrypoint string                        `json:"entrypoint,omitempty"`
	Build      *BuildConfig                  `json:"build,omitempty"`
	Env        map[string]rawEnvVarSpec      `json:"env,omitempty"`
	HealthPath string                        `json:"health_path,omitempty"`
	Size       string                        `json:"size,omitempty"`
}

// Parse reads and parses kameas-site.json at root/kameas-site.json,
// then calls Validate. It returns the validated Manifest or a descriptive
// error. Rich errors are intentional — agent tooling reads them.
func Parse(root string) (Manifest, error) {
	path := filepath.Join(root, manifestFilename)
	data, err := os.ReadFile(path) // #nosec G304 -- root is caller-provided site dir
	if err != nil {
		return Manifest{}, fmt.Errorf("sites/manifest: read %s: %w", path, err)
	}
	return ParseBytes(data)
}

// ParseBytes parses and validates a raw kameas-site.json payload. Callers
// that already have the bytes (e.g. from an archive) use this directly.
func ParseBytes(data []byte) (Manifest, error) {
	var raw rawManifest
	if err := json.Unmarshal(data, &raw); err != nil {
		return Manifest{}, fmt.Errorf("sites/manifest: unmarshal: %w", err)
	}

	// Check for forbidden "value" fields in dynamic.env entries before we
	// construct the final Manifest — this is our only chance to catch them.
	if raw.Dynamic != nil {
		for varName, spec := range raw.Dynamic.Env {
			if _, hasValue := spec["value"]; hasValue {
				return Manifest{}, fmt.Errorf(
					"sites/manifest: dynamic.env[%q] contains a \"value\" field; "+
						"env values must never be stored in the manifest — "+
						"inject them via PUT /api/v1/sites/{id}/env instead",
					varName,
				)
			}
		}
	}

	// Convert rawManifest → Manifest.
	m := Manifest{
		Version: raw.Version,
		Name:    raw.Name,
		Type:    raw.Type,
		Static:  raw.Static,
		Ignore:  raw.Ignore,
	}
	if raw.Dynamic != nil {
		env := make(map[string]EnvVarSpec, len(raw.Dynamic.Env))
		for k, rv := range raw.Dynamic.Env {
			var spec EnvVarSpec
			if err := json.Unmarshal(marshalRawEnvVarSpec(rv), &spec); err != nil {
				return Manifest{}, fmt.Errorf("sites/manifest: unmarshal env[%q]: %w", k, err)
			}
			env[k] = spec
		}
		m.Dynamic = &DynamicSection{
			Runtime:    raw.Dynamic.Runtime,
			Framework:  raw.Dynamic.Framework,
			Entrypoint: raw.Dynamic.Entrypoint,
			Build:      raw.Dynamic.Build,
			Env:        env,
			HealthPath: raw.Dynamic.HealthPath,
			Size:       raw.Dynamic.Size,
		}
	}

	if err := Validate(m); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// marshalRawEnvVarSpec converts a rawEnvVarSpec (map[string]json.RawMessage)
// back into JSON for use with the EnvVarSpec decoder. Only "description" and
// "required" keys are forwarded; "value" (already rejected above) is excluded
// so the Go struct gets only what the schema allows.
func marshalRawEnvVarSpec(rv rawEnvVarSpec) []byte {
	allowed := map[string]json.RawMessage{}
	for k, v := range rv {
		if k == "description" || k == "required" {
			allowed[k] = v
		}
	}
	b, _ := json.Marshal(allowed)
	return b
}

// Validate checks all invariants described in the spec FR-001..FR-003.
// Errors are phrased for agent consumption: they name the field and
// explain the constraint in plain English.
func Validate(m Manifest) error {
	// FR-001: version must be 1.
	if m.Version != 1 {
		return fmt.Errorf("sites/manifest: version must be 1, got %d", m.Version)
	}

	// FR-001: name grammar ^[a-z0-9]+(-[a-z0-9]+)*$ and ≤40 chars.
	if m.Name == "" {
		return fmt.Errorf("sites/manifest: name is required")
	}
	if len(m.Name) > maxNameLen {
		return fmt.Errorf(
			"sites/manifest: name %q is %d chars, maximum is %d",
			m.Name, len(m.Name), maxNameLen,
		)
	}
	if !nameRE.MatchString(m.Name) {
		return fmt.Errorf(
			"sites/manifest: name %q does not match required pattern ^[a-z0-9]+(-[a-z0-9]+)*$; "+
				"use lowercase letters and digits only, with hyphens as internal separators (no leading/trailing/consecutive hyphens)",
			m.Name,
		)
	}

	// FR-001: type must be "static" or "dynamic".
	switch m.Type {
	case KindStatic, KindDynamic:
	default:
		return fmt.Errorf(
			"sites/manifest: type must be \"static\" or \"dynamic\", got %q",
			m.Type,
		)
	}

	// FR-001: exactly one of static/dynamic section must be present,
	// and it must match the declared type.
	hasStatic := m.Static != nil
	hasDynamic := m.Dynamic != nil

	if hasStatic && hasDynamic {
		return fmt.Errorf(
			"sites/manifest: both \"static\" and \"dynamic\" sections are present; " +
				"a site must be one type only — remove the section that does not match type=%q",
			m.Type,
		)
	}
	if !hasStatic && !hasDynamic {
		return fmt.Errorf(
			"sites/manifest: neither \"static\" nor \"dynamic\" section is present; " +
				"add a %q section matching type=%q",
			m.Type, m.Type,
		)
	}
	if m.Type == KindStatic && !hasStatic {
		return fmt.Errorf(
			"sites/manifest: type is \"static\" but no \"static\" section found; " +
				"add a \"static\" section with a \"dir\" field",
		)
	}
	if m.Type == KindDynamic && !hasDynamic {
		return fmt.Errorf(
			"sites/manifest: type is \"dynamic\" but no \"dynamic\" section found; " +
				"add a \"dynamic\" section with at least a \"runtime\" field",
		)
	}

	// FR-001: static.dir path containment — no absolute path, no ".." component.
	if hasStatic {
		if err := validateStaticDir(m.Static.Dir); err != nil {
			return err
		}
	}

	// FR-002: dynamic section defaults + custom-framework entrypoint required.
	if hasDynamic {
		if err := validateDynamic(m.Dynamic); err != nil {
			return err
		}
	}

	return nil
}

// validateStaticDir checks FR-001 path containment rules for static.dir.
func validateStaticDir(dir string) error {
	// Empty dir is allowed — means the site root itself is served.
	if dir == "" {
		return nil
	}
	// Reject any backslash outright (matches contract §2: paths must use
	// forward slashes; Windows-separator escapes such as "..\esc" are
	// not accepted on any platform).
	if strings.ContainsRune(dir, '\\') {
		return fmt.Errorf(
			"sites/manifest: static.dir %q contains a backslash; "+
				"use forward-slash separators only",
			dir,
		)
	}
	if filepath.IsAbs(dir) {
		return fmt.Errorf(
			"sites/manifest: static.dir must be a relative path, got absolute path %q",
			dir,
		)
	}
	// Detect ".." traversal by cleaning and checking the result.
	cleaned := filepath.Clean(dir)
	// After cleaning, an escape attempt starts with ".." or is "..".
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf(
			"sites/manifest: static.dir %q contains \"..\" and would escape the site root; "+
				"use a path relative to the site root without parent-directory components",
			dir,
		)
	}
	// Also catch literal ".." anywhere in path segments.
	for _, seg := range strings.Split(filepath.ToSlash(dir), "/") {
		if seg == ".." {
			return fmt.Errorf(
				"sites/manifest: static.dir %q contains \"..\" segment; "+
					"paths must stay within the site root directory",
				dir,
			)
		}
	}
	return nil
}

// validateDynamic applies FR-002 framework preset logic and FR-003 runtime
// validation (loose: any non-empty string; server validates the closed enum).
func validateDynamic(d *DynamicSection) error {
	// FR-002: for "custom" framework, entrypoint is required.
	if d.Framework == FrameworkCustom && d.Entrypoint == "" {
		return fmt.Errorf(
			"sites/manifest: framework is \"custom\" but entrypoint is empty; " +
				"provide the full command string, e.g. \"python app.py --port $PORT\"",
		)
	}

	// FR-003: runtime is loosely validated — any non-empty string is accepted.
	// The server validates the closed enum and returns 422 unsupported_runtime.
	// Harness surfaces that verbatim; no need to duplicate the enum here.
	// (An empty runtime is also OK in a manifest used for sites_init scaffolding.)

	// Apply size default.
	if d.Size == "" {
		d.Size = "S"
	}

	return nil
}

// ApplyFrameworkPresets fills in entrypoint and health_path defaults for the
// known framework presets (streamlit, dash). For "custom", callers must
// supply their own entrypoint. Returns a copy of the DynamicSection with
// preset fields filled in where they were absent; the original is not mutated.
//
// FR-002: streamlit → "streamlit run app.py --server.port $PORT --server.address 0.0.0.0" + "/_stcore/health"
//
//	dash   → "gunicorn app:server --bind 0.0.0.0:$PORT" + "/"
//	custom → entrypoint unchanged; health_path defaults to "/"
func ApplyFrameworkPresets(d DynamicSection) DynamicSection {
	out := d // shallow copy
	switch out.Framework {
	case FrameworkStreamlit:
		if out.Entrypoint == "" {
			out.Entrypoint = "streamlit run app.py --server.port $PORT --server.address 0.0.0.0"
		}
		if out.HealthPath == "" {
			out.HealthPath = "/_stcore/health"
		}
	case FrameworkDash:
		if out.Entrypoint == "" {
			out.Entrypoint = "gunicorn app:server --bind 0.0.0.0:$PORT"
		}
		if out.HealthPath == "" {
			out.HealthPath = "/"
		}
	default: // custom or empty
		if out.HealthPath == "" {
			out.HealthPath = "/"
		}
	}
	if out.Size == "" {
		out.Size = "S"
	}
	return out
}
