// Package fsbuiltins implements the builtin filesystem tools for the
// builtin-filesystem-tools-01KR3N4P mission. It registers six tools:
//
//   - kaneaz__read_file     — read a file's text content (WP01)
//   - kaneaz__list_dir      — list a directory's entries (WP01)
//   - kaneaz__glob          — glob match across a directory tree (WP01)
//   - kaneaz__grep          — in-process regex search (WP02)
//   - kaneaz__write_file    — atomic write to a path (WP03)
//   - kaneaz__edit_file     — replace a substring in a file (WP03)
//   - kaneaz__list_open_worklist — enumerate pending worklist items (WP04)
//
// All tools gate on the same Cedar fs.Gate flow. Read tools gate on
// OpRead, write tools on OpWrite. The edit_file tool additionally
// enforces that the file was read in the current session before editing
// (ErrEditWithoutRead).
//
// Per-session "read" state is held in a process-global ReadSet keyed by
// session ID. The chat runner (or dispatch adapter) is expected to call
// ReadSet.Add(sessionID, canonicalPath) after every successful
// kaneaz__read_file call — the edit_file tool consults the set.
//
// DIRECTIVE_001: backend-only; no CGo, no GUI imports.
package fsbuiltins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	corefs "github.com/kameas-ai/kenaz-harness/core/tools/fs"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// Tool names — kaneaz__ prefix is reserved for built-ins.
const (
	NameReadFile          = "kaneaz__read_file"
	NameListDir           = "kaneaz__list_dir"
	NameGlob              = "kaneaz__glob"
	NameGrep              = "kaneaz__grep"
	NameWriteFile         = "kaneaz__write_file"
	NameEditFile          = "kaneaz__edit_file"
	NameListOpenWorklist  = "kaneaz__list_open_worklist"
)

// ErrEditWithoutRead is returned by edit_file when the canonical path
// was not previously returned by a read_file call in this session.
var ErrEditWithoutRead = errors.New("fsbuiltins: edit_file requires a prior read_file call for this path in the current session")

// ErrFSDenied is returned when the Gate denies access.
var ErrFSDenied = errors.New("fsbuiltins: filesystem access denied by policy")

// Options is the constructor config for all fs builtin tools.
type Options struct {
	// Gate is the Cedar permission gate. Required in production; may be
	// nil in tests (all paths are then permitted without policy check).
	Gate *corefs.Gate
	// ReadSet tracks per-session paths that have been read. Required for
	// edit_file enforcement.
	ReadSet *ReadSet
	// WorklistDir is the directory the worklist tool scans. When empty,
	// list_open_worklist returns an empty list.
	WorklistDir string
	// Enabled is the function consulted on every Call. When nil, all
	// calls are accepted. When the function returns false the tool
	// returns an "feature not enabled" error without touching the disk.
	ReadEnabled  func() bool
	WriteEnabled func() bool
}

// ReadSet is a process-global per-session set of canonical paths that
// have been read by the current session. edit_file requires a path to
// be in the set before accepting edits.
//
// ReadSet is safe for concurrent use.
type ReadSet struct {
	mu   sync.RWMutex
	data map[string]map[string]struct{} // sessionID → set[canonicalPath]
}

// NewReadSet returns an empty ReadSet.
func NewReadSet() *ReadSet {
	return &ReadSet{data: map[string]map[string]struct{}{}}
}

// noSessionSentinel is the map key used when the session ID is empty
// (e.g. in tests or non-session contexts). An empty sessionID should
// not silently skip enforcement; instead, all empty-session reads and
// edits share the same bucket.
const noSessionSentinel = "__no_session__"

func normalizeSessionID(id string) string {
	if id == "" {
		return noSessionSentinel
	}
	return id
}

