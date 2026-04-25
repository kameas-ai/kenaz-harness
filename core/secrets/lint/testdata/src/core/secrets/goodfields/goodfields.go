package goodfields

// Negative cases: credential-shaped fields whose type is []byte must
// produce no diagnostic.

type Config struct {
	APIKey     []byte
	Token      []byte
	Secret     []byte
	Password   []byte
	Credential []byte

	Name   string // OK: not credential-shaped
	Region string // OK
}
