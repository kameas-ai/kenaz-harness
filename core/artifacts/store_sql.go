package artifacts

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/storage"
)

// SQLStoreOption configures the SQL-backed store at construction time.
type SQLStoreOption func(*unitsStore)

// WithSQLClock overrides the wall-clock source. Tests pin this for
// deterministic CreatedAt timestamps.
func WithSQLClock(now func() time.Time) SQLStoreOption {
	return func(s *unitsStore) {
		if now != nil {
			s.now = now
		}
	}
}

// WithSQLIDGen overrides the id generator.
func WithSQLIDGen(gen func() (string, error)) SQLStoreOption {
	return func(s *unitsStore) {
		if gen != nil {
			s.idGen = gen
		}
	}
}

// WithSessionProjectReader injects the session→project lookup used by
// UpdateScope to validate promotions. Optional: a nil reader is
// equivalent to "no session has a project", which means every
// promotion attempt fails ErrUnsupportedScope.
func WithSessionProjectReader(r SessionProjectReader) SQLStoreOption {
	return func(s *unitsStore) {
		s.sessReader = r
	}
}

// NewSQLStore constructs the SQL-backed Store against the unified
// storage.DB. Since migration units/1104-artifacts-to-units
// (artifacts-as-units-01DOGF0C WP04) artifacts live in the units tables
// as kind='artifact' rows, so this is the units-backed store; the legacy
// artifacts-table implementation was removed with the store switch — the
// tables it read are renamed *_legacy by that migration and nothing may
// write them.
func NewSQLStore(db storage.DB, opts ...SQLStoreOption) Store {
	return NewUnitsStore(db, opts...)
}

// ArtifactsRefcountSource exposes a Store as a RefcountSource for the
// attachments.MediaStore composite refcount. WP02 wires this via
// `media.RegisterRefcountSource(artifacts.ArtifactsRefcountSource{Store: store})`
// at chassis construction; WP01 ships only the type.
type ArtifactsRefcountSource struct {
	Store Store
}

// Refcount implements attachments.RefcountSource.
func (a ArtifactsRefcountSource) Refcount(ctx context.Context, contentHash string) (int, error) {
	if a.Store == nil {
		return 0, nil
	}
	return a.Store.RefcountFor(ctx, contentHash)
}

func validSource(s string) bool {
	switch s {
	case SourceCodeBlock, SourceToolOutput, SourceUserPin, SourceModelOutput:
		return true
	}
	return false
}

func validScope(s string) bool {
	switch s {
	case ScopeKindSession, ScopeKindProject, ScopeKindGlobal:
		return true
	}
	return false
}

// ── ID generator ───────────────────────────────────────────────────────

// defaultArtifactID returns a 26-char Crockford-base32 ULID. We use a
// process-monotonic generator so two Inserts in the same millisecond
// produce strictly increasing ids — useful for List ordering.
func defaultArtifactID() (string, error) {
	return artifactULIDGen.Next()
}

var artifactULIDGen = newArtifactULIDGenerator()

type artifactULIDGenerator struct {
	mu     sync.Mutex
	lastMs uint64
	tail   [10]byte
}

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func newArtifactULIDGenerator() *artifactULIDGenerator { return &artifactULIDGenerator{} }

func (g *artifactULIDGenerator) Next() (string, error) {
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
				return "", errors.New("artifacts: ULID tail overflow")
			}
		}
	} else {
		var t [10]byte
		if _, err := rand.Read(t[:]); err != nil {
			return "", fmt.Errorf("artifacts: rand: %w", err)
		}
		g.tail = t
		g.lastMs = ms
	}
	return encodeArtifactULID(g.lastMs, g.tail), nil
}

func encodeArtifactULID(ms uint64, tail [10]byte) string {
	var raw [16]byte
	raw[0] = byte(ms >> 40)
	raw[1] = byte(ms >> 32)
	raw[2] = byte(ms >> 24)
	raw[3] = byte(ms >> 16)
	raw[4] = byte(ms >> 8)
	raw[5] = byte(ms)
	copy(raw[6:], tail[:])

	out := make([]byte, 26)
	out[0] = crockford[(raw[0]>>5)&0x07]
	out[1] = crockford[raw[0]&0x1f]
	out[2] = crockford[(raw[1]>>3)&0x1f]
	out[3] = crockford[((raw[1]&0x07)<<2)|((raw[2]>>6)&0x03)]
	out[4] = crockford[(raw[2]>>1)&0x1f]
	out[5] = crockford[((raw[2]&0x01)<<4)|((raw[3]>>4)&0x0f)]
	out[6] = crockford[((raw[3]&0x0f)<<1)|((raw[4]>>7)&0x01)]
	out[7] = crockford[(raw[4]>>2)&0x1f]
	out[8] = crockford[((raw[4]&0x03)<<3)|((raw[5]>>5)&0x07)]
	out[9] = crockford[raw[5]&0x1f]
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
