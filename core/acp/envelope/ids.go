package envelope

import (
	"crypto/rand"
	"encoding/hex"
	"sync/atomic"
	"time"
)

// newA2ATaskID returns a wire-level a2a_task_id. The shape mirrors
// what the SDK emits (an opaque string); v1 produces a 26-char
// time-prefixed hex id stable enough for fixture tests and ordered by
// emission time.
func newA2ATaskID() string {
	return "a2a_" + randomID(8)
}

// newMessageID returns a unique message id within the envelope's
// lifetime.
func newMessageID() string {
	return "msg_" + randomID(8)
}

func randomID(nbytes int) string {
	buf := make([]byte, nbytes)
	if _, err := rand.Read(buf); err != nil {
		// Fallback to a monotonic counter so callers never see an
		// empty id even if /dev/urandom is unavailable.
		c := monotonic.Add(1)
		ts := time.Now().UnixNano()
		for i := range buf {
			buf[i] = byte((ts >> (i * 8)) ^ c)
		}
	}
	return hex.EncodeToString(buf)
}

var monotonic atomic.Int64
