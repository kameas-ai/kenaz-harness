package units

import (
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"
)

// defaultUnitID returns a 26-char Crockford-base32 ULID. We use a
// process-monotonic generator so two Creates in the same millisecond
// produce strictly increasing ids.
func defaultUnitID() (string, error) {
	return unitULIDGen.Next()
}

var unitULIDGen = newUnitULIDGenerator()

type unitULIDGenerator struct {
	mu     sync.Mutex
	lastMs uint64
	tail   [10]byte
}

const unitCrockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func newUnitULIDGenerator() *unitULIDGenerator { return &unitULIDGenerator{} }

func (g *unitULIDGenerator) Next() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ms := uint64(time.Now().UnixMilli())
	if ms < g.lastMs {
		ms = g.lastMs
	}
	if ms == g.lastMs {
		for i := len(g.tail) - 1; i >= 0; i-- {
			g.tail[i]++
			if g.tail[i] != 0 {
				break
			}
			if i == 0 {
				return "", errors.New("units: ULID tail overflow")
			}
		}
	} else {
		var t [10]byte
		if _, err := rand.Read(t[:]); err != nil {
			return "", fmt.Errorf("units: rand: %w", err)
		}
		g.tail = t
		g.lastMs = ms
	}
	return encodeUnitULID(g.lastMs, g.tail), nil
}

func encodeUnitULID(ms uint64, tail [10]byte) string {
	var raw [16]byte
	raw[0] = byte(ms >> 40)
	raw[1] = byte(ms >> 32)
	raw[2] = byte(ms >> 24)
	raw[3] = byte(ms >> 16)
	raw[4] = byte(ms >> 8)
	raw[5] = byte(ms)
	copy(raw[6:], tail[:])

	out := make([]byte, 26)
	out[0] = unitCrockford[(raw[0]>>5)&0x07]
	out[1] = unitCrockford[raw[0]&0x1f]
	out[2] = unitCrockford[(raw[1]>>3)&0x1f]
	out[3] = unitCrockford[((raw[1]&0x07)<<2)|((raw[2]>>6)&0x03)]
	out[4] = unitCrockford[(raw[2]>>1)&0x1f]
	out[5] = unitCrockford[((raw[2]&0x01)<<4)|((raw[3]>>4)&0x0f)]
	out[6] = unitCrockford[((raw[3]&0x0f)<<1)|((raw[4]>>7)&0x01)]
	out[7] = unitCrockford[(raw[4]>>2)&0x1f]
	out[8] = unitCrockford[((raw[4]&0x03)<<3)|((raw[5]>>5)&0x07)]
	out[9] = unitCrockford[raw[5]&0x1f]
	out[10] = unitCrockford[(raw[6]>>3)&0x1f]
	out[11] = unitCrockford[((raw[6]&0x07)<<2)|((raw[7]>>6)&0x03)]
	out[12] = unitCrockford[(raw[7]>>1)&0x1f]
	out[13] = unitCrockford[((raw[7]&0x01)<<4)|((raw[8]>>4)&0x0f)]
	out[14] = unitCrockford[((raw[8]&0x0f)<<1)|((raw[9]>>7)&0x01)]
	out[15] = unitCrockford[(raw[9]>>2)&0x1f]
	out[16] = unitCrockford[((raw[9]&0x03)<<3)|((raw[10]>>5)&0x07)]
	out[17] = unitCrockford[raw[10]&0x1f]
	out[18] = unitCrockford[(raw[11]>>3)&0x1f]
	out[19] = unitCrockford[((raw[11]&0x07)<<2)|((raw[12]>>6)&0x03)]
	out[20] = unitCrockford[(raw[12]>>1)&0x1f]
	out[21] = unitCrockford[((raw[12]&0x01)<<4)|((raw[13]>>4)&0x0f)]
	out[22] = unitCrockford[((raw[13]&0x0f)<<1)|((raw[14]>>7)&0x01)]
	out[23] = unitCrockford[(raw[14]>>2)&0x1f]
	out[24] = unitCrockford[((raw[14]&0x03)<<3)|((raw[15]>>5)&0x07)]
	out[25] = unitCrockford[raw[15]&0x1f]
	return string(out)
}
