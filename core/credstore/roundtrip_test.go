package credstore_test

// WP04 — RoundTrip convenience wrapper tests.
//
// Coverage:
//   - Happy path: bytes visible inside op, op error propagated.
//   - Bytes are zeroed after RoundTrip even if op panics.
//   - RoundTrip on a closed store returns ErrStoreClosed.
//   - RoundTrip with an empty ref returns ErrEmptyCredential.

import (
	"context"
	"errors"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/credstore"
)

func TestRoundTrip_HappyPath(t *testing.T) {
	resolver := newFakeResolver(map[string][]byte{"RT": []byte("rtvalue")})
	s := newStore(t, resolver, nil)
	ctx := context.Background()

	var got string
	err := s.RoundTrip(ctx, validRef("RT"), "provider_call", func(b []byte) error {
		got = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got != "rtvalue" {
		t.Fatalf("want %q, got %q", "rtvalue", got)
	}
}

func TestRoundTrip_OpErrorPropagated(t *testing.T) {
	resolver := newFakeResolver(map[string][]byte{"RT": []byte("v")})
	s := newStore(t, resolver, nil)
	ctx := context.Background()

	want := errors.New("op-err")
	err := s.RoundTrip(ctx, validRef("RT"), "provider_call", func([]byte) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("want op error, got %v", err)
	}
}

func TestRoundTrip_EmptyRef(t *testing.T) {
	s := newStore(t, newFakeResolver(nil), nil)
	ctx := context.Background()

	err := s.RoundTrip(ctx, validRef(""), "provider_call", func([]byte) error { return nil })
	if !errors.Is(err, credstore.ErrEmptyCredential) {
		t.Fatalf("want ErrEmptyCredential, got %v", err)
	}
}

func TestRoundTrip_ClosedStore(t *testing.T) {
	resolver := newFakeResolver(map[string][]byte{"K": []byte("v")})
	s := credstore.New(resolver, nil)
	s.Close()
	ctx := context.Background()

	err := s.RoundTrip(ctx, validRef("K"), "provider_call", func([]byte) error { return nil })
	if !errors.Is(err, credstore.ErrStoreClosed) {
		t.Fatalf("want ErrStoreClosed, got %v", err)
	}
}

func TestRoundTrip_PanicZeroesBuf(t *testing.T) {
	payload := []byte("roundtrip-secret")
	resolver := newFakeResolver(map[string][]byte{"RT": payload})
	s := newStore(t, resolver, nil)
	ctx := context.Background()

	type mirror struct{ backing []byte }
	mirrorCh := make(chan mirror, 1)

	func() {
		defer func() { recover() }() //nolint:errcheck
		_ = s.RoundTrip(ctx, validRef("RT"), "provider_call", func(b []byte) error {
			mirrorCh <- mirror{backing: b}
			panic("deliberate roundtrip panic")
		})
	}()

	m := <-mirrorCh
	for i, v := range m.backing {
		if v != 0 {
			t.Fatalf("buf[%d] = %d after panic, want 0", i, v)
		}
	}
}
