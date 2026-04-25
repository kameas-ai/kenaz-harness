package otherpkg

// The analyzer must not fire on packages outside core/secrets/.

type Config struct {
	APIKey string // OK: outside the secrets tree, no diagnostic
	Token  string // OK
}
