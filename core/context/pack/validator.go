package pack

import (
	"fmt"
	"regexp"
)

// DefaultLayerSizeBudget is NFR-002's default 256 KB ceiling per layer.
const DefaultLayerSizeBudget int64 = 256 * 1024

// ValidatorOptions tunes validator behaviour. The zero value applies
// charter defaults: 256 KB layer ceiling, signature reference required,
// path-traversal already enforced by the parser.
type ValidatorOptions struct {
	// LayerSizeBudget is the ceiling enforced against the pack's total
	// entry size. Zero means [DefaultLayerSizeBudget]. Use a negative
	// value to disable.
	LayerSizeBudget int64

	// MaxEntrySize is the optional per-entry ceiling. Zero disables.
	MaxEntrySize int64

	// SignatureRequired toggles the FR-014 / FR-003 invariant that every
	// pack carries a signature reference. Defaults to true.
	SignatureRequired *bool

	// HardSizeFail makes layer-size overflow fail validation rather than
	// emit a warning. Off by default — trimming is policy, not parser
	// (WP04 owns the trim policy).
	HardSizeFail bool
}

func (o ValidatorOptions) sigRequired() bool {
	if o.SignatureRequired == nil {
		return true
	}
	return *o.SignatureRequired
}

func (o ValidatorOptions) layerBudget() int64 {
	if o.LayerSizeBudget == 0 {
		return DefaultLayerSizeBudget
	}
	if o.LayerSizeBudget < 0 {
		return 0
	}
	return o.LayerSizeBudget
}

// nameRule constrains both pack and entry names. Stable, kebab-or-dot
// identifiers — friendly to filesystems, lockfiles, and trust anchors.
var nameRule = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

// versionRule is permissive — semver is ideal but not strictly required;
// the bundle resolver enforces semver on the lockfile side.
var versionRule = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,64}$`)

// Validate enforces FR-013 / FR-014 schema rules on a parsed pack. It
// mutates p only to attach structured warnings (e.g. soft size-budget
// overflow). Returns the first hard-fail [Error] encountered, or nil.
func Validate(p *ContextPack, opts ValidatorOptions) error {
	if p == nil {
		return newErr(CodeManifestMalformed, "<nil>", "nil pack", nil)
	}

	if p.Ref.Name == "" {
		return newErr(CodeRequiredFieldMissing, "name", "pack manifest is missing required field", nil)
	}
	if !nameRule.MatchString(p.Ref.Name) {
		return newErr(CodeInvalidName, p.Ref.Name, "pack name does not match [a-z0-9][a-z0-9._-]{0,127}", nil)
	}
	if p.Ref.Version == "" {
		return newErr(CodeRequiredFieldMissing, "version", "pack manifest is missing required field", nil)
	}
	if !versionRule.MatchString(p.Ref.Version) {
		return newErr(CodeInvalidVersion, p.Ref.Version, "pack version contains illegal characters", nil)
	}
	if !p.Ref.Layer.Valid() {
		return newErr(CodeInvalidLayer, string(p.Ref.Layer),
			fmt.Sprintf("layer must be one of %s|%s|%s", LayerOrg, LayerTeam, LayerPersonal), nil)
	}
	if opts.sigRequired() && p.Signature.Empty() {
		return newErr(CodeSignatureRefMissing, p.Ref.Name,
			"pack manifest must carry a signature reference (FR-003 / FR-014)", nil)
	}

	seen := make(map[string]string, len(p.Entries))
	var totalSize int64
	for _, e := range p.Entries {
		if e.Name == "" {
			return newErr(CodeRequiredFieldMissing, "entry.name",
				"context entry has no name", nil)
		}
		if !nameRule.MatchString(e.Name) {
			return newErr(CodeInvalidName, e.Name,
				"entry name does not match [a-z0-9][a-z0-9._-]{0,127}", nil)
		}
		if !e.Kind.Valid() {
			return newErr(CodeInvalidEntryKind, e.Name,
				fmt.Sprintf("entry kind %q is not one of glossary|explanation|skill|guidance", e.Kind),
				nil)
		}
		if prev, dup := seen[e.Name]; dup {
			return newErr(CodeDuplicateEntryName, e.Name,
				fmt.Sprintf("duplicate entry name within pack (also at %s)", prev), nil)
		}
		seen[e.Name] = e.RelPath
		if opts.MaxEntrySize > 0 && e.SizeBytes > opts.MaxEntrySize {
			return newErr(CodeOversizeEntry, e.Name,
				fmt.Sprintf("entry %d bytes exceeds per-entry ceiling %d bytes", e.SizeBytes, opts.MaxEntrySize),
				nil)
		}
		totalSize += e.SizeBytes
	}

	budget := opts.layerBudget()
	if budget > 0 && totalSize > budget {
		if opts.HardSizeFail {
			return newErr(CodeOversizeLayer, p.Ref.Name,
				fmt.Sprintf("pack %d bytes exceeds layer budget %d bytes (hard-fail)", totalSize, budget),
				nil)
		}
		p.Warnings = append(p.Warnings, Warning{
			Code:    string(CodeOversizeLayer),
			Subject: p.Ref.Name,
			Message: fmt.Sprintf("pack %d bytes exceeds layer budget %d bytes; trimming is policy", totalSize, budget),
		})
	}
	return nil
}
