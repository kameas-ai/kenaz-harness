// Package contexts — AGENTS.md-style context module support.
//
// A context *directory* is a "module" iff it contains a root file named
// context.md or agents.md (case-insensitive; context.md preferred). The
// root file may carry YAML front-matter with an `always: [paths]` list
// declaring which sibling files to load eagerly at conversation start;
// everything else in the directory is reachable only on demand via the
// read_context_file built-in tool.
//
// This file adds three public helpers on Library:
//
//	RootFile(dir) (relpath, ok bool) — find the root file for a dir.
//	IsModule(dir) bool               — true when a root file exists.
//
// And a Module value type:
//
//	Module{Root, Always, Others}     — parsed module descriptor.
//
// Front-matter is parsed from the root file using a small internal YAML
// parser (gopkg.in/yaml.v3 is already a transitive dep).
package contexts

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// rootFileNames is the ordered candidate list. context.md is preferred
// over agents.md when both exist (spec §2).
var rootFileNames = []string{
	"context.md",
	"agents.md",
}

// Module is the parsed descriptor for a context module directory.
//
// Root is the library-relative path of the root file (e.g.
// "my-module/context.md").  Always is the list of library-relative
// paths declared in the root file's front-matter `always:` key; every
// path is resolved relative to the module directory and normalised to a
// slash-separated library-relative path. Others holds every other .md /
// .txt file in the same directory that is not the root — these are
// accessible only via read_context_file.
type Module struct {
	// Root is the slash-separated library-relative path of the root file.
	Root string
	// Always is the ordered list of library-relative paths that are
	// eagerly loaded at conversation start alongside the root.
	Always []string
	// Others is every other recognised content file in the module
	// directory (not the root, not in Always). Accessible on demand.
	Others []string
}

// RootFile returns the library-relative path of the root file inside
// dir (which is itself a library-relative path), and whether it exists.
// dir may be empty (meaning the library root itself). The search is
// case-insensitive to accommodate authors on case-insensitive
// file systems.
func (l *Library) RootFile(dir string) (relpath string, ok bool) {
	if l == nil {
		return "", false
	}
	var absDir string
	if dir == "" {
		absDir = l.root
	} else {
		rel, err := l.cleanRel(dir)
		if err != nil {
			return "", false
		}
		absDir = filepath.Join(l.root, rel)
	}
	// Read the directory entries and look for a root file candidate.
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return "", false
	}
	// Build a lower-case → real-name map for case-insensitive matching.
	nameMap := make(map[string]string, len(entries))
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		nameMap[strings.ToLower(e.Name())] = e.Name()
	}
	for _, candidate := range rootFileNames {
		if realName, found := nameMap[candidate]; found {
			if dir == "" {
				return realName, true
			}
			rel, err := l.cleanRel(dir + "/" + realName)
			if err != nil {
				return "", false
			}
			return rel, true
		}
	}
	return "", false
}

// IsModule reports whether dir is a context module — i.e. whether a
// root file (context.md or agents.md) exists within it.
func (l *Library) IsModule(dir string) bool {
	_, ok := l.RootFile(dir)
	return ok
}

