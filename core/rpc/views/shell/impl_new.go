//go:build !serve

// impl_new.go wires the Wails runtime.BrowserOpenURL as the default
// opener for the shell.API constructor. The serve-mode equivalent
// (impl_new_serve.go) uses a no-op opener instead.

package shell

import "github.com/wailsapp/wails/v2/pkg/runtime"

// New constructs a ShellAPI. Pass nil for opener to use the default
// Wails runtime.BrowserOpenURL; tests pass a recorder.
func New(opener Opener) *API {
	if opener == nil {
		opener = runtime.BrowserOpenURL
	}
	return &API{opener: opener}
}
