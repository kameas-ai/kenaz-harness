//go:build serve

// impl_new_serve.go provides a CGO-free / Wails-free constructor for
// shell.API when building in serve mode (cmd/harness-served). The
// default opener is a no-op because there is no Wails runtime in the
// headless VM binary.

package shell

// New constructs a ShellAPI. In serve mode the default opener is a no-op
// (OpenInOSBrowser will return an error when no Wails context is set);
// pass a non-nil opener to override.
func New(opener Opener) *API {
	return &API{opener: opener}
}
