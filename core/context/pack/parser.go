package pack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// manifest is the on-disk projection of pack.yaml. The exported
// [ContextPack] is built from it after validation.
type manifest struct {
	Name        string         `yaml:"name"`
	Version     string         `yaml:"version"`
	Layer       string         `yaml:"layer"`
	Issuer      string         `yaml:"issuer,omitempty"`
	License     string         `yaml:"license,omitempty"`
	Description string         `yaml:"description,omitempty"`
	Required    bool           `yaml:"required,omitempty"`
	Access      AccessPolicy   `yaml:"access,omitempty"`
	Signature   SignatureRef   `yaml:"signature,omitempty"`
	GeneratedAt string         `yaml:"generated_at,omitempty"`
	Entries     entriesSection `yaml:"entries,omitempty"`
}

type entriesSection struct {
	Root string `yaml:"root,omitempty"` // override default "entries" subdir
}

// frontmatter describes the parsed YAML front-matter of a Markdown entry.
type frontmatter struct {
	Name        string         `yaml:"name"`
	Kind        string         `yaml:"kind"`
	Tags        []string       `yaml:"tags,omitempty"`
	Scope       Scope          `yaml:"scope,omitempty"`
	Frontmatter map[string]any `yaml:",inline"`
}

// ParseDir reads a pack rooted at dir and returns a typed [ContextPack].
// The pack is *not* validated by this function — call [Validate] (or
// [ParseAndValidate]) to enforce schema, naming uniqueness, and size
// budgets.
//
// Path-traversal containment is inherited from the bundle resolver: every
// resolved entry path must remain within dir.
func ParseDir(dir string) (*ContextPack, error) {
	dir = filepath.Clean(dir)
	manifestPath := filepath.Join(dir, "pack.yaml")
	mraw, err := os.ReadFile(manifestPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, newErr(CodeManifestNotFound, manifestPath, "pack.yaml missing", err)
		}
		return nil, newErr(CodeManifestUnreadable, manifestPath, "could not read pack.yaml", err)
	}

	var m manifest
	if err := yaml.Unmarshal(mraw, &m); err != nil {
		return nil, newErr(CodeManifestMalformed, manifestPath, "invalid YAML 1.2 manifest", err)
	}

	pack := &ContextPack{
		Ref: PackRef{
			Name:    strings.TrimSpace(m.Name),
			Version: strings.TrimSpace(m.Version),
			Layer:   Layer(strings.TrimSpace(m.Layer)),
		},
		Issuer:      m.Issuer,
		License:     m.License,
		Description: m.Description,
		Required:    m.Required,
		Access:      m.Access,
		Signature:   m.Signature,
	}

	entriesRoot := strings.TrimSpace(m.Entries.Root)
	if entriesRoot == "" {
		entriesRoot = "entries"
	}
	entriesAbs, err := safeJoin(dir, entriesRoot)
	if err != nil {
		return nil, err
	}

	entries, err := walkEntries(dir, entriesAbs, pack.Ref)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	pack.Entries = entries

	for _, e := range entries {
		pack.SizeBytes += e.SizeBytes
	}

	pack.Ref.ContentHash = computePackHash(pack)
	for i := range pack.Entries {
		pack.Entries[i].SourcePack.ContentHash = pack.Ref.ContentHash
	}
	return pack, nil
}

// ParseAndValidate is a convenience wrapper that parses dir then runs the
// default validator. Returns the *ContextPack regardless of validation
// outcome, alongside the typed error so callers can inspect any partial
// state for diagnostic purposes.
func ParseAndValidate(dir string, opts ValidatorOptions) (*ContextPack, error) {
	p, err := ParseDir(dir)
	if err != nil {
		return p, err
	}
	if err := Validate(p, opts); err != nil {
		return p, err
	}
	return p, nil
}

// safeJoin resolves rel under root and confirms the result remains within
// root after symlink-aware cleanup. Mirrors core/bundle FR-014 containment.
func safeJoin(root, rel string) (string, error) {
	cleaned := filepath.Clean(rel)
	if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") {
		return "", newErr(CodePathTraversal, rel, "path escapes pack root", nil)
	}
	abs := filepath.Join(root, cleaned)
	rooted, err := filepath.Abs(root)
	if err != nil {
		return "", newErr(CodePathTraversal, root, "could not resolve pack root", err)
	}
	candidate, err := filepath.Abs(abs)
	if err != nil {
		return "", newErr(CodePathTraversal, abs, "could not resolve target", err)
	}
	if !strings.HasPrefix(candidate+string(filepath.Separator), rooted+string(filepath.Separator)) &&
		candidate != rooted {
		return "", newErr(CodePathTraversal, rel, "path escapes pack root", nil)
	}
	return abs, nil
}

func walkEntries(packRoot, entriesRoot string, ref PackRef) ([]ContextEntry, error) {
	info, err := os.Stat(entriesRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil // empty pack is parser-legal; validator will flag
		}
		return nil, newErr(CodeEntryUnreadable, entriesRoot, "could not stat entries root", err)
	}
	if !info.IsDir() {
		return nil, newErr(CodeEntryMalformed, entriesRoot, "entries root is not a directory", nil)
	}

	out := []ContextEntry{}
	walkErr := filepath.WalkDir(entriesRoot, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return newErr(CodeEntryUnreadable, path, "walk error", werr)
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		// Path-traversal containment: ensure path is within packRoot.
		rel, err := filepath.Rel(packRoot, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			return newErr(CodePathTraversal, path, "entry escapes pack root", err)
		}
		entry, err := parseEntryFile(path, rel, ref)
		if err != nil {
			return err
		}
		// Default kind from parent directory if frontmatter omits it.
		if entry.Kind == "" {
			entry.Kind = inferKindFromPath(rel)
		}
		out = append(out, entry)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return out, nil
}

