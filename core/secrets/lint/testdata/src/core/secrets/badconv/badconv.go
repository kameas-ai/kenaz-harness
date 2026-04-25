package badconv

// Conversion of a credential-shaped []byte to string outside of a test
// file must be flagged.

func leak() {
	secretBytes := []byte("hunter2-aaaaaaaaaaaaaaaaaaaaaa")
	_ = string(secretBytes) // want "secret-bytes: string\\(\\.\\.\\.\\) conversion of credential-shaped"

	tokenBytes := []byte("xyz-aaaaaaaaaaaaaaaaaaaa")
	_ = string(tokenBytes) // want "secret-bytes: string\\(\\.\\.\\.\\) conversion of credential-shaped"

	// Negative: name is not credential-shaped.
	regionBytes := []byte("us-east-1")
	_ = string(regionBytes)
}
