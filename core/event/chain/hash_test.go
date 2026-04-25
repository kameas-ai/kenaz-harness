package chain

import (
	"bytes"
	"testing"
)

func TestPayloadHashDeterministic(t *testing.T) {
	a := PayloadHash([]byte(`{"x":1}`))
	b := PayloadHash([]byte(`{"x":1}`))
	if a != b {
		t.Fatalf("hash should be deterministic")
	}
}

func TestPayloadHashDifferentiates(t *testing.T) {
	a := PayloadHash([]byte(`{"x":1}`))
	b := PayloadHash([]byte(`{"x":2}`))
	if a == b {
		t.Fatalf("different inputs must produce different hashes")
	}
}

func TestZeroHashAllZero(t *testing.T) {
	var want [32]byte
	if !bytes.Equal(ZeroHash[:], want[:]) {
		t.Fatalf("ZeroHash must be all zero bytes")
	}
}

func TestLinkHashPassthrough(t *testing.T) {
	prev := PayloadHash([]byte(`{"a":1}`))
	cur := PayloadHash([]byte(`{"b":2}`))
	got := LinkHash(prev, cur)
	if got != prev {
		t.Fatalf("LinkHash should currently passthrough prev (schema = prev_hash references prior payload_hash)")
	}
}

// Round-trip: canonicalize same logical value two different ways,
// hash, compare. Documents the byte-stability guarantee NFR-005 leans on.
func TestCanonicalThenHashRoundTrip(t *testing.T) {
	v1 := map[string]any{"a": 1, "b": []any{1, 2, 3}, "c": map[string]any{"x": "y"}}
	v2 := map[string]any{"c": map[string]any{"x": "y"}, "b": []any{1, 2, 3}, "a": 1}
	c1, _ := Canonical(v1)
	c2, _ := Canonical(v2)
	if !bytes.Equal(c1, c2) {
		t.Fatalf("canonical encoding mismatch")
	}
	if PayloadHash(c1) != PayloadHash(c2) {
		t.Fatalf("hashes diverge despite identical canonical bytes")
	}
}
