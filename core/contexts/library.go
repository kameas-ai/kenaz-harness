// Package contexts implements the Context Library — a flat pool of
// markdown / text content rooted at <DataDir>/contexts/. The library
// is content-only: scope (global / project / session) is a separate
// concern handled by later WPs (see kitty-specs/context-library-01KQ3MF1).
//
// Library exposes file-tree CRUD + a most-recently-applied LRU. Every
// path argument is validated against the library root: ".." traversal,
// absolute paths, and symlink-escape are rejected before any I/O.
//
// Design notes:
//   - Allowed extensions are the small set users actually attach as
//     context: .md / .markdown / .txt. Save rejects everything else.
//   - Save enforces a 1 MiB ceiling per file. The limit isn't a privacy
//     control — it's a sanity check so a stray paste doesn't wedge the
//     UI's preview pane. WP05 may surface the cap as a setting.
//   - Delete moves into <root>/.trash/<RFC3339>-<basename>; 30-day
//     retention is policy, swept by SweepTrash. The trash directory is
//     hidden from Tree() so the user's library view stays clean.
//   - RecentlyApplied is a JSON file at <root>/.recent.json. WP01
//     ships the data structure; the LLM send pipeline will start
//     touching it from WP03+.
package contexts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Library is a content-only file-tree CRUD surface anchored at root.
type Library struct {
	root string

	mu       sync.Mutex
	watchers []*Watcher
}

// NodeKind discriminates folders from files in a tree response.
type NodeKind string

const (
	// KindFolder is a directory node. Children populated recursively.
	KindFolder NodeKind = "folder"
	// KindFile is a leaf file node. Children is always nil.
	KindFile NodeKind = "file"
)

// Node is one entry in the recursive tree returned by Tree.
//
// Path is the slash-separated path RELATIVE to the library root —
// frontends never see absolute filesystem paths and bindings reject
// them on the inbound side.
type Node struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Kind     NodeKind  `json:"kind"`
	Size     int64     `json:"size,omitempty"`
	Modified time.Time `json:"modified,omitempty"`
	Children []Node    `json:"children,omitempty"`
}

// Errors surfaced by Library. Wrapped with %w so callers can match
// without depending on package internals.
var (
	ErrInvalidPath        = errors.New("contexts: invalid path")
	ErrUnsupportedExt     = errors.New("contexts: unsupported file extension")
	ErrFileTooLarge       = errors.New("contexts: file exceeds 1 MiB limit")
	ErrSymlinkEscape      = errors.New("contexts: path resolves outside library root")
	ErrNotFound           = errors.New("contexts: not found")
	ErrAlreadyExists      = errors.New("contexts: already exists")
	ErrIsDirectory        = errors.New("contexts: path is a directory")
)

// MaxFileBytes is the per-file ceiling enforced by Save. Public so
// tests + future settings UI can reference the same value.
const MaxFileBytes = 1 << 20 // 1 MiB

// allowedExtensions is the closed set of file extensions Save accepts.
// All comparisons are lower-cased.
var allowedExtensions = map[string]struct{}{
	".md":       {},
	".markdown": {},
	".txt":      {},
}

const (
	trashDirName  = ".trash"
	recentJSONRel = ".recent.json"
	trashTTL      = 30 * 24 * time.Hour
)

// Open returns a Library rooted at rootPath. The directory is created
// (with mode 0o700) when it does not exist so the chassis can boot
// without the user pre-creating <DataDir>/contexts/. Symlinks at the
// root are rejected: the rest of the API resolves real paths and a
// symlinked root would defeat the escape check.
func Open(rootPath string) (*Library, error) {
	if rootPath == "" {
		return nil, errors.New("contexts: Open requires a non-empty root")
	}
	if !filepath.IsAbs(rootPath) {
		return nil, fmt.Errorf("%w: root must be absolute", ErrInvalidPath)
	}
	if err := os.MkdirAll(rootPath, 0o700); err != nil {
		return nil, fmt.Errorf("contexts: create root %q: %w", rootPath, err)
	}
	resolved, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return nil, fmt.Errorf("contexts: resolve root %q: %w", rootPath, err)
	}
	return &Library{root: resolved}, nil
}

// Root returns the (resolved) absolute root path. Wails surfaces this
// to the frontend as a status hint ("library at /…/.harness/contexts").
func (l *Library) Root() string {
	if l == nil {
		return ""
	}
	return l.root
}