// Add records that sessionID has read canonicalPath.
func (r *ReadSet) Add(sessionID, canonicalPath string) {
	if r == nil || canonicalPath == "" {
		return
	}
	sid := normalizeSessionID(sessionID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.data[sid] == nil {
		r.data[sid] = map[string]struct{}{}
	}
	r.data[sid][canonicalPath] = struct{}{}
}

// Has reports whether sessionID has previously read canonicalPath.
func (r *ReadSet) Has(sessionID, canonicalPath string) bool {
	if r == nil || canonicalPath == "" {
		return false
	}
	sid := normalizeSessionID(sessionID)
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.data[sid] == nil {
		return false
	}
	_, ok := r.data[sid][canonicalPath]
	return ok
}

// Drop removes all entries for sessionID (called when a session ends).
func (r *ReadSet) Drop(sessionID string) {
	if r == nil {
		return
	}
	sid := normalizeSessionID(sessionID)
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, sid)
}

// sessionIDFromCtx extracts the session ID from context using the
// toolloop package's convention.
func sessionIDFromCtx(ctx context.Context) string {
	return toolloop.SessionIDFromContext(ctx)
}

// gateCheck calls the Gate for the given op and path. When Gate is nil
// the call is always permitted (test / nil-gate path). Returns the
// canonical path on success.
func gateCheck(ctx context.Context, gate *corefs.Gate, op corefs.Op, rawPath string) (string, error) {
	canonical, err := corefs.Canonicalize(rawPath)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}
	if gate == nil {
		// No gate wired — permit unconditionally (test path).
		return canonical, nil
	}
	d, err := gate.Evaluate(ctx, op, canonical)
	if err != nil {
		return "", fmt.Errorf("gate: %w", err)
	}
	// cedar.Allow is the only permitting outcome; Deny and NotApplicable deny.
	if d.Outcome != cedar.Allow {
		return "", fmt.Errorf("%w: %s", ErrFSDenied, d.Reason)
	}
	return canonical, nil
}

