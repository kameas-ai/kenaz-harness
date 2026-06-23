package lockfile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	bundle "github.com/kameas-ai/kenaz-harness/core/bundle"
)

// Read parses a canonical kenaz.lock document. The reader accepts only the
// subset of TOML that Write emits — a deliberate choice (see package doc).
// Lockfiles authored by hand should be re-canonicalized via Write before
// commit.
//
// Supported TOML subset:
//   - top-level integer assignment: schema_version = N
//   - empty top-level table:        [universal]
//   - array-of-tables:              [[bundle]] / [[bundle.artifact]]
//   - inline-table arrays:          dependencies = [{name=..., version=..., content_hash=...}]
//   - quoted strings with no escapes other than \" and \\
//   - integer values, no floats
func Read(data []byte) (*Lockfile, error) {
	lf := &Lockfile{SchemaVersion: 0}
	lines := splitLines(data)

	var (
		section       string         // "" / "universal" / "bundle" / "bundle.artifact"
		curBundle     *LockedBundle
		curBundleIdx  = -1
	)

	for i := 0; i < len(lines); i++ {
		raw := lines[i]
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Section headers.
		if strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]") {
			hdr := strings.TrimSuffix(strings.TrimPrefix(line, "[["), "]]")
			switch hdr {
			case "bundle":
				lf.Bundles = append(lf.Bundles, LockedBundle{})
				curBundleIdx = len(lf.Bundles) - 1
				curBundle = &lf.Bundles[curBundleIdx]
				section = "bundle"
			case "bundle.artifact":
				if curBundle == nil {
					return nil, fmt.Errorf("%w: [[bundle.artifact]] before any [[bundle]] (line %d)",
						bundle.ErrLockfileInvalid, i+1)
				}
				curBundle.Artifacts = append(curBundle.Artifacts, LockedArtifact{})
				section = "bundle.artifact"
			default:
				return nil, fmt.Errorf("%w: unknown array-of-tables section %q (line %d)",
					bundle.ErrLockfileInvalid, hdr, i+1)
			}
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			hdr := strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			switch hdr {
			case "universal":
				section = "universal"
			default:
				return nil, fmt.Errorf("%w: unknown table section %q (line %d)",
					bundle.ErrLockfileInvalid, hdr, i+1)
			}
			continue
		}

		// key = value
		eq := strings.Index(line, "=")
		if eq < 0 {
			return nil, fmt.Errorf("%w: expected key=value (line %d): %q",
				bundle.ErrLockfileInvalid, i+1, raw)
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])

		// Multi-line inline-array values: the canonical writer emits
		// `dependencies = [\n  { ... },\n]`. Accumulate continuation
		// lines until we see the closing ']' on its own.
		if strings.HasPrefix(val, "[") && !strings.HasSuffix(val, "]") {
			parts := []string{val}
			i++
			for ; i < len(lines); i++ {
				cont := strings.TrimSpace(lines[i])
				parts = append(parts, cont)
				if cont == "]" || strings.HasSuffix(cont, "]") {
					break
				}
			}
			val = strings.Join(parts, " ")
		}

		switch section {
		case "":
			// Top-level keys.
			switch key {
			case "schema_version":
				n, err := strconv.Atoi(val)
				if err != nil {
					return nil, fmt.Errorf("%w: schema_version not int (line %d)",
						bundle.ErrLockfileInvalid, i+1)
				}
				lf.SchemaVersion = n
			default:
				return nil, fmt.Errorf("%w: unknown top-level key %q (line %d)",
					bundle.ErrLockfileInvalid, key, i+1)
			}
		case "universal":
			// Reserved; ignore unknown keys here.
			_ = key
			_ = val
		case "bundle":
			s, ok := unquoteValue(val)
			if !ok && key != "dependencies" {
				return nil, fmt.Errorf("%w: bundle.%s value not parseable (line %d)",
					bundle.ErrLockfileInvalid, key, i+1)
			}
			switch key {
			case "name":
				curBundle.Name = s
			case "version":
				curBundle.Version = s
			case "source":
				curBundle.Source = s
			case "content_hash":
				curBundle.ContentHash = s
			case "signature_ref":
				curBundle.SignatureRef = s
			case "dependencies":
				deps, err := parseInlineDeps(val)
				if err != nil {
					return nil, fmt.Errorf("%w: bundle.dependencies (line %d): %v",
						bundle.ErrLockfileInvalid, i+1, err)
				}
				curBundle.Dependencies = deps
			default:
				return nil, fmt.Errorf("%w: unknown bundle key %q (line %d)",
					bundle.ErrLockfileInvalid, key, i+1)
			}
		case "bundle.artifact":
			s, ok := unquoteValue(val)
			if !ok {
				return nil, fmt.Errorf("%w: bundle.artifact.%s value not parseable (line %d)",
					bundle.ErrLockfileInvalid, key, i+1)
			}
			art := &curBundle.Artifacts[len(curBundle.Artifacts)-1]
			switch key {
			case "name":
				art.Name = s
			case "kind":
				art.Kind = s
			case "content_hash":
				art.ContentHash = s
			default:
				return nil, fmt.Errorf("%w: unknown bundle.artifact key %q (line %d)",
					bundle.ErrLockfileInvalid, key, i+1)
			}
		}
	}

	if lf.SchemaVersion == 0 {
		return nil, fmt.Errorf("%w: schema_version is required", bundle.ErrLockfileInvalid)
	}
	if lf.SchemaVersion < MinSchemaVersion || lf.SchemaVersion > CurrentSchemaVersion {
		return nil, fmt.Errorf("%w: schema_version=%d (supported [%d,%d])",
			bundle.ErrSchemaUnsupported, lf.SchemaVersion, MinSchemaVersion, CurrentSchemaVersion)
	}
	return lf, nil
}

