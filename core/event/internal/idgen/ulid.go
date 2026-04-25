// Package idgen produces process-monotonic ULID-shaped event ids.
//
// Format: 26-character Crockford-base32, the canonical ULID rendering.
// Strict monotonicity is guaranteed within a single process even when
// the wall clock jitters: we hold the previous timestamp + 80-bit
// random tail and increment the tail when timestamps repeat. If the
// wall clock goes backward we hold the timestamp at the previous
// floor and continue incrementing the tail.
//
// Exposed only via constructors in core/event so callers cannot forge
// ids (DIRECTIVE_001).
package idgen

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sync"
	"time"
)

// crockford is the Crockford-base32 alphabet used by the ULID spec.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Generator is a thread-safe, monotonic ULID generator.
type Generator struct {
	mu       sync.Mutex
	lastMs   uint64
	lastTail [10]byte
}

// New returns a fresh Generator seeded from crypto/rand.
func New() *Generator { return &Generator{} }

// Default is the package-level singleton used by the public API
// constructors. Tests that need a deterministic generator can build
// their own.
var Default = New()

// NextWith generates the next id at the supplied wall-clock time.
// Exposed for tests that need to inject time.
func (g *Generator) NextWith(now time.Time) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	ms := uint64(now.UnixMilli())
	if ms < g.lastMs {
		// Wall clock jitter / backward move: hold last floor.
		ms = g.lastMs
	}

	if ms == g.lastMs {
		// Same millisecond: increment the 80-bit tail by one.
		if err := incrementTail(&g.lastTail); err != nil {
			return "", err
		}
	} else {
		var t [10]byte
		if _, err := rand.Read(t[:]); err != nil {
			return "", fmt.Errorf("idgen: rand: %w", err)
		}
		g.lastTail = t
		g.lastMs = ms
	}

	return encode(g.lastMs, g.lastTail), nil
}

// Next generates the next id at the current wall-clock time.
func (g *Generator) Next() (string, error) {
	return g.NextWith(time.Now())
}

// Next is the package-level convenience for the singleton generator.
func Next() (string, error) { return Default.Next() }

// incrementTail adds one to a 10-byte big-endian counter. Returns
// an error if all bits would overflow within a single millisecond
// (astronomically unlikely; we surface it rather than silently
// reset).
func incrementTail(t *[10]byte) error {
	for i := len(t) - 1; i >= 0; i-- {
		t[i]++
		if t[i] != 0 {
			return nil
		}
	}
	return fmt.Errorf("idgen: tail overflow within one millisecond")
}

// encode renders ms (48-bit) + tail (80-bit) as 26-char Crockford-base32.
// The standard ULID layout is:
//   tttt tttt tttt | rrrr rrrr rrrr rrrr rrrr   (48 bits time, 80 bits random)
//   = 10 chars time | 16 chars random           (each char = 5 bits)
func encode(ms uint64, tail [10]byte) string {
	var raw [16]byte
	// Big-endian 48-bit timestamp in the top 6 bytes.
	raw[0] = byte(ms >> 40)
	raw[1] = byte(ms >> 32)
	raw[2] = byte(ms >> 24)
	raw[3] = byte(ms >> 16)
	raw[4] = byte(ms >> 8)
	raw[5] = byte(ms)
	copy(raw[6:], tail[:])

	// 16 bytes = 128 bits = 25.6 chars; ULID uses 26 chars by
	// padding the high nibble with zero bits (i.e. the first
	// character only encodes the top 3 bits of the timestamp).
	out := make([]byte, 26)

	// First char: bits 47..45 of ms (only 3 bits used).
	out[0] = crockford[(raw[0]>>5)&0x07]
	// Next 9 chars cover the remaining 45 bits of the timestamp.
	out[1] = crockford[raw[0]&0x1f]
	out[2] = crockford[(raw[1]>>3)&0x1f]
	out[3] = crockford[((raw[1]&0x07)<<2)|((raw[2]>>6)&0x03)]
	out[4] = crockford[(raw[2]>>1)&0x1f]
	out[5] = crockford[((raw[2]&0x01)<<4)|((raw[3]>>4)&0x0f)]
	out[6] = crockford[((raw[3]&0x0f)<<1)|((raw[4]>>7)&0x01)]
	out[7] = crockford[(raw[4]>>2)&0x1f]
	out[8] = crockford[((raw[4]&0x03)<<3)|((raw[5]>>5)&0x07)]
	out[9] = crockford[raw[5]&0x1f]
	// 16 chars cover the 80-bit tail (5 bits per char).
	out[10] = crockford[(raw[6]>>3)&0x1f]
	out[11] = crockford[((raw[6]&0x07)<<2)|((raw[7]>>6)&0x03)]
	out[12] = crockford[(raw[7]>>1)&0x1f]
	out[13] = crockford[((raw[7]&0x01)<<4)|((raw[8]>>4)&0x0f)]
	out[14] = crockford[((raw[8]&0x0f)<<1)|((raw[9]>>7)&0x01)]
	out[15] = crockford[(raw[9]>>2)&0x1f]
	out[16] = crockford[((raw[9]&0x03)<<3)|((raw[10]>>5)&0x07)]
	out[17] = crockford[raw[10]&0x1f]
	out[18] = crockford[(raw[11]>>3)&0x1f]
	out[19] = crockford[((raw[11]&0x07)<<2)|((raw[12]>>6)&0x03)]
	out[20] = crockford[(raw[12]>>1)&0x1f]
	out[21] = crockford[((raw[12]&0x01)<<4)|((raw[13]>>4)&0x0f)]
	out[22] = crockford[((raw[13]&0x0f)<<1)|((raw[14]>>7)&0x01)]
	out[23] = crockford[(raw[14]>>2)&0x1f]
	out[24] = crockford[((raw[14]&0x03)<<3)|((raw[15]>>5)&0x07)]
	out[25] = crockford[raw[15]&0x1f]

	return string(out)
}

