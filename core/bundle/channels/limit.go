package channels

import (
	"errors"
	"fmt"
	"io"
)

// ErrFetchTooLarge is returned (wrapped) by a CapWriter sink once a fetch
// delivers more bytes than its cap. Channel.Fetch has no size bound of
// its own — http_mirror deliberately has no wall-clock cap on a
// progressing body (engine-publication-01ENPUB01 WP-H4) — so every caller
// that buffers or stores a fetch MUST bound it by what it expects
// (engine-publication WP-H6, review F2).
var ErrFetchTooLarge = errors.New("channels: fetched content exceeds its size cap")

// Size caps for the small, fixed-shape objects every install fetches.
const (
	// MaxSignatureBytes bounds a detached signature (a raw ed25519 sig is
	// 64 bytes; this leaves room for any envelope scheme we might add).
	MaxSignatureBytes = 64 << 10
	// MaxManifestBytes bounds a bundle manifest (kenaz.yaml).
	MaxManifestBytes = 4 << 20
)

// CapWriter wraps w so that writing more than max bytes in total fails
// with ErrFetchTooLarge (nothing past the cap reaches w). Pass it as a
// Fetch sink: the failed Write aborts the channel's copy loop, which
// returns the error.
func CapWriter(w io.Writer, max int64) io.Writer {
	return &capWriter{w: w, remaining: max, max: max}
}

type capWriter struct {
	w         io.Writer
	remaining int64
	max       int64
}

func (c *capWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > c.remaining {
		n, err := c.w.Write(p[:c.remaining])
		c.remaining -= int64(n)
		if err != nil {
			return n, err
		}
		return n, fmt.Errorf("%w (limit %d bytes)", ErrFetchTooLarge, c.max)
	}
	n, err := c.w.Write(p)
	c.remaining -= int64(n)
	return n, err
}