// Write returns the canonical byte representation of lf. The encoding rules
// — fixed key order, byte-wise sort by `(name, version, source)` with
// content_hash tie-break, mandatory trailing newline, no CRLF — make output
// stable across runs and OS targets (NFR-002).
func Write(lf *Lockfile) ([]byte, error) {
	if lf == nil {
		return nil, fmt.Errorf("%w: nil lockfile", bundle.ErrLockfileInvalid)
	}
	if lf.SchemaVersion == 0 {
		lf.SchemaVersion = CurrentSchemaVersion
	}
	if lf.SchemaVersion < MinSchemaVersion || lf.SchemaVersion > CurrentSchemaVersion {
		return nil, fmt.Errorf("%w: schema_version=%d", bundle.ErrSchemaUnsupported, lf.SchemaVersion)
	}

	bundles := append([]LockedBundle(nil), lf.Bundles...)
	sortBundles(bundles)

	var b strings.Builder
	fmt.Fprintf(&b, "schema_version = %d\n", lf.SchemaVersion)

	for i := range bundles {
		writeBundle(&b, &bundles[i])
	}

	// Reserved [universal] table at the end (always emitted so the file
	// shape is stable; v1 leaves it empty).
	b.WriteString("\n[universal]\n")

	out := b.String()
	// Mandatory trailing newline (string ends with newline because the
	// last write was "\n[universal]\n"). Keep the contract explicit.
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return []byte(out), nil
}

// ContentHash returns sha256:<hex> over the canonical bytes.
func (lf *Lockfile) ContentHash() (string, error) {
	b, err := Write(lf)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:]), nil
}

func writeBundle(b *strings.Builder, lb *LockedBundle) {
	b.WriteString("\n[[bundle]]\n")
	fmt.Fprintf(b, "name = %s\n", quote(lb.Name))
	fmt.Fprintf(b, "version = %s\n", quote(lb.Version))
	fmt.Fprintf(b, "source = %s\n", quote(lb.Source))
	fmt.Fprintf(b, "content_hash = %s\n", quote(lb.ContentHash))
	if lb.SignatureRef != "" {
		fmt.Fprintf(b, "signature_ref = %s\n", quote(lb.SignatureRef))
	}
	if len(lb.Dependencies) > 0 {
		deps := append([]LockedDep(nil), lb.Dependencies...)
		sort.SliceStable(deps, func(i, j int) bool {
			if deps[i].Name != deps[j].Name {
				return deps[i].Name < deps[j].Name
			}
			if deps[i].Version != deps[j].Version {
				return deps[i].Version < deps[j].Version
			}
			return deps[i].ContentHash < deps[j].ContentHash
		})
		b.WriteString("dependencies = [\n")
		for _, d := range deps {
			fmt.Fprintf(b, "  { name = %s, version = %s, content_hash = %s },\n",
				quote(d.Name), quote(d.Version), quote(d.ContentHash))
		}
		b.WriteString("]\n")
	}
	if len(lb.Artifacts) > 0 {
		arts := append([]LockedArtifact(nil), lb.Artifacts...)
		sort.SliceStable(arts, func(i, j int) bool {
			if arts[i].Name != arts[j].Name {
				return arts[i].Name < arts[j].Name
			}
			return arts[i].Kind < arts[j].Kind
		})
		for _, a := range arts {
			b.WriteString("\n[[bundle.artifact]]\n")
			fmt.Fprintf(b, "name = %s\n", quote(a.Name))
			fmt.Fprintf(b, "kind = %s\n", quote(a.Kind))
			fmt.Fprintf(b, "content_hash = %s\n", quote(a.ContentHash))
		}
	}
}