// Tree walks the library, returning the recursive tree as a single
// Node whose Path is "" (root). Hidden entries (dotfiles) are omitted
// — including the .trash dir and .recent.json metadata.
func (l *Library) Tree() (Node, error) {
	if l == nil {
		return Node{}, errors.New("contexts: nil library")
	}
	return l.walk(l.root, "")
}

// walk builds a Node for absPath. relPath is the slash-separated
// library-relative path of absPath; root passes "" so its children
// surface as top-level entries.
func (l *Library) walk(absPath, relPath string) (Node, error) {
	info, err := os.Stat(absPath)
	if err != nil {
		return Node{}, err
	}
	if !info.IsDir() {
		return Node{
			Name:     info.Name(),
			Path:     relPath,
			Kind:     KindFile,
			Size:     info.Size(),
			Modified: info.ModTime().UTC(),
		}, nil
	}
	entries, err := os.ReadDir(absPath)
	if err != nil {
		return Node{}, err
	}
	// Stable lexicographic ordering: folders first, then files,
	// each group alphabetical. Matches what users expect from a
	// file browser and keeps tree-diff tests deterministic.
	sort.Slice(entries, func(i, j int) bool {
		ai, aj := entries[i], entries[j]
		if ai.IsDir() != aj.IsDir() {
			return ai.IsDir() && !aj.IsDir()
		}
		return ai.Name() < aj.Name()
	})
	children := make([]Node, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			// Hidden: .trash, .recent.json, dotfiles users dropped
			// in by mistake.
			continue
		}
		childAbs := filepath.Join(absPath, name)
		childRel := name
		if relPath != "" {
			childRel = relPath + "/" + name
		}
		child, err := l.walk(childAbs, childRel)
		if err != nil {
			// Surface partial trees rather than failing hard — a
			// single unreadable subtree should not blank the UI.
			continue
		}
		children = append(children, child)
	}
	rootName := info.Name()
	if relPath == "" {
		rootName = ""
	}
	return Node{
		Name:     rootName,
		Path:     relPath,
		Kind:     KindFolder,
		Modified: info.ModTime().UTC(),
		Children: children,
	}, nil
}

// Get reads a file and returns its contents as a string. Path
// validation rejects extensions outside the allowed set so the
// frontend can't accidentally exfil arbitrary blobs.
func (l *Library) Get(path string) (string, error) {
	abs, err := l.resolveExisting(path, false)
	if err != nil {
		return "", err
	}
	if !hasAllowedExt(abs) {
		return "", fmt.Errorf("%w: %s", ErrUnsupportedExt, filepath.Ext(abs))
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return "", fmt.Errorf("contexts: read %q: %w", path, err)
	}
	return string(b), nil
}