// ParseModule reads and parses the module rooted at dir. It discovers
// the root file, parses its YAML front-matter for the `always:` list,
// and enumerates the other files in the directory.
//
// ParseModule returns ErrNotFound when dir does not exist and a wrapped
// error when there is no root file (calling code should check IsModule
// first when the caller is optional).
func (l *Library) ParseModule(dir string) (Module, error) {
	if l == nil {
		return Module{}, ErrNotFound
	}

	rootRel, ok := l.RootFile(dir)
	if !ok {
		return Module{}, ErrNotFound
	}

	// Derive the absolute directory path.
	var absDir string
	if dir == "" {
		absDir = l.root
	} else {
		rel, err := l.cleanRel(dir)
		if err != nil {
			return Module{}, err
		}
		absDir = filepath.Join(l.root, rel)
	}

	// Read the root file's YAML front-matter.
	absRoot := filepath.Join(l.root, rootRel)
	rawRoot, err := os.ReadFile(absRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return Module{}, ErrNotFound
		}
		return Module{}, err
	}

	alwaysRaw, parseErr := parseAlways(rawRoot)
	// We don't fail hard on front-matter parse errors — an author might
	// write a context.md without any front-matter, which is valid. The
	// root file is still the root; always: defaults to empty.
	_ = parseErr

	// Resolve always: paths relative to the module directory.
	var always []string
	for _, raw := range alwaysRaw {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		// Resolve relative to the module dir.
		var candidate string
		if dir == "" {
			candidate = raw
		} else {
			candidate = dir + "/" + raw
		}
		rel, err := l.cleanRel(candidate)
		if err != nil {
			continue // skip malformed always: entries
		}
		abs := filepath.Join(l.root, rel)
		if err := l.assertWithinRoot(abs); err != nil {
			continue // skip escaping paths
		}
		always = append(always, rel)
	}

	// Enumerate all other content files in the module directory
	// (not the root, not in the always list, only recognised extensions).
	alwaysSet := make(map[string]bool, len(always))
	alwaysSet[rootRel] = true
	for _, a := range always {
		alwaysSet[a] = true
	}

	entries, err := os.ReadDir(absDir)
	if err != nil {
		return Module{}, err
	}
	var others []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !hasAllowedExt(e.Name()) {
			continue
		}
		var rel string
		if dir == "" {
			rel = e.Name()
		} else {
			rel = dir + "/" + e.Name()
		}
		rel = filepath.ToSlash(filepath.Clean(rel))
		if alwaysSet[rel] {
			continue
		}
		others = append(others, rel)
	}

	return Module{
		Root:   rootRel,
		Always: always,
		Others: others,
	}, nil
}

// moduleFrontmatter is the parsed shape of the root-file YAML front-matter.
// Only the always: field is used; unrecognised keys are silently ignored.
type moduleFrontmatter struct {
	Always []string `yaml:"always"`
}

// parseAlways extracts the always: list from the YAML front-matter of a
// root file. Front-matter is delimited by leading "---" … "---" as in
// standard Markdown/YAML front-matter. Returns nil + nil when there is
// no front-matter (root-file-without-front-matter is valid).
func parseAlways(raw []byte) ([]string, error) {
	fm, _, err := splitModuleFrontmatter(raw)
	if err != nil || fm == nil {
		return nil, err
	}
	var mfm moduleFrontmatter
	if yerr := yaml.Unmarshal(fm, &mfm); yerr != nil {
		return nil, yerr
	}
	return mfm.Always, nil
}

// splitModuleFrontmatter is a small duplicate-free extractor. The pack
// parser's splitFrontmatter lives in core/context/pack (a sibling
// package) and requires frontmatter to be present — our use-case is the
// opposite (front-matter optional). We replicate only the splitting
// logic here rather than importing across packages.
func splitModuleFrontmatter(raw []byte) (fm []byte, body []byte, err error) {
	const delim = "---"
	lines := strings.SplitAfter(string(raw), "\n")
	if len(lines) == 0 {
		return nil, raw, nil
	}
	// Find the opening delimiter (must be the very first non-empty line
	// or a leading BOM + blank lines).
	start := -1
	for i, line := range lines {
		trimmed := strings.TrimRight(line, " \t\r\n")
		if trimmed == "" {
			continue
		}
		if trimmed == delim {
			start = i
		}
		break
	}
	if start < 0 {
		return nil, raw, nil
	}
	// Find the closing delimiter.
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimRight(lines[i], " \t\r\n")
		if trimmed == delim {
			fmBody := strings.Join(lines[start+1:i], "")
			rest := strings.Join(lines[i+1:], "")
			return []byte(fmBody), []byte(rest), nil
		}
	}
	return nil, nil, nil // closing delimiter not found — treat as no front-matter
}

// ContextModuleStub is the content written to a new context.md when
// CreateFolder scaffolds a module root. It explains the always/on-demand
// model in-line so authors immediately understand the authoring pattern.
const ContextModuleStub = `---
always: []
---

# Context Module

This directory is a context module. Files listed under **always:** are
injected into every conversation automatically. All other files in this
directory are available on demand: the assistant will call
**read_context_file** to load them when they become relevant.

## Usage

1. Edit this file to describe the module's purpose and key conventions.
2. Add supporting files (e.g. conventions.md, glossary.md).
3. Set **always: [conventions.md]** in the front-matter above to eager-load
   any file that should *always* be in context.
4. Attach this folder to a session or project from the Context tab.
`
