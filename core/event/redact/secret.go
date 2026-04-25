package redact

import (
	"crypto/hmac"
	"crypto/sha256"
	"sync"
)

// CredentialReference stubs the secrets-keychain CredentialReference
// type so this mission can land before secrets-keychain. The real type
// will be replaced by core/secrets.CredentialReference; the contract
// here is the same: the salt is identified by an opaque keychain id
// and resolved into a Secret at process start.
type CredentialReference struct {
	Keychain string `json:"keychain"`
}

// Secret is a []byte-typed wrapper that zeroizes itself on Rotate.
// Callers should never hold a string copy of the underlying bytes.
type Secret struct {
	mu          sync.RWMutex
	bytes       []byte
	fingerprint string
}

// NewSecret constructs a Secret from raw bytes. The fingerprint is the
// first 12 base32-ish chars of HMAC-SHA-256(salt, "kaneaz-fingerprint").
// Used in RedactionSummary so audit can scope correlation queries to
// a salt window.
func NewSecret(salt []byte) *Secret {
	cp := make([]byte, len(salt))
	copy(cp, salt)
	mac := hmac.New(sha256.New, cp)
	mac.Write([]byte("kaneaz-fingerprint"))
	sum := mac.Sum(nil)
	fp := encodeFingerprint(sum)
	return &Secret{bytes: cp, fingerprint: fp}
}

// Bytes returns a defensive copy of the secret bytes.
func (s *Secret) Bytes() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make([]byte, len(s.bytes))
	copy(cp, s.bytes)
	return cp
}

// Fingerprint returns the salt fingerprint (safe to log).
func (s *Secret) Fingerprint() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.fingerprint
}

// Rotate replaces the underlying secret bytes and zeroizes the prior
// buffer.
func (s *Secret) Rotate(newSalt []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.bytes {
		s.bytes[i] = 0
	}
	cp := make([]byte, len(newSalt))
	copy(cp, newSalt)
	s.bytes = cp
	mac := hmac.New(sha256.New, cp)
	mac.Write([]byte("kaneaz-fingerprint"))
	s.fingerprint = encodeFingerprint(mac.Sum(nil))
}

// Resolver resolves a CredentialReference to a Secret. Stubbed for
// this mission; the real resolver lives in secrets-keychain.
type Resolver interface {
	Resolve(ref CredentialReference) (*Secret, error)
}

// StaticResolver is a test-friendly resolver backed by an in-memory map.
type StaticResolver struct {
	m map[string][]byte
}

// NewStaticResolver constructs a resolver from a map of keychain ids
// to salt bytes.
func NewStaticResolver(salts map[string][]byte) *StaticResolver {
	cp := make(map[string][]byte, len(salts))
	for k, v := range salts {
		c := make([]byte, len(v))
		copy(c, v)
		cp[k] = c
	}
	return &StaticResolver{m: cp}
}

// Resolve looks up a salt by keychain id.
func (r *StaticResolver) Resolve(ref CredentialReference) (*Secret, error) {
	salt, ok := r.m[ref.Keychain]
	if !ok {
		return nil, ErrSaltNotResolved
	}
	return NewSecret(salt), nil
}

func encodeFingerprint(sum []byte) string {
	const alphabet = "0123456789abcdefghjkmnpqrstvwxyz"
	out := make([]byte, 12)
	for i := 0; i < 12 && i < len(sum); i++ {
		out[i] = alphabet[sum[i]&0x1f]
	}
	return string(out)
}
