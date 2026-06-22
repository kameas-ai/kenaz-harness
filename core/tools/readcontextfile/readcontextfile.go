// Package readcontextfile implements the kaneaz__read_context_file
// built-in tool. It lets the agent read a non-root, non-always file
// from an attached context module on demand.
//
// Security constraints (NFR-003):
//   - The requested path must be relative and must not escape the
//     library root (no ".." traversal, no absolute paths).
//   - The file must reside within an attached module directory (the
//     implementation receives the set of attached module directories
//     so requests outside them are rejected).
//   - File size is bounded to MaxFileBytes (1 MiB, same as the library).
//
// Tool name: kaneaz__read_context_file  (uses the "kaneaz__" built-in
// prefix so the toolloop can route by prefix without a registry lookup).
package readcontextfile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/contexts"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

const (
	// ToolName is the namespaced identifier surfaced to the model.
	ToolName = "kaneaz__read_context_file"

	// ToolDescription explains when the agent should use this tool.
	// The phrasing intentionally echoes AGENTS.md conventions so models
	// already familiar with that pattern map onto it naturally.
	ToolDescription = "Read a file from an attached context module. " +
		"Use this when the module's root file (context.md) references " +
		"another file you need to understand the full context. " +
		"The path must be relative to the library root and must point " +
		"to a file inside an attached module directory."
)

// inputSchema is the JSON Schema for the tool's arguments. Only `path`
// is required; `module_hint` is optional and helps the model express
// intent (ignored at dispatch time — the path must be self-sufficient).
const inputSchema = `{
  "type": "object",
  "properties": {
    "path": {
      "type": "string",
      "description": "Library-relative path of the file to read (e.g. \"my-module/conventions.md\"). Must be inside an attached module directory."
    }
  },
  "required": ["path"]
}`

// LibraryReader is the narrow interface the tool needs from the Context
// Library. core/contexts.Library satisfies it; tests can pass a fake.
type LibraryReader interface {
	// Root returns the absolute path of the library root.
	Root() string
	// Get reads a file and returns its contents.
	Get(path string) (string, error)
}

// ModuleSource provides the set of currently attached module directories
// so the tool can enforce that the requested path is inside one. The
// resolver is called on each invocation so the set reflects live
// attachment state.
type ModuleSource interface {
	// AttachedModuleDirs returns the set of library-relative directory
	// paths that are currently active as context modules (i.e. have a
	// context.md / agents.md root file). An empty slice means "no module
	// constraints" — the tool rejects all requests in that case.
	AttachedModuleDirs(ctx context.Context, sessionID string) ([]string, error)
}

// Options configures a Tool.
type Options struct {
	// Library is required — the tool reads from it.
	Library LibraryReader
	// Modules supplies the currently attached module directories. When
	// nil the tool rejects all requests (safe default: no library wired
	// means no on-demand reads should succeed).
	Modules ModuleSource
	// SessionResolver derives the session ID from the request context.
	// Defaults to toolloop.SessionIDFromContext.
	SessionResolver func(ctx context.Context) string
}

// Tool implements the kaneaz__read_context_file built-in.
// Safe for concurrent use after construction.
type Tool struct {
	lib      LibraryReader
	modules  ModuleSource
	resolver func(ctx context.Context) string
}

// New constructs a Tool. Library is required; Modules may be nil (all
// requests will be rejected gracefully).
func New(opts Options) *Tool {
	resolver := opts.SessionResolver
	if resolver == nil {
		resolver = toolloop.SessionIDFromContext
	}
	return &Tool{
		lib:      opts.Library,
		modules:  opts.Modules,
		resolver: resolver,
	}
}

// Name implements toolloop.BuiltinTool.
func (t *Tool) Name() string { return ToolName }

// Description implements toolloop.BuiltinTool.
func (t *Tool) Description() string { return ToolDescription }

// InputSchema implements toolloop.BuiltinTool.
func (t *Tool) InputSchema() json.RawMessage { return json.RawMessage(inputSchema) }

// args is the decoded argument shape.
type args struct {
	Path string `json:"path"`
}

type successResult struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type errorResult struct {
	Error string `json:"error"`
}

