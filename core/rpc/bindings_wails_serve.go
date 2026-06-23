//go:build serve

// bindings_wails_serve.go provides stub replacements for the Wails-runtime-
// dependent binding methods when building cmd/harness-served (-tags serve).
//
// All methods return ErrNotSupportedInServeMode because serve mode runs in
// a headless VM with no Wails runtime and no OS-native GUI dialogs.

package rpc

import (
	"errors"

	"github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
)

// ErrNotSupportedInServeMode is returned by GUI-only binding methods
// (file-picker dialogs) when the harness is running in serve mode.
var ErrNotSupportedInServeMode = errors.New("rpc: not supported in serve mode (no Wails runtime)")

// Sessions_Export is not supported in serve mode — there is no OS file picker.
func (b *Bindings) Sessions_Export(_, _ string) (sessions.ExportResult, error) {
	return sessions.ExportResult{}, ErrNotSupportedInServeMode
}

// Artifacts_SaveAs is not supported in serve mode — there is no OS file picker.
func (b *Bindings) Artifacts_SaveAs(_, _ string) (string, error) {
	return "", ErrNotSupportedInServeMode
}

// Tools_PickDirectory is not supported in serve mode — there is no OS directory picker.
func (b *Bindings) Tools_PickDirectory(_, _ string) (string, error) {
	return "", ErrNotSupportedInServeMode
}

// Shell_PickFile is not supported in serve mode — there is no OS file picker.
func (b *Bindings) Shell_PickFile(_, _ string, _ []string) (string, error) {
	return "", ErrNotSupportedInServeMode
}
