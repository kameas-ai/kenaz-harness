// Package shell exposes OS-shell affordances to the harness frontend.
// v1 covers a single operation: opening an absolute filesystem path
// in the OS file browser via Wails's BrowserOpenURL with a file://
// URL. The view is intentionally thin — it validates the path exists
// (no point opening a "file not found" in Finder) and delegates the
// actual launch to runtime.BrowserOpenURL.
package shell

import "context"

// ShellAPI is the view-scoped surface for OS-shell operations.
// Implementations MUST be safe for concurrent use.
type ShellAPI interface {
	// OpenInOSBrowser opens path in the OS file browser. path must
	// be a real absolute path that exists; the implementation
	// rewrites it as file://<abs> and hands the URL to the
	// platform's default browser-open handler. A non-existent path
	// returns an error so the caller can surface "directory was
	// moved" rather than letting the OS surface a confusing dialog.
	OpenInOSBrowser(ctx context.Context, path string) error
}
