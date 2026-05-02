package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/tools"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Opener is the runtime hook OpenInOSBrowser uses to dispatch the
// file:// URL. Production wires Wails's runtime.BrowserOpenURL; tests
// wire a recorder that captures the URL without spawning a window.
type Opener func(ctx context.Context, url string)

// API is the concrete ShellAPI implementation. The Wails app context
// (captured in main.go's OnStartup callback and threaded through
// rpc.API.SetContext) is held on the value because Wails's
// runtime.BrowserOpenURL refuses any other context — passing
// context.Background crashes with "An invalid context was passed".
type API struct {
	mu     sync.RWMutex
	ctx    context.Context
	opener Opener
}

// New constructs a ShellAPI. Pass nil for opener to use the default
// wails runtime.BrowserOpenURL; tests pass a recorder.
func New(opener Opener) *API {
	if opener == nil {
		opener = runtime.BrowserOpenURL
	}
	return &API{opener: opener}
}

// SetContext threads the Wails OnStartup-supplied context to the
// view so BrowserOpenURL dispatches correctly. main.go calls this
// once via rpc.API.SetContext on app startup. Pre-startup calls (or
// the test path that constructs the view standalone) return an
// error from OpenInOSBrowser rather than crashing the runtime.
func (a *API) SetContext(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
}

// OpenInOSBrowser validates path, normalises it to an absolute
// path, and hands a file:// URL to the configured opener.
func (a *API) OpenInOSBrowser(_ context.Context, path string) error {
	if path == "" {
		return errors.New("shell.open: empty path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("shell.open: abs %q: %w", path, err)
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("shell.open: path does not exist: %w", err)
	}
	a.mu.RLock()
	wailsCtx := a.ctx
	opener := a.opener
	a.mu.RUnlock()
	if wailsCtx == nil {
		return errors.New("shell.open: Wails context not yet wired")
	}
	if opener == nil {
		return errors.New("shell.open: no opener configured")
	}
	url := "file://" + abs
	opener(wailsCtx, url)
	return nil
}

// expandTilde expands a leading ~/ to the user's home directory.
// Returns the input unchanged when no expansion is needed.
func expandTilde(p string) string {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// isAllowed checks the path against the same deny-list rules as
// tools.ValidateAllowedDir. We can't call ValidateAllowedDir directly
// because it requires the path to exist + be a directory; here we
// want to filter completion candidates that may be files OR may not
// exist yet. Reusing tools' canonical-form deny-list keeps the
// security guarantee.
func isAllowed(abs string) bool {
	// Check the lexical path first — a path that syntactically lives
	// under a denied prefix (e.g. /etc/resolv.conf on Linux, where the
	// file is a symlink to /run/systemd/...) must stay denied even when
	// EvalSymlinks resolves it outside the deny-list.
	if tools.IsDeniedPath(filepath.Clean(abs)) {
		return false
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// Pre-existing path failed to canonicalise: treat as denied.
		canonical = abs
	}
	return !tools.IsDeniedPath(canonical)
}

// PathComplete resolves up to PathCompletionLimit completions for
// partial. The partial is split into (parentDir, prefix); we list
// parentDir and return entries whose name starts with prefix and
// whose canonical form clears the deny-list.
func (a *API) PathComplete(_ context.Context, partial string) ([]string, error) {
	if partial == "" {
		return nil, nil
	}
	expanded := expandTilde(partial)

	if !filepath.IsAbs(expanded) {
		abs, err := filepath.Abs(expanded)
		if err != nil {
			return nil, fmt.Errorf("shell.pathComplete: abs %q: %w", partial, err)
		}
		expanded = abs
	}

	// If the partial ends with a separator, list the directory itself.
	parent := expanded
	prefix := ""
	if !strings.HasSuffix(partial, string(filepath.Separator)) {
		parent = filepath.Dir(expanded)
		prefix = filepath.Base(expanded)
	}

	info, err := os.Stat(parent)
	if err != nil {
		// Non-existent parent: empty result, not an error — the user
		// is mid-typing.
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("shell.pathComplete: stat %q: %w", parent, err)
	}
	if !info.IsDir() {
		return nil, nil
	}

	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil, fmt.Errorf("shell.pathComplete: readdir %q: %w", parent, err)
	}

	out := make([]string, 0, PathCompletionLimit)
	for _, e := range entries {
		name := e.Name()
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}
		// Hide chassis-internal dotfiles unless the user is explicitly
		// looking inside a dot directory.
		if prefix == "" && strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(parent, name)
		if !isAllowed(full) {
			continue
		}
		// Re-attach a trailing separator on directories so the
		// frontend can render a folder hint.
		if e.IsDir() {
			full += string(filepath.Separator)
		}
		out = append(out, full)
		if len(out) >= PathCompletionLimit {
			break
		}
	}
	sort.Strings(out)
	return out, nil
}

// ReadFile reads path, validates the canonical form against the
// deny-list, detects the MIME type, enforces size caps, and returns
// the bytes + media type to the caller.
func (a *API) ReadFile(_ context.Context, path string) ([]byte, string, error) {
	if path == "" {
		return nil, "", errors.New("shell.readFile: empty path")
	}
	expanded := expandTilde(path)
	if !filepath.IsAbs(expanded) {
		abs, err := filepath.Abs(expanded)
		if err != nil {
			return nil, "", fmt.Errorf("shell.readFile: abs %q: %w", path, err)
		}
		expanded = abs
	}
	info, err := os.Stat(expanded)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", fmt.Errorf("shell.readFile: %w", os.ErrNotExist)
		}
		return nil, "", fmt.Errorf("shell.readFile: stat %q: %w", expanded, err)
	}
	if info.IsDir() {
		return nil, "", fmt.Errorf("shell.readFile: %q is a directory", expanded)
	}
	if !isAllowed(expanded) {
		return nil, "", fmt.Errorf("shell.readFile: %w: %q", tools.ErrPathInDenyList, expanded)
	}

	mediaType := mimeFromExt(filepath.Ext(expanded))

	cap := MaxTextSnapshotBytes
	if mediaType == "application/pdf" || strings.HasPrefix(mediaType, "image/") {
		cap = MaxImageOrPDFBytes
	}
	if info.Size() > cap {
		return nil, "", fmt.Errorf("shell.readFile: %d bytes exceeds %d cap", info.Size(), cap)
	}

	f, err := os.Open(expanded)
	if err != nil {
		return nil, "", fmt.Errorf("shell.readFile: open %q: %w", expanded, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, cap+1))
	if err != nil {
		return nil, "", fmt.Errorf("shell.readFile: read %q: %w", expanded, err)
	}
	if int64(len(data)) > cap {
		return nil, "", fmt.Errorf("shell.readFile: payload exceeds %d cap", cap)
	}

	if mediaType == "" {
		end := len(data)
		if end > 512 {
			end = 512
		}
		mediaType = http.DetectContentType(data[:end])
	}
	return data, mediaType, nil
}

// mimeFromExt returns a stable MIME type for the well-known
// extensions the chat surface accepts. Unknown extensions return
// empty string and let http.DetectContentType take a swing.
func mimeFromExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".pdf":
		return "application/pdf"
	case ".json":
		return "application/json"
	case ".md":
		return "text/markdown"
	case ".txt":
		return "text/plain"
	case ".go", ".py", ".js", ".ts", ".vue", ".sh", ".yaml", ".yml", ".toml":
		return "text/plain"
	}
	return ""
}

// Compile-time witness.
var _ ShellAPI = (*API)(nil)
