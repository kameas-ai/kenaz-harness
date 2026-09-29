package advice

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// FeaturesHash derives the cache key's stable component from an
// extracted Features value via canonical JSON marshaling (deterministic
// for structs — Go's encoding/json emits struct fields in declaration
// order — and for maps, which encoding/json sorts by key). Kinds should
// still prefer a struct over a map for Features (see the Features doc
// comment) since a struct also gives PromptRenderer a stable, typed
// shape; FeaturesHash works either way. Mirrors
// risk.hashNormalizedArgs's rationale (fixed-size map keys regardless of
// payload length) but hashes the MARSHALED features rather than an
// already-normalized string, since a kind's Features is a Go value, not
// pre-serialized text.
//
// Returns an error (never a best-effort partial hash) when f cannot be
// marshaled — e.g. a kind's extractor accidentally returned a value
// containing a channel or a func — so a caller treats that exactly like
// every other Recommend failure mode: degrade to no advice, never guess.
func FeaturesHash(f Features) (string, error) {
	b, err := json.Marshal(f)
	if err != nil {
		return "", fmt.Errorf("advice: hashing features: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
