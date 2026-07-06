//go:build !serve

// bindings_wails.go contains the Wails-runtime-dependent binding methods
// extracted from bindings.go. These methods open OS-native dialogs via the
// Wails runtime and cannot run in serve mode (no Wails runtime in the
// headless VM binary).
//
// The serve-mode equivalents live in bindings_wails_serve.go and return
// ErrNotSupportedInServeMode.

package rpc

import (
	"context"
	"os"
	"strings"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
)

// Sessions_Export exports the session identified by sessionID to the
// local filesystem. format is "markdown" or "json". A native file-save
// dialog is opened via the Wails runtime so the user can choose the
// destination path; the suggested default filename is derived from the
// session title and today's date.
//
// Cedar gate: Action::"session.export" is checked before any work is
// done. On Cedar Deny the call returns an error, no file is written,
// and no audit event is emitted.
//
// Returns ExportResult{Path, ByteCount} on success. Returns an error
// when the user cancels the file-picker (ErrExportCancelled) or when
// Cedar denies the export.
//
// session-export-01NDFSEX05 WP02.
func (b *Bindings) Sessions_Export(sessionID, format string) (sessions.ExportResult, error) {
	// Wire the Wails-backed FilePicker before delegating. The Cedar gate
	// and audit emitter were already wired at boot time in API.Start() via
	// WithExportOpts; WithExportPicker sets only the picker so those fields
	// are not disturbed. The picker is created inline because it captures
	// b.ctx() which requires the Wails runtime context to be live (it is,
	// since OnStartup runs before any Wails-bound method is called).
	sessions.WithExportPicker(b.api.Sessions(), &wailsFilePickerAdapter{b: b})
	return b.api.Sessions().Export(b.ctx(), sessionID, format)
}

// wailsFilePickerAdapter adapts the Wails runtime's SaveFileDialog to
// the sessions.FilePicker interface. Created inline by Sessions_Export
// so the Bindings type doesn't accumulate permanent state.
type wailsFilePickerAdapter struct{ b *Bindings }

func (w *wailsFilePickerAdapter) PickSavePath(_ context.Context, title, defaultFilename string) (string, error) {
	if title == "" {
		title = "Export session"
	}
	return wruntime.SaveFileDialog(w.b.ctx(), wruntime.SaveDialogOptions{
		Title:           title,
		DefaultFilename: defaultFilename,
	})
}

// Artifacts_SaveAs opens the OS-native save-file dialog and writes
// the artifact's bytes to the user-chosen path. Returns the absolute
// path written, or empty string if the user cancels. The bytes are
// resolved server-side from the media store so the round-trip never
// re-base64-encodes on the JS side (which is fragile in some webviews
// for large or binary blobs).
func (b *Bindings) Artifacts_SaveAs(id, suggestedName string) (string, error) {
	// Pull the artifact + its bytes through the existing view path.
	withBytes, err := b.api.Artifacts().Get(b.ctx(), id)
	if err != nil {
		return "", err
	}
	if suggestedName == "" {
		suggestedName = withBytes.Artifact.Title
	}
	if suggestedName == "" {
		suggestedName = id
	}
	dest, err := wruntime.SaveFileDialog(b.ctx(), wruntime.SaveDialogOptions{
		Title:           "Save artifact",
		DefaultFilename: suggestedName,
	})
	if err != nil {
		return "", err
	}
	if dest == "" {
		// User cancelled; soft return.
		return "", nil
	}
	if err := os.WriteFile(dest, withBytes.Bytes, 0o644); err != nil {
		return "", err
	}
	return dest, nil
}

// Tools_PickDirectory opens the OS-native folder-picker dialog and
// returns the absolute path the user selected. Returns the empty
// string when the user cancels (Wails surfaces cancel as a no-error
// empty result). The default-directory hint nudges the dialog to a
// useful starting point — pass "" to let the OS decide. Used by the
// recipe-install modal's allowed_directories chip list so the user
// can pick a folder graphically instead of typing an absolute path.
func (b *Bindings) Tools_PickDirectory(title, defaultDir string) (string, error) {
	if title == "" {
		title = "Choose a directory"
	}
	return wruntime.OpenDirectoryDialog(b.ctx(), wruntime.OpenDialogOptions{
		Title:            title,
		DefaultDirectory: defaultDir,
	})
}

// Shell_PickFile opens the OS-native file-picker dialog and returns the
// absolute path of the file the user selected. Returns the empty string
// when the user cancels (Wails surfaces cancel as a no-error empty result).
//
// Parameters:
//   - title:      dialog window title; defaults to "Choose a file" if empty.
//   - defaultDir: starting directory hint; pass "" to let the OS decide.
//   - filters:    optional MIME / extension hints passed to the OS picker.
//     Each entry is "Display Name|*.ext1;*.ext2" (Wails format). Pass nil
//     for no filtering.
//
// Used by the AskUserQuestion dialog's "file" kind so the user can select a
// file path graphically (WP10) rather than typing the path manually.
func (b *Bindings) Shell_PickFile(title, defaultDir string, filters []string) (string, error) {
	if title == "" {
		title = "Choose a file"
	}
	var wailsFilters []wruntime.FileFilter
	for _, f := range filters {
		// Each filter string is "Display Name|*.ext1;*.ext2"
		pipe := strings.Index(f, "|")
		if pipe < 0 {
			wailsFilters = append(wailsFilters, wruntime.FileFilter{
				DisplayName: f,
				Pattern:     f,
			})
		} else {
			wailsFilters = append(wailsFilters, wruntime.FileFilter{
				DisplayName: f[:pipe],
				Pattern:     f[pipe+1:],
			})
		}
	}
	return wruntime.OpenFileDialog(b.ctx(), wruntime.OpenDialogOptions{
		Title:            title,
		DefaultDirectory: defaultDir,
		Filters:          wailsFilters,
	})
}