// Save writes content to path, creating any missing parent
// directories. The path's extension must be in the allowed set and
// content must be ≤ 1 MiB. Existing files are overwritten atomically
// (write-temp + rename).
func (l *Library) Save(path, content string) error {
	if l == nil {
		return errors.New("contexts: nil library")
	}
	rel, err := l.cleanRel(path)
	if err != nil {
		return err
	}
	if !hasAllowedExt(rel) {
		return fmt.Errorf("%w: %s", ErrUnsupportedExt, filepath.Ext(rel))
	}
	if int64(len(content)) > MaxFileBytes {
		return fmt.Errorf("%w: %d bytes", ErrFileTooLarge, len(content))
	}
	abs := filepath.Join(l.root, rel)
	if err := l.assertWithinRoot(abs); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return fmt.Errorf("contexts: mkdir parents %q: %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".save-*")
	if err != nil {
		return fmt.Errorf("contexts: tmp file %q: %w", path, err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("contexts: write %q: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("contexts: close %q: %w", path, err)
	}
	if err := os.Rename(tmpName, abs); err != nil {
		return fmt.Errorf("contexts: rename %q: %w", path, err)
	}
	l.notify()
	return nil
}

// CreateFolder creates a new directory at path (recursively).
// Idempotent: succeeds if the directory already exists.
func (l *Library) CreateFolder(path string) error {
	if l == nil {
		return errors.New("contexts: nil library")
	}
	rel, err := l.cleanRel(path)
	if err != nil {
		return err
	}
	if rel == "" {
		return fmt.Errorf("%w: cannot create root", ErrInvalidPath)
	}
	abs := filepath.Join(l.root, rel)
	if err := l.assertWithinRoot(abs); err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err == nil {
		if info.IsDir() {
			return nil
		}
		return fmt.Errorf("%w: %s exists as a file", ErrAlreadyExists, path)
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("contexts: stat %q: %w", path, err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return fmt.Errorf("contexts: mkdir %q: %w", path, err)
	}
	l.notify()
	return nil
}

// Rename moves oldPath to newPath. Both paths are validated against
// the library root. The target's parent directory is created if it
// doesn't exist. Renaming a file's extension out of the allowed set
// is rejected so the renamed file isn't orphaned from Get.
func (l *Library) Rename(oldPath, newPath string) error {
	if l == nil {
		return errors.New("contexts: nil library")
	}
	oldAbs, err := l.resolveExisting(oldPath, true)
	if err != nil {
		return err
	}
	newRel, err := l.cleanRel(newPath)
	if err != nil {
		return err
	}
	if newRel == "" {
		return fmt.Errorf("%w: cannot rename to root", ErrInvalidPath)
	}
	newAbs := filepath.Join(l.root, newRel)
	if err := l.assertWithinRoot(newAbs); err != nil {
		return err
	}
	info, err := os.Stat(oldAbs)
	if err != nil {
		return fmt.Errorf("contexts: stat %q: %w", oldPath, err)
	}
	if !info.IsDir() && !hasAllowedExt(newRel) {
		return fmt.Errorf("%w: %s", ErrUnsupportedExt, filepath.Ext(newRel))
	}
	if _, err := os.Stat(newAbs); err == nil {
		return fmt.Errorf("%w: %s", ErrAlreadyExists, newPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("contexts: stat %q: %w", newPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(newAbs), 0o700); err != nil {
		return fmt.Errorf("contexts: mkdir parents %q: %w", newPath, err)
	}
	if err := os.Rename(oldAbs, newAbs); err != nil {
		return fmt.Errorf("contexts: rename: %w", err)
	}
	l.notify()
	return nil
}

// Delete moves path to <root>/.trash/<RFC3339>-<basename>. Trash is
// swept by SweepTrash on a 30-day TTL. A trashed file can be
// recovered manually for the retention window.
func (l *Library) Delete(path string) error {
	if l == nil {
		return errors.New("contexts: nil library")
	}
	abs, err := l.resolveExisting(path, true)
	if err != nil {
		return err
	}
	trashRoot := filepath.Join(l.root, trashDirName)
	if err := os.MkdirAll(trashRoot, 0o700); err != nil {
		return fmt.Errorf("contexts: mkdir trash: %w", err)
	}
	base := filepath.Base(abs)
	stamp := time.Now().UTC().Format("20060102T150405.000000000")
	dst := filepath.Join(trashRoot, stamp+"-"+base)
	// Disambiguate on the unlikely tick collision.
	for i := 1; i < 10; i++ {
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			break
		}
		dst = filepath.Join(trashRoot, fmt.Sprintf("%s-%d-%s", stamp, i, base))
	}
	if err := os.Rename(abs, dst); err != nil {
		return fmt.Errorf("contexts: trash %q: %w", path, err)
	}
	l.notify()
	return nil
}

// SweepTrash removes trash entries older than 30 days. Safe to call
// on every boot — a missing trash directory is a no-op.
func (l *Library) SweepTrash() error {
	if l == nil {
		return nil
	}
	trashRoot := filepath.Join(l.root, trashDirName)
	entries, err := os.ReadDir(trashRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("contexts: list trash: %w", err)
	}
	cutoff := time.Now().UTC().Add(-trashTTL)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().UTC().Before(cutoff) {
			_ = os.RemoveAll(filepath.Join(trashRoot, e.Name()))
		}
	}
	return nil
}

// RecentlyApplied returns up to limit most-recently-applied paths.
// "Applied" means used as a context attachment — WP01 ships the
// JSON store; later WPs flip the call sites that mark a path as
// applied.
func (l *Library) RecentlyApplied(limit int) []string {
	if l == nil || limit <= 0 {
		return nil
	}
	entries := l.loadRecent()
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries
}

// MarkApplied prepends path to the recents list (deduped). The
// resulting list is capped to 64 entries — the LRU lives on disk so
// the rail's "recently applied" pane survives Wails restarts.
func (l *Library) MarkApplied(path string) error {
	if l == nil {
		return errors.New("contexts: nil library")
	}
	rel, err := l.cleanRel(path)
	if err != nil {
		return err
	}
	abs := filepath.Join(l.root, rel)
	if err := l.assertWithinRoot(abs); err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return fmt.Errorf("contexts: stat %q: %w", path, err)
	}
	entries := l.loadRecent()
	out := make([]string, 0, len(entries)+1)
	out = append(out, rel)
	for _, e := range entries {
		if e == rel {
			continue
		}
		out = append(out, e)
	}
	const recentCap = 64
	if len(out) > recentCap {
		out = out[:recentCap]
	}
	return l.writeRecent(out)
}

// loadRecent reads the LRU JSON. A missing or corrupt file returns
// nil — the caller treats that as "no recents yet".
func (l *Library) loadRecent() []string {
	b, err := os.ReadFile(filepath.Join(l.root, recentJSONRel))
	if err != nil {
		return nil
	}
	var entries []string
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil
	}
	return entries
}

func (l *Library) writeRecent(entries []string) error {
	b, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("contexts: marshal recent: %w", err)
	}
	dst := filepath.Join(l.root, recentJSONRel)
	tmp, err := os.CreateTemp(l.root, ".recent-*")
	if err != nil {
		return fmt.Errorf("contexts: tmp recent: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("contexts: write recent: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("contexts: close recent: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("contexts: rename recent: %w", err)
	}
	return nil
}

// cleanRel sanitises a frontend-supplied path. Reject absolute paths,
// "..", null bytes, and Windows drive letters; collapse separators.
// Returns the cleaned slash-separated relative path.
func (l *Library) cleanRel(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("%w: empty path", ErrInvalidPath)
	}
	if strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("%w: null byte", ErrInvalidPath)
	}
	if filepath.IsAbs(p) {
		return "", fmt.Errorf("%w: absolute path %q", ErrInvalidPath, p)
	}
	// Normalise to forward slashes so cross-platform users can paste
	// either form. filepath.Clean handles the rest (including "..").
	p = strings.ReplaceAll(p, "\\", "/")
	cleaned := filepath.ToSlash(filepath.Clean(p))
	if cleaned == "." {
		return "", fmt.Errorf("%w: cannot operate on root", ErrInvalidPath)
	}
	if strings.HasPrefix(cleaned, "../") || cleaned == ".." || strings.Contains(cleaned, "/../") {
		return "", fmt.Errorf("%w: %q escapes root", ErrInvalidPath, p)
	}
	if strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("%w: leading slash in %q", ErrInvalidPath, p)
	}
	return cleaned, nil
}