// Call implements toolloop.BuiltinTool. It validates the path, confirms
// it belongs to an attached module, then returns the file's content.
func (t *Tool) Call(ctx context.Context, rawArgs json.RawMessage) (json.RawMessage, error) {
	if t == nil || t.lib == nil {
		return marshalErr("library_unavailable",
			"read_context_file: context library is not configured")
	}

	var a args
	if err := json.Unmarshal(rawArgs, &a); err != nil {
		return marshalErr("invalid_args",
			fmt.Sprintf("read_context_file: invalid arguments: %v", err))
	}
	if strings.TrimSpace(a.Path) == "" {
		return marshalErr("invalid_args", "read_context_file: path is required")
	}

	// --- Path-confinement check ---
	// Reject absolute paths and traversal before touching the library.
	if filepath.IsAbs(a.Path) || strings.Contains(a.Path, "..") {
		return marshalErr("path_traversal",
			"read_context_file: path must be relative and must not contain \"..\"")
	}

	// --- Module-membership check ---
	// The requested path must sit inside one of the currently attached
	// module directories.
	if err := t.confirmInAttachedModule(ctx, a.Path); err != nil {
		return marshalErr("not_in_module", err.Error())
	}

	// --- Read ---
	content, err := t.lib.Get(a.Path)
	if err != nil {
		if isNotFound(err) {
			return marshalErr("not_found",
				fmt.Sprintf("read_context_file: %q not found in library", a.Path))
		}
		if isFileTooLarge(err) {
			return marshalErr("file_too_large",
				fmt.Sprintf("read_context_file: %q exceeds %d byte limit", a.Path, contexts.MaxFileBytes))
		}
		return marshalErr("read_error",
			fmt.Sprintf("read_context_file: read failed: %v", err))
	}

	// Bound the output to MaxFileBytes as a defence-in-depth against
	// a file growing between the stat check in Get and the read.
	if int64(len(content)) > contexts.MaxFileBytes {
		return marshalErr("file_too_large",
			fmt.Sprintf("read_context_file: %q content exceeds %d bytes", a.Path, contexts.MaxFileBytes))
	}

	out, _ := json.Marshal(successResult{Path: a.Path, Content: content})
	return out, nil
}

// confirmInAttachedModule checks that reqPath is a library-relative
// path that sits inside one of the attached module directories.
func (t *Tool) confirmInAttachedModule(ctx context.Context, reqPath string) error {
	if t.modules == nil {
		return fmt.Errorf("read_context_file: no module source configured; cannot verify module membership for %q", reqPath)
	}
	sessionID := t.resolver(ctx)
	dirs, err := t.modules.AttachedModuleDirs(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("read_context_file: failed to enumerate attached modules: %w", err)
	}
	if len(dirs) == 0 {
		return fmt.Errorf("read_context_file: no module directories are attached; %q is not accessible", reqPath)
	}
	// Normalise the requested path to slash-separated form.
	cleanReq := filepath.ToSlash(filepath.Clean(reqPath))
	for _, dir := range dirs {
		// dir is a library-relative module directory (e.g. "my-module").
		prefix := filepath.ToSlash(filepath.Clean(dir))
		if prefix == "" || prefix == "." {
			// Library-root module: every path is within it.
			return nil
		}
		if strings.HasPrefix(cleanReq, prefix+"/") {
			return nil
		}
	}
	return fmt.Errorf("read_context_file: %q is not inside any attached module directory (attached: %v)", reqPath, dirs)
}

// isNotFound inspects errors from the Library for a not-found signal.
// Uses the typed sentinel (errors.Is) rather than string-matching so the
// check is stable across library refactors and can't misclassify an
// unrelated error whose message happens to contain "not found".
func isNotFound(err error) bool {
	return errors.Is(err, contexts.ErrNotFound) || os.IsNotExist(err)
}

// isFileTooLarge detects the library's size-cap error via its sentinel.
func isFileTooLarge(err error) bool {
	return errors.Is(err, contexts.ErrFileTooLarge)
}

func marshalErr(kind, message string) (json.RawMessage, error) {
	out, _ := json.Marshal(errorResult{Error: fmt.Sprintf("[%s] %s", kind, message)})
	return out, nil
}

// Compile-time witness that Tool satisfies BuiltinTool.
var _ toolloop.BuiltinTool = (*Tool)(nil)
