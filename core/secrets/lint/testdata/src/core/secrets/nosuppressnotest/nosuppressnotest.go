package nosuppressnotest

// The //nolint:secret-bytes escape hatch must NOT suppress diagnostics
// in non-test files. The diagnostic must still fire even when the
// directive is present.

type Config struct {
	APIKey string //nolint:secret-bytes legitimate justification // want "secret-bytes: credential-shaped field APIKey"
}