// resolveExisting cleans path, joins it under root, then verifies the
// final resolved path is still inside root. allowDir controls whether
// directories are accepted (Get rejects them; Delete / Rename allow).
func (l *Library) resolveExisting(path string, allowDir bool) (string, error) {
	rel, err := l.cleanRel(path)
	if err != nil {
		return "", err
	}
	abs := filepath.Join(l.root, rel)
	if err := l.assertWithinRoot(abs); err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return "", fmt.Errorf("contexts: stat %q: %w", path, err)
	}
	if info.IsDir() && !allowDir {
		return "", fmt.Errorf("%w: %s", ErrIsDirectory, path)
	}
	return abs, nil
}

// assertWithinRoot resolves abs through symlinks and verifies the
// canonical form is still rooted at l.root. EvalSymlinks fails if abs
// doesn't exist; Save / CreateFolder call it before creating the
// target so we walk upwards to the nearest existing ancestor.
func (l *Library) assertWithinRoot(abs string) error {
	if !strings.HasPrefix(abs, l.root) {
		return fmt.Errorf("%w: %s", ErrSymlinkEscape, abs)
	}
	probe := abs
	for {
		resolved, err := filepath.EvalSymlinks(probe)
		if err == nil {
			if !strings.HasPrefix(resolved, l.root) {
				return fmt.Errorf("%w: %s -> %s", ErrSymlinkEscape, abs, resolved)
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("contexts: resolve %q: %w", abs, err)
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return fmt.Errorf("%w: cannot resolve %s", ErrSymlinkEscape, abs)
		}
		probe = parent
	}
}

// hasAllowedExt returns true if path's extension is in the allowlist.
func hasAllowedExt(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	_, ok := allowedExtensions[ext]
	return ok
}