// sortBundles sorts in-place by (name, version, source, content_hash).
// Comparison is byte-wise (Go string compare); no locale.
func sortBundles(bs []LockedBundle) {
	sort.SliceStable(bs, func(i, j int) bool {
		if bs[i].Name != bs[j].Name {
			return bs[i].Name < bs[j].Name
		}
		if bs[i].Version != bs[j].Version {
			return bs[i].Version < bs[j].Version
		}
		if bs[i].Source != bs[j].Source {
			return bs[i].Source < bs[j].Source
		}
		return bs[i].ContentHash < bs[j].ContentHash
	})
}

// quote emits a TOML basic string. We escape only `\` and `"`; control
// characters (which the lockfile never contains in practice) are left
// alone. Inputs are always ASCII per the schema regex.
func quote(s string) string {
	if strings.ContainsAny(s, `\"`) {
		s = strings.ReplaceAll(s, `\`, `\\`)
		s = strings.ReplaceAll(s, `"`, `\"`)
	}
	return `"` + s + `"`
}

func unquoteValue(s string) (string, bool) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", false
	}
	body := s[1 : len(s)-1]
	body = strings.ReplaceAll(body, `\"`, `"`)
	body = strings.ReplaceAll(body, `\\`, `\`)
	return body, true
}

// parseInlineDeps decodes `[ { name=.., version=.., content_hash=.. }, ... ]`.
func parseInlineDeps(s string) ([]LockedDep, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") {
		return nil, fmt.Errorf("expected '[' got %q", s)
	}
	// strip outer brackets, then split on '},' boundaries.
	inner := strings.TrimSpace(s[1:])
	if strings.HasSuffix(inner, "]") {
		inner = strings.TrimSpace(inner[:len(inner)-1])
	}
	// Could be empty list.
	if inner == "" {
		return nil, nil
	}
	// Multi-line writers may put each entry on its own line ending with
	// "},". Normalize first.
	inner = strings.ReplaceAll(inner, "\n", " ")

	var out []LockedDep
	for {
		inner = strings.TrimSpace(inner)
		if inner == "" {
			break
		}
		if !strings.HasPrefix(inner, "{") {
			return nil, fmt.Errorf("expected '{' got %q", inner)
		}
		end := strings.Index(inner, "}")
		if end < 0 {
			return nil, fmt.Errorf("missing '}'")
		}
		body := inner[1:end]
		dep, err := parseInlineKVs(body)
		if err != nil {
			return nil, err
		}
		out = append(out, dep)
		inner = strings.TrimSpace(inner[end+1:])
		inner = strings.TrimPrefix(inner, ",")
	}
	return out, nil
}

func parseInlineKVs(body string) (LockedDep, error) {
	d := LockedDep{}
	parts := strings.Split(body, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		eq := strings.Index(p, "=")
		if eq < 0 {
			return d, fmt.Errorf("expected k=v in %q", p)
		}
		k := strings.TrimSpace(p[:eq])
		v := strings.TrimSpace(p[eq+1:])
		s, ok := unquoteValue(v)
		if !ok {
			return d, fmt.Errorf("dep value %q not quoted", v)
		}
		switch k {
		case "name":
			d.Name = s
		case "version":
			d.Version = s
		case "content_hash":
			d.ContentHash = s
		default:
			return d, fmt.Errorf("unknown dep key %q", k)
		}
	}
	return d, nil
}

// splitLines normalizes CRLF→LF before splitting so cross-OS reads work.
func splitLines(data []byte) []string {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	return strings.Split(s, "\n")
}
