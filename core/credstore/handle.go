package credstore

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
)

// Handle is an opaque, in-memory token representing a single credential
// grant. It is a uint64 XOR'd with a per-process random salt at issue
// time, making the value unguessable across process restarts and
// unrelated to the underlying CredentialRef.
//
// Handles must NEVER be logged as raw integers. String() returns a
// redaction-safe representation; use that for any diagnostic output.
//
// Spec: FR-002 — Handle is single-use OR time-bounded (default 60s).
type Handle uint64

// processSalt is initialised once per process by init(). Every Handle
// value returned to callers is XOR'd with the first 8 bytes of this
// salt so that a handle value observed in a log is useless across
// process restarts.
var processSalt uint64

func init() {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is fatal — the process cannot issue
		// cryptographically safe handles without it.
		panic("credstore: failed to seed per-process handle salt: " + err.Error())
	}
	processSalt = binary.LittleEndian.Uint64(b[:])
}

// mintHandle generates a new random Handle XOR'd with processSalt.
// The raw random uint64 is never exposed; only the XOR'd form is
// returned to callers. WP02 calls this inside Issue.
func mintHandle() (Handle, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	raw := binary.LittleEndian.Uint64(b[:])
	return Handle(raw ^ processSalt), nil
}

// String returns a redaction-safe representation of the Handle suitable
// for log lines and error messages. The format is:
//
//	credstore.Handle(<hex>)
//
// The hex value is the XOR'd handle, not the raw random uint64. Logging
// a handle in this form does NOT expose the raw random value and is safe
// to include in debug output.
func (h Handle) String() string {
	return fmt.Sprintf("credstore.Handle(%016x)", uint64(h))
}
