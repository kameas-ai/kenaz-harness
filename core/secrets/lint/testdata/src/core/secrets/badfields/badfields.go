package badfields

// Each `// want` comment is the analysistest harness's expectation for a
// diagnostic on the same line. Strings are regexp patterns.

type Config struct {
	APIKey      string // want "secret-bytes: credential-shaped field APIKey"
	Token       string // want "secret-bytes: credential-shaped field Token"
	Secret      string // want "secret-bytes: credential-shaped field Secret"
	Password    string // want "secret-bytes: credential-shaped field Password"
	Credential  string // want "secret-bytes: credential-shaped field Credential"
	AnthropicKey string // want "secret-bytes: credential-shaped field AnthropicKey"

	Name   string // OK: not a credential-shaped name
	Region string // OK
}