// errResult returns a json.RawMessage encoding {"error": msg, "is_error": true}.
func errResult(msg string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"is_error":true,"error":%s}`, jsonString(msg)))
}

// okResult wraps a map/struct as a JSON result.
func okResult(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// jsonString returns msg as a JSON string literal.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// ─── read_file ──────────────────────────────────────────────────────────────

// ReadFileTool implements kaneaz__read_file.
type ReadFileTool struct {
	gate    *corefs.Gate
	readSet *ReadSet
	enabled func() bool
}

// NewReadFileTool constructs the read_file tool.
func NewReadFileTool(opts Options) *ReadFileTool {
	return &ReadFileTool{
		gate:    opts.Gate,
		readSet: opts.ReadSet,
		enabled: opts.ReadEnabled,
	}
}

func (t *ReadFileTool) Name() string { return NameReadFile }

func (t *ReadFileTool) Description() string {
	return "Read the full text content of a file. " +
		"Returns the file's contents as a UTF-8 string. " +
		"Large files are returned in full; use offset+limit args to page. " +
		"Required before edit_file can modify the file in the same session."
}

const readFileSchema = `{
  "type": "object",
  "properties": {
    "path": {
      "type": "string",
      "description": "Absolute or relative path to the file to read."
    },
    "offset": {
      "type": "integer",
      "description": "0-based byte offset to start reading from (default 0)."
    },
    "limit": {
      "type": "integer",
      "description": "Maximum number of bytes to return (default: entire file)."
    }
  },
  "required": ["path"]
}`

func (t *ReadFileTool) InputSchema() json.RawMessage { return json.RawMessage(readFileSchema) }

type readFileArgs struct {
	Path   string `json:"path"`
	Offset int64  `json:"offset"`
	Limit  int64  `json:"limit"`
}

type readFileResult struct {
	Content  string `json:"content"`
	ByteSize int64  `json:"byte_size"`
	Truncated bool  `json:"truncated"`
}

func (t *ReadFileTool) Call(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if t.enabled != nil && !t.enabled() {
		return errResult("read_file: FSReadEnabled is off"), nil
	}
	var args readFileArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return errResult("read_file: invalid args: " + err.Error()), nil
	}
	if args.Path == "" {
		return errResult("read_file: path is required"), nil
	}

	canonical, err := gateCheck(ctx, t.gate, corefs.OpRead, args.Path)
	if err != nil {
		return errResult("read_file: " + err.Error()), nil
	}

	f, err := os.Open(canonical)
	if err != nil {
		return errResult("read_file: " + err.Error()), nil
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return errResult("read_file: stat: " + err.Error()), nil
	}
	fileSize := stat.Size()

	if args.Offset > 0 {
		if _, err := f.Seek(args.Offset, io.SeekStart); err != nil {
			return errResult("read_file: seek: " + err.Error()), nil
		}
	}

	var reader io.Reader = f
	truncated := false
	if args.Limit > 0 {
		remaining := fileSize - args.Offset
		if args.Limit < remaining {
			reader = io.LimitReader(f, args.Limit)
			truncated = true
		}
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		return errResult("read_file: read: " + err.Error()), nil
	}

	// Record the read so edit_file can verify prior-read.
	sessionID := sessionIDFromCtx(ctx)
	if t.readSet != nil {
		t.readSet.Add(sessionID, canonical)
	}

	return okResult(readFileResult{
		Content:   string(data),
		ByteSize:  fileSize,
		Truncated: truncated,
	}), nil
}

// ─── list_dir ───────────────────────────────────────────────────────────────

// ListDirTool implements kaneaz__list_dir.
type ListDirTool struct {
	gate    *corefs.Gate
	enabled func() bool
}

// NewListDirTool constructs the list_dir tool.
func NewListDirTool(opts Options) *ListDirTool {
	return &ListDirTool{gate: opts.Gate, enabled: opts.ReadEnabled}
}

func (t *ListDirTool) Name() string { return NameListDir }

func (t *ListDirTool) Description() string {
	return "List entries in a directory. " +
		"Returns name, type (file/dir/symlink), and size for each entry. " +
		"Use recursive=true to walk subdirectories."
}

const listDirSchema = `{
  "type": "object",
  "properties": {
    "path": {
      "type": "string",
      "description": "Absolute or relative path to the directory."
    },
    "recursive": {
      "type": "boolean",
      "description": "When true, walks subdirectories recursively (default false)."
    },
    "max_entries": {
      "type": "integer",
      "description": "Maximum number of entries to return (default 1000)."
    }
  },
  "required": ["path"]
}`

func (t *ListDirTool) InputSchema() json.RawMessage { return json.RawMessage(listDirSchema) }

type listDirArgs struct {
	Path       string `json:"path"`
	Recursive  bool   `json:"recursive"`
	MaxEntries int    `json:"max_entries"`
}

type dirEntry struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Size    int64  `json:"size"`
	Path    string `json:"path"`
}

type listDirResult struct {
	Entries   []dirEntry `json:"entries"`
	Truncated bool       `json:"truncated"`
}

func (t *ListDirTool) Call(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if t.enabled != nil && !t.enabled() {
		return errResult("list_dir: FSReadEnabled is off"), nil
	}
	var args listDirArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return errResult("list_dir: invalid args: " + err.Error()), nil
	}
	if args.Path == "" {
		return errResult("list_dir: path is required"), nil
	}
	if args.MaxEntries <= 0 {
		args.MaxEntries = 1000
	}

	canonical, err := gateCheck(ctx, t.gate, corefs.OpRead, args.Path)
	if err != nil {
		return errResult("list_dir: " + err.Error()), nil
	}

	var entries []dirEntry
	truncated := false

	if args.Recursive {
		err = filepath.WalkDir(canonical, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil // skip unreadable entries
			}
			if path == canonical {
				return nil // skip root itself
			}
			if len(entries) >= args.MaxEntries {
				truncated = true
				return filepath.SkipAll
			}
			entries = append(entries, toDirEntry(path, canonical, d))
			return nil
		})
	} else {
		rawEntries, readErr := os.ReadDir(canonical)
		if readErr != nil {
			return errResult("list_dir: " + readErr.Error()), nil
		}
		for _, d := range rawEntries {
			if len(entries) >= args.MaxEntries {
				truncated = true
				break
			}
			fullPath := filepath.Join(canonical, d.Name())
			entries = append(entries, toDirEntry(fullPath, canonical, d))
		}
	}

	if err != nil {
		return errResult("list_dir: walk: " + err.Error()), nil
	}

	return okResult(listDirResult{Entries: entries, Truncated: truncated}), nil
}

func toDirEntry(path, root string, d fs.DirEntry) dirEntry {
	rel, _ := filepath.Rel(root, path)
	typ := "file"
	if d.IsDir() {
		typ = "dir"
	} else if d.Type()&fs.ModeSymlink != 0 {
		typ = "symlink"
	}
	var size int64
	if info, err := d.Info(); err == nil {
		size = info.Size()
	}
	return dirEntry{
		Name: d.Name(),
		Type: typ,
		Size: size,
		Path: rel,
	}
}

// ─── glob ───────────────────────────────────────────────────────────────────

// GlobTool implements kaneaz__glob.
type GlobTool struct {
	gate    *corefs.Gate
	enabled func() bool
}

// NewGlobTool constructs the glob tool.
func NewGlobTool(opts Options) *GlobTool {
	return &GlobTool{gate: opts.Gate, enabled: opts.ReadEnabled}
}

func (t *GlobTool) Name() string { return NameGlob }

func (t *GlobTool) Description() string {
	return "Find files matching a glob pattern relative to a base directory. " +
		"Pattern supports *, **, ?, and character classes. " +
		"Returns absolute paths of matching files."
}

const globSchema = `{
  "type": "object",
  "properties": {
    "pattern": {
      "type": "string",
      "description": "Glob pattern (e.g. '**/*.go', 'src/*.ts'). ** matches any number of path segments."
    },
    "base_dir": {
      "type": "string",
      "description": "Directory to root the search (default: current working directory)."
    },
    "max_results": {
      "type": "integer",
      "description": "Maximum number of results to return (default 500)."
    }
  },
  "required": ["pattern"]
}`

func (t *GlobTool) InputSchema() json.RawMessage { return json.RawMessage(globSchema) }

type globArgs struct {
	Pattern    string `json:"pattern"`
	BaseDir    string `json:"base_dir"`
	MaxResults int    `json:"max_results"`
}

type globResult struct {
	Matches   []string `json:"matches"`
	Truncated bool     `json:"truncated"`
}

func (t *GlobTool) Call(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if t.enabled != nil && !t.enabled() {
		return errResult("glob: FSReadEnabled is off"), nil
	}
	var args globArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return errResult("glob: invalid args: " + err.Error()), nil
	}
	if args.Pattern == "" {
		return errResult("glob: pattern is required"), nil
	}
	if args.MaxResults <= 0 {
		args.MaxResults = 500
	}
	if args.BaseDir == "" {
		var err error
		args.BaseDir, err = os.Getwd()
		if err != nil {
			return errResult("glob: getwd: " + err.Error()), nil
		}
	}

	canonical, err := gateCheck(ctx, t.gate, corefs.OpRead, args.BaseDir)
	if err != nil {
		return errResult("glob: " + err.Error()), nil
	}

	var matches []string
	truncated := false

	err = filepath.WalkDir(canonical, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // skip unreadable
		}
		if d.IsDir() {
			return nil // only match files
		}
		if len(matches) >= args.MaxResults {
			truncated = true
			return filepath.SkipAll
		}
		rel, relErr := filepath.Rel(canonical, path)
		if relErr != nil {
			rel = path
		}
		matched, matchErr := matchGlob(args.Pattern, rel)
		if matchErr != nil {
			return matchErr
		}
		if matched {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return errResult("glob: walk: " + err.Error()), nil
	}

	sort.Strings(matches)
	return okResult(globResult{Matches: matches, Truncated: truncated}), nil
}

// matchGlob matches a path against a glob pattern, supporting ** as a
// multi-segment wildcard. Uses filepath.Match for single-segment
// matching and a manual walk for ** patterns.
func matchGlob(pattern, path string) (bool, error) {
	// Normalise separators.
	pattern = filepath.ToSlash(pattern)
	path = filepath.ToSlash(path)

	if !strings.Contains(pattern, "**") {
		// Simple case: delegate to filepath.Match.
		matched, err := filepath.Match(filepath.FromSlash(pattern), filepath.FromSlash(path))
		return matched, err
	}

	// ** matching: split both into segments and match segment-by-segment,
	// treating ** as "zero or more segments."
	return matchDoublestar(strings.Split(pattern, "/"), strings.Split(path, "/")), nil
}

func matchDoublestar(pats, segs []string) bool {
	pi, si := 0, 0
	for pi < len(pats) && si < len(segs) {
		if pats[pi] == "**" {
			// ** can consume 0 or more segments.
			pi++
			if pi == len(pats) {
				return true // trailing ** matches everything
			}
			// Try matching the rest of the pattern against every suffix.
			for k := si; k <= len(segs); k++ {
				if matchDoublestar(pats[pi:], segs[k:]) {
					return true
				}
			}
			return false
		}
		ok, err := filepath.Match(pats[pi], segs[si])
		if err != nil || !ok {
			return false
		}
		pi++
		si++
	}
	// Consume any trailing ** patterns.
	for pi < len(pats) && pats[pi] == "**" {
		pi++
	}
	return pi == len(pats) && si == len(segs)
}

// ─── grep ───────────────────────────────────────────────────────────────────

// GrepTool implements kaneaz__grep using an internal walker (no ripgrep).
type GrepTool struct {
	gate    *corefs.Gate
	enabled func() bool
}

// NewGrepTool constructs the grep tool.
func NewGrepTool(opts Options) *GrepTool {
	return &GrepTool{gate: opts.Gate, enabled: opts.ReadEnabled}
}

func (t *GrepTool) Name() string { return NameGrep }

func (t *GrepTool) Description() string {
	return "Search for a regex pattern in files under a directory. " +
		"Uses an internal walker — does not shell out to ripgrep or grep. " +
		"Returns matching lines with file path and line number."
}

const grepSchema = `{
  "type": "object",
  "properties": {
    "pattern": {
      "type": "string",
      "description": "Go RE2 regular expression to search for."
    },
    "path": {
      "type": "string",
      "description": "File or directory to search. Directories are walked recursively."
    },
    "include": {
      "type": "string",
      "description": "Optional glob pattern to restrict which files are searched (e.g. '*.go')."
    },
    "case_insensitive": {
      "type": "boolean",
      "description": "When true, match is case-insensitive (default false)."
    },
    "max_results": {
      "type": "integer",
      "description": "Maximum number of matching lines to return (default 200)."
    }
  },
  "required": ["pattern", "path"]
}`

func (t *GrepTool) InputSchema() json.RawMessage { return json.RawMessage(grepSchema) }

type grepArgs struct {
	Pattern         string `json:"pattern"`
	Path            string `json:"path"`
	Include         string `json:"include"`
	CaseInsensitive bool   `json:"case_insensitive"`
	MaxResults      int    `json:"max_results"`
}

type grepMatch struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

type grepResult struct {
	Matches   []grepMatch `json:"matches"`
	Truncated bool        `json:"truncated"`
}

func (t *GrepTool) Call(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if t.enabled != nil && !t.enabled() {
		return errResult("grep: FSReadEnabled is off"), nil
	}
	var args grepArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return errResult("grep: invalid args: " + err.Error()), nil
	}
	if args.Pattern == "" {
		return errResult("grep: pattern is required"), nil
	}
	if args.Path == "" {
		return errResult("grep: path is required"), nil
	}
	if args.MaxResults <= 0 {
		args.MaxResults = 200
	}

	reStr := args.Pattern
	if args.CaseInsensitive {
		reStr = "(?i)" + reStr
	}
	re, err := regexp.Compile(reStr)
	if err != nil {
		return errResult("grep: invalid pattern: " + err.Error()), nil
	}

	canonical, err := gateCheck(ctx, t.gate, corefs.OpRead, args.Path)
	if err != nil {
		return errResult("grep: " + err.Error()), nil
	}

	var matches []grepMatch
	truncated := false

	grepFile := func(filePath string) error {
		data, readErr := os.ReadFile(filePath)
		if readErr != nil {
			return nil // skip unreadable files
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if len(matches) >= args.MaxResults {
				truncated = true
				return nil
			}
			if re.MatchString(line) {
				matches = append(matches, grepMatch{
					File:    filePath,
					Line:    i + 1,
					Content: line,
				})
			}
		}
		return nil
	}

	info, statErr := os.Stat(canonical)
	if statErr != nil {
		return errResult("grep: stat: " + statErr.Error()), nil
	}

	if info.IsDir() {
		walkErr := filepath.WalkDir(canonical, func(path string, d fs.DirEntry, wErr error) error {
			if wErr != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if truncated {
				return filepath.SkipAll
			}
			if args.Include != "" {
				matched, _ := filepath.Match(args.Include, d.Name())
				if !matched {
					return nil
				}
			}
			return grepFile(path)
		})
		if walkErr != nil {
			return errResult("grep: walk: " + walkErr.Error()), nil
		}
	} else {
		if err := grepFile(canonical); err != nil {
			return errResult("grep: " + err.Error()), nil
		}
	}

	return okResult(grepResult{Matches: matches, Truncated: truncated}), nil
}

// ─── write_file ─────────────────────────────────────────────────────────────

// WriteFileTool implements kaneaz__write_file with atomic-write pattern.
type WriteFileTool struct {
	gate    *corefs.Gate
	enabled func() bool
}

// NewWriteFileTool constructs the write_file tool.
func NewWriteFileTool(opts Options) *WriteFileTool {
	return &WriteFileTool{gate: opts.Gate, enabled: opts.WriteEnabled}
}

func (t *WriteFileTool) Name() string { return NameWriteFile }

func (t *WriteFileTool) Description() string {
	return "Write content to a file, replacing its entire content atomically. " +
		"Creates the file and any missing parent directories. " +
		"Uses a tmp-file-then-rename pattern for crash safety."
}

const writeFileSchema = `{
  "type": "object",
  "properties": {
    "path": {
      "type": "string",
      "description": "Absolute or relative path to the file to write."
    },
    "content": {
      "type": "string",
      "description": "The content to write (UTF-8)."
    },
    "mode": {
      "type": "integer",
      "description": "File permission bits in octal (default 0644)."
    }
  },
  "required": ["path", "content"]
}`

func (t *WriteFileTool) InputSchema() json.RawMessage { return json.RawMessage(writeFileSchema) }

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    int    `json:"mode"`
}

type writeFileResult struct {
	Written int64  `json:"written"`
	Path    string `json:"path"`
}

func (t *WriteFileTool) Call(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if t.enabled != nil && !t.enabled() {
		return errResult("write_file: FSWriteEnabled is off"), nil
	}
	var args writeFileArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return errResult("write_file: invalid args: " + err.Error()), nil
	}
	if args.Path == "" {
		return errResult("write_file: path is required"), nil
	}
	if args.Mode == 0 {
		args.Mode = 0o644
	}

	canonical, err := gateCheck(ctx, t.gate, corefs.OpWrite, args.Path)
	if err != nil {
		return errResult("write_file: " + err.Error()), nil
	}

	if err := atomicWrite(canonical, []byte(args.Content), fs.FileMode(args.Mode)); err != nil {
		return errResult("write_file: " + err.Error()), nil
	}

	return okResult(writeFileResult{Written: int64(len(args.Content)), Path: canonical}), nil
}

// atomicWrite writes data to path using a tmp-file-then-rename pattern.
// Creates missing parent directories. The rename is atomic on POSIX
// (same filesystem); Windows has weaker rename semantics but is still
// better than a direct truncate+write.
func atomicWrite(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".fsbuiltins-write-*")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after successful rename

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// ─── edit_file ──────────────────────────────────────────────────────────────

// EditFileTool implements kaneaz__edit_file. Enforces that the path was
// read in the current session before accepting an edit.
type EditFileTool struct {
	gate    *corefs.Gate
	readSet *ReadSet
	enabled func() bool
}

// NewEditFileTool constructs the edit_file tool.
func NewEditFileTool(opts Options) *EditFileTool {
	return &EditFileTool{
		gate:    opts.Gate,
		readSet: opts.ReadSet,
		enabled: opts.WriteEnabled,
	}
}

func (t *EditFileTool) Name() string { return NameEditFile }

func (t *EditFileTool) Description() string {
	return "Replace an exact substring in a file with new text. " +
		"Requires a prior read_file call for the same path in the current session. " +
		"Returns an error if old_str appears more than once (ambiguous match) or not at all."
}

const editFileSchema = `{
  "type": "object",
  "properties": {
    "path": {
      "type": "string",
      "description": "Absolute or relative path to the file to edit."
    },
    "old_str": {
      "type": "string",
      "description": "Exact substring to find and replace. Must appear exactly once."
    },
    "new_str": {
      "type": "string",
      "description": "Replacement text."
    }
  },
  "required": ["path", "old_str", "new_str"]
}`

func (t *EditFileTool) InputSchema() json.RawMessage { return json.RawMessage(editFileSchema) }

type editFileArgs struct {
	Path   string `json:"path"`
	OldStr string `json:"old_str"`
	NewStr string `json:"new_str"`
}

type editFileResult struct {
	Written int64  `json:"written"`
	Path    string `json:"path"`
}

func (t *EditFileTool) Call(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if t.enabled != nil && !t.enabled() {
		return errResult("edit_file: FSWriteEnabled is off"), nil
	}
	var args editFileArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return errResult("edit_file: invalid args: " + err.Error()), nil
	}
	if args.Path == "" {
		return errResult("edit_file: path is required"), nil
	}

	// Validate path and check write permission.
	canonical, err := gateCheck(ctx, t.gate, corefs.OpWrite, args.Path)
	if err != nil {
		return errResult("edit_file: " + err.Error()), nil
	}

	// Enforce ErrEditWithoutRead.
	sessionID := sessionIDFromCtx(ctx)
	if t.readSet != nil && !t.readSet.Has(sessionID, canonical) {
		return errResult(ErrEditWithoutRead.Error()), nil
	}

	// Read current content.
	existing, err := os.ReadFile(canonical)
	if err != nil {
		return errResult("edit_file: read: " + err.Error()), nil
	}
	content := string(existing)

	// Count occurrences.
	count := strings.Count(content, args.OldStr)
	if count == 0 {
		return errResult("edit_file: old_str not found in file"), nil
	}
	if count > 1 {
		return errResult(fmt.Sprintf("edit_file: old_str appears %d times (must be unique)", count)), nil
	}

	// Apply replacement.
	newContent := strings.Replace(content, args.OldStr, args.NewStr, 1)

	// Preserve file mode.
	info, err := os.Stat(canonical)
	if err != nil {
		return errResult("edit_file: stat: " + err.Error()), nil
	}
	mode := info.Mode()

	if err := atomicWrite(canonical, []byte(newContent), mode); err != nil {
		return errResult("edit_file: " + err.Error()), nil
	}

	return okResult(editFileResult{Written: int64(len(newContent)), Path: canonical}), nil
}

// ─── list_open_worklist ─────────────────────────────────────────────────────

// ListOpenWorklistTool implements kaneaz__list_open_worklist.
// It scans a YAML/JSON worklist directory for pending items and returns
// them. When the worklist is empty it also returns a loop-refusal hint.
type ListOpenWorklistTool struct {
	worklistDir string
	enabled     func() bool
}

// NewListOpenWorklistTool constructs the list_open_worklist tool.
func NewListOpenWorklistTool(opts Options) *ListOpenWorklistTool {
	return &ListOpenWorklistTool{
		worklistDir: opts.WorklistDir,
		enabled:     opts.ReadEnabled,
	}
}

func (t *ListOpenWorklistTool) Name() string { return NameListOpenWorklist }

func (t *ListOpenWorklistTool) Description() string {
	return "List pending items in the session worklist. " +
		"Returns items with status 'open' or 'in_progress'. " +
		"When the worklist is empty, returns a loop-refusal hint — " +
		"the model MUST stop and ask the user for the next task rather than inventing work."
}

const listOpenWorklistSchema = `{
  "type": "object",
  "properties": {
    "filter_status": {
      "type": "string",
      "description": "Filter to items with this status (default: open and in_progress)."
    }
  }
}`

func (t *ListOpenWorklistTool) InputSchema() json.RawMessage {
	return json.RawMessage(listOpenWorklistSchema)
}

type worklistArgs struct {
	FilterStatus string `json:"filter_status"`
}

type worklistItem struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type worklistResult struct {
	Items       []worklistItem `json:"items"`
	LoopRefusal string         `json:"loop_refusal,omitempty"`
}

func (t *ListOpenWorklistTool) Call(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if t.enabled != nil && !t.enabled() {
		return errResult("list_open_worklist: FSReadEnabled is off"), nil
	}

	var args worklistArgs
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &args)
	}

	items, err := t.loadItems(ctx)
	if err != nil {
		return errResult("list_open_worklist: " + err.Error()), nil
	}

	// Filter.
	var filtered []worklistItem
	for _, item := range items {
		if args.FilterStatus != "" {
			if item.Status == args.FilterStatus {
				filtered = append(filtered, item)
			}
		} else {
			if item.Status == "open" || item.Status == "in_progress" {
				filtered = append(filtered, item)
			}
		}
	}

	result := worklistResult{Items: filtered}
	if len(filtered) == 0 {
		result.LoopRefusal = "The worklist is empty. Stop and ask the user for the next task — do not invent or assume additional work."
	}

	return okResult(result), nil
}

func (t *ListOpenWorklistTool) loadItems(_ context.Context) ([]worklistItem, error) {
	if t.worklistDir == "" {
		return nil, nil
	}

	entries, err := os.ReadDir(t.worklistDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var items []worklistItem
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(t.worklistDir, name))
		if readErr != nil {
			continue
		}
		var item worklistItem
		if strings.HasSuffix(name, ".json") {
			if err := json.Unmarshal(data, &item); err == nil {
				items = append(items, item)
			}
		}
		// YAML parsing would need a dependency; for now accept JSON only
		// and leave YAML as a no-op (documented limitation).
	}

	return items, nil
}

// compile-time BuiltinTool interface assertions.
var (
	_ toolloop.BuiltinTool = (*ReadFileTool)(nil)
	_ toolloop.BuiltinTool = (*ListDirTool)(nil)
	_ toolloop.BuiltinTool = (*GlobTool)(nil)
	_ toolloop.BuiltinTool = (*GrepTool)(nil)
	_ toolloop.BuiltinTool = (*WriteFileTool)(nil)
	_ toolloop.BuiltinTool = (*EditFileTool)(nil)
	_ toolloop.BuiltinTool = (*ListOpenWorklistTool)(nil)
)
