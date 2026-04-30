package credstore_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/credstore"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/ref"
)

func TestPeek_DisplayRule_LongKey(t *testing.T) {
	// A 32-byte key — longer than 12 bytes → prefix+…+suffix
	key := []byte("sk-anthropic-example-key-d4f9xyz") // 32 bytes
	resolver := newFakeResolver(map[string][]byte{"LONG": key})
	s := newStore(t, resolver, nil)
	ctx := context.Background()

	r, err := s.Peek(ctx, validRef("LONG"))
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if !strings.Contains(r.Display, "…") {
		t.Fatalf("expected '…' separator in display for long key, got %q", r.Display)
	}
	// prefix = first 4 runes, suffix = last 4 runes
	keyStr := string(key)
	wantPrefix := string([]rune(keyStr)[:4])
	wantSuffix := string([]rune(keyStr)[len([]rune(keyStr))-4:])
	if !strings.HasPrefix(r.Display, wantPrefix) {
		t.Fatalf("display %q should start with %q", r.Display, wantPrefix)
	}
	if !strings.HasSuffix(r.Display, wantSuffix) {
		t.Fatalf("display %q should end with %q", r.Display, wantSuffix)
	}
}

func TestPeek_DisplayRule_ShortKey(t *testing.T) {
	// An 8-byte key — ≤12 bytes → 8 bullets
	key := []byte("tooshort") // 8 bytes
	resolver := newFakeResolver(map[string][]byte{"SHORT": key})
	s := newStore(t, resolver, nil)
	ctx := context.Background()

	r, err := s.Peek(ctx, validRef("SHORT"))
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	want := strings.Repeat("•", 8)
	if r.Display != want {
		t.Fatalf("want %q, got %q", want, r.Display)
	}
}

func TestPeek_DisplayRule_ExactlyTwelveBytes(t *testing.T) {
	// 12 bytes → ≤12 rule → 8 bullets
	key := []byte("12bytesvalue") // 12 bytes
	resolver := newFakeResolver(map[string][]byte{"TWELVE": key})
	s := newStore(t, resolver, nil)
	ctx := context.Background()

	r, err := s.Peek(ctx, validRef("TWELVE"))
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	want := strings.Repeat("•", 8)
	if r.Display != want {
		t.Fatalf("want %q (≤12 bytes), got %q", want, r.Display)
	}
}

func TestPeek_EmptyRef(t *testing.T) {
	s := newStore(t, newFakeResolver(nil), nil)
	ctx := context.Background()
	_, err := s.Peek(ctx, secrets.CredentialReference{})
	if !errors.Is(err, credstore.ErrEmptyCredential) {
		t.Fatalf("want ErrEmptyCredential, got %v", err)
	}
}

func TestPeek_LocatorIsID(t *testing.T) {
	// Peek must expose the redaction-safe ID, not the raw locator.
	key := []byte("some-secret-value-that-is-long-enough")
	resolver := newFakeResolver(map[string][]byte{"MYLOCATOR": key})
	s := newStore(t, resolver, nil)
	ctx := context.Background()

	cred := secrets.CredentialReference{Kind: ref.RefEnv, Locator: "MYLOCATOR"}
	r, err := s.Peek(ctx, cred)
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if r.Locator == "MYLOCATOR" {
		t.Fatal("Peek must return redaction-safe ID, not raw locator")
	}
	if r.Locator != cred.ID() {
		t.Fatalf("Locator: want %q, got %q", cred.ID(), r.Locator)
	}
}

func TestPeek_KindPresent(t *testing.T) {
	key := []byte("value-long-enough-to-trigger-prefix-rule")
	resolver := newFakeResolver(map[string][]byte{"KINDTEST": key})
	s := newStore(t, resolver, nil)
	ctx := context.Background()

	cred := secrets.CredentialReference{Kind: ref.RefEnv, Locator: "KINDTEST"}
	r, err := s.Peek(ctx, cred)
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if r.Kind != "env" {
		t.Fatalf("want Kind='env', got %q", r.Kind)
	}
}