func inferKindFromPath(rel string) EntryKind {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 {
		return ""
	}
	switch parts[1] {
	case "glossary":
		return KindGlossary
	case "explanations", "explanation":
		return KindExplanation
	case "skills", "skill":
		return KindSkill
	case "guidance", "agents":
		return KindGuidance
	}
	return ""
}

// parseEntryFile parses one Markdown-with-YAML-frontmatter file. The
// frontmatter is delimited by lines containing only `---`. Frontmatter is
// required; a body-only file is a parser error so authoring mistakes are
// caught early (Risk R6).
func parseEntryFile(absPath, relPath string, ref PackRef) (ContextEntry, error) {
	raw, err := os.ReadFile(absPath)
	if err != nil {
		return ContextEntry{}, newErr(CodeEntryUnreadable, relPath, "could not read entry", err)
	}

	fm, body, err := splitFrontmatter(raw)
	if err != nil {
		return ContextEntry{}, newErr(CodeFrontmatterInvalid, relPath, err.Error(), err)
	}
	if fm == nil {
		return ContextEntry{}, newErr(CodeFrontmatterMissing, relPath, "entry has no YAML frontmatter", nil)
	}

	var f frontmatter
	if err := yaml.Unmarshal(fm, &f); err != nil {
		return ContextEntry{}, newErr(CodeFrontmatterInvalid, relPath, "invalid YAML frontmatter", err)
	}

	entry := ContextEntry{
		Name:        strings.TrimSpace(f.Name),
		Kind:        EntryKind(strings.TrimSpace(f.Kind)),
		Body:        body,
		Frontmatter: f.Frontmatter,
		Scope:       f.Scope,
		Tags:        f.Tags,
		SourceLayer: ref.Layer,
		SourcePack:  ref,
		SizeBytes:   int64(len(body) + len(fm)),
		RelPath:     filepath.ToSlash(relPath),
	}
	entry.ContentHash = entryHash(entry)
	if entry.Name == "" {
		// Default name from filename (without extension) if frontmatter omits.
		base := filepath.Base(relPath)
		entry.Name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return entry, nil
}

// splitFrontmatter returns the YAML frontmatter bytes and remaining body.
// Recognised delimiter: a line containing only "---" at byte offset 0,
// followed by frontmatter, terminated by another "---" on its own line.
func splitFrontmatter(raw []byte) (fm []byte, body []byte, err error) {
	lf := []byte{'\n'}
	delim := []byte("---")

	// Tolerate a leading UTF-8 BOM.
	if bytes.HasPrefix(raw, []byte{0xef, 0xbb, 0xbf}) {
		raw = raw[3:]
	}
	// Tolerate leading blank lines before the opening delimiter.
	trim := raw
	for len(trim) > 0 && (trim[0] == ' ' || trim[0] == '\t' || trim[0] == '\n' || trim[0] == '\r') {
		trim = trim[1:]
	}
	if !bytes.HasPrefix(trim, delim) {
		return nil, raw, nil
	}
	// Find the end of the opening delimiter line.
	openEnd := bytes.Index(trim, lf)
	if openEnd < 0 {
		return nil, nil, fmt.Errorf("frontmatter opening delimiter not terminated")
	}
	// Confirm the opening delimiter line is exactly `---` (allow whitespace/CR).
	openLine := bytes.TrimRight(trim[:openEnd], " \t\r")
	if !bytes.Equal(openLine, delim) {
		return nil, raw, nil
	}
	rest := trim[openEnd+1:]
	// Find a closing delimiter line.
	for offset := 0; offset < len(rest); {
		nl := bytes.Index(rest[offset:], lf)
		var line []byte
		if nl < 0 {
			line = rest[offset:]
		} else {
			line = rest[offset : offset+nl]
		}
		trimmed := bytes.TrimRight(line, " \t\r")
		if bytes.Equal(trimmed, delim) {
			fmBytes := rest[:offset]
			var bodyBytes []byte
			if nl < 0 {
				bodyBytes = nil
			} else {
				bodyBytes = rest[offset+nl+1:]
			}
			return fmBytes, bodyBytes, nil
		}
		if nl < 0 {
			break
		}
		offset += nl + 1
	}
	return nil, nil, fmt.Errorf("frontmatter closing delimiter `---` not found")
}

// entryHash is the per-entry content hash recorded in the snapshot.
// Hashes a stable concatenation of name, kind, scope, and body so that
// non-content metadata changes still produce a different hash.
func entryHash(e ContextEntry) string {
	h := sha256.New()
	h.Write([]byte("name:"))
	h.Write([]byte(e.Name))
	h.Write([]byte{0})
	h.Write([]byte("kind:"))
	h.Write([]byte(e.Kind))
	h.Write([]byte{0})
	for _, w := range e.Scope.Workflows {
		h.Write([]byte("wf:"))
		h.Write([]byte(w))
		h.Write([]byte{0})
	}
	for _, a := range e.Scope.Agents {
		h.Write([]byte("ag:"))
		h.Write([]byte(a))
		h.Write([]byte{0})
	}
	h.Write([]byte("body:"))
	h.Write(e.Body)
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// computePackHash builds a Merkle-style aggregate over entry hashes plus
// pack identity. The signature envelope (separately materialised) covers
// this aggregate.
func computePackHash(p *ContextPack) string {
	h := sha256.New()
	fmt.Fprintf(h, "name:%s\x00version:%s\x00layer:%s\x00", p.Ref.Name, p.Ref.Version, p.Ref.Layer)
	// entries are already sorted by name in ParseDir before this is called.
	for _, e := range p.Entries {
		h.Write([]byte(e.Name))
		h.Write([]byte{0})
		h.Write([]byte(e.ContentHash))
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
