// Package shell exposes OS-shell affordances to the harness frontend.
// v1 covers a single operation: opening an absolute filesystem path
// in the OS file browser via Wails's BrowserOpenURL with a file://
// URL. The view is intentionally thin — it validates the path exists
// (no point opening a "file not found" in Finder) and delegates the
// actual launch to runtime.BrowserOpenURL.
//
// multimodal-io WP04 extends the surface with PathComplete + ReadFile
// to back the chat-input @filepath shortcut. Both honor the
// deny-list rooted in core/rpc/views/tools.ValidateAllowedDir so the
// chat surface can never autocomplete or read /etc/passwd.
package shell

import "context"

// PathCompletionLimit caps the number of completions PathComplete
// returns to keep the autocomplete dropdown responsive even when the
// user typed a path matching every entry under /tmp.
const PathCompletionLimit = 32

// MaxImageOrPDFBytes is the per-file cap ReadFile enforces for
// image / PDF media types (mirrors the frontend WP04 paperclip cap).
const MaxImageOrPDFBytes int64 = 20 * 1024 * 1024

// MaxTextSnapshotBytes is the per-file cap ReadFile enforces for
// non-multimodal text snapshots (mirrors the ContextPreview snapshot
// cap on the frontend).
const MaxTextSnapshotBytes int64 = 1 * 1024 * 1024

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

	// PathComplete resolves up to PathCompletionLimit completions for
	// the supplied partial path. The partial may be relative to the
	// user's home (~/...) or absolute. Implementations MUST honour
	// the same deny-list as ValidateAllowedDir — completions whose
	// canonical form lands under a denied root are filtered out.
	// Empty partial returns an empty list (rather than every entry
	// in the user's home directory).
	PathComplete(ctx context.Context, partial string) ([]string, error)

	// ReadFile reads a single file and returns its bytes plus the
	// detected MIME type. The path must clear the same deny-list as
	// ValidateAllowedDir; size is capped at MaxImageOrPDFBytes for
	// image/* and application/pdf, and MaxTextSnapshotBytes
	// otherwise. Returning the bytes on the wire is intentional — the
	// caller is the chat surface, which immediately base64-encodes the
	// payload and forwards it to Attachments_AddMedia or to a text
	// snapshot attachment.
	ReadFile(ctx context.Context, path string) (data []byte, mediaType string, err error)
}