// Parse validates a Crockford-base32 ULID string and returns its 16
// raw bytes. Used by Verify and tests.
func Parse(s string) ([16]byte, error) {
	var out [16]byte
	if len(s) != 26 {
		return out, fmt.Errorf("idgen: expected 26 chars, got %d", len(s))
	}
	// Build a decode table.
	var dec [256]int8
	for i := range dec {
		dec[i] = -1
	}
	for i, c := range []byte(crockford) {
		dec[c] = int8(i)
		// Accept lower-case too (Crockford convention).
		if c >= 'A' && c <= 'Z' {
			dec[c+('a'-'A')] = int8(i)
		}
	}
	// Decode 26 base-32 chars into 130 bits, take the bottom 128.
	var bits [26]int8
	for i := 0; i < 26; i++ {
		v := dec[s[i]]
		if v < 0 {
			return out, fmt.Errorf("idgen: invalid char %q at %d", s[i], i)
		}
		bits[i] = v
	}
	// First char only contributes 3 bits.
	if bits[0] > 7 {
		return out, fmt.Errorf("idgen: first char overflows ULID range")
	}
	// Pack big-endian.
	out[0] = byte(bits[0])<<5 | byte(bits[1])
	out[1] = byte(bits[2])<<3 | byte(bits[3])>>2
	out[2] = byte(bits[3])<<6 | byte(bits[4])<<1 | byte(bits[5])>>4
	out[3] = byte(bits[5])<<4 | byte(bits[6])>>1
	out[4] = byte(bits[6])<<7 | byte(bits[7])<<2 | byte(bits[8])>>3
	out[5] = byte(bits[8])<<5 | byte(bits[9])
	out[6] = byte(bits[10])<<3 | byte(bits[11])>>2
	out[7] = byte(bits[11])<<6 | byte(bits[12])<<1 | byte(bits[13])>>4
	out[8] = byte(bits[13])<<4 | byte(bits[14])>>1
	out[9] = byte(bits[14])<<7 | byte(bits[15])<<2 | byte(bits[16])>>3
	out[10] = byte(bits[16])<<5 | byte(bits[17])
	out[11] = byte(bits[18])<<3 | byte(bits[19])>>2
	out[12] = byte(bits[19])<<6 | byte(bits[20])<<1 | byte(bits[21])>>4
	out[13] = byte(bits[21])<<4 | byte(bits[22])>>1
	out[14] = byte(bits[22])<<7 | byte(bits[23])<<2 | byte(bits[24])>>3
	out[15] = byte(bits[24])<<5 | byte(bits[25])
	return out, nil
}

// TimestampMillis extracts the timestamp from a parsed ULID's raw
// bytes. Useful for retention and time-range queries.
func TimestampMillis(raw [16]byte) uint64 {
	return uint64(raw[0])<<40 | uint64(raw[1])<<32 | uint64(raw[2])<<24 |
		uint64(raw[3])<<16 | uint64(raw[4])<<8 | uint64(raw[5])
}

// Compare returns -1/0/+1 by lexicographic order, equivalent to
// monotonic order for ULIDs.
func Compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// _ keeps binary referenced; some platforms strip otherwise unused
// imports during link.
var _ = binary.BigEndian
