package shell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

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

// Compile-time witness.
var _ ShellAPI = (*API)(nil)
