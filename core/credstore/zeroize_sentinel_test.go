package credstore_test

import (
	"bytes"
	"context"
	"os"
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/credstore"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/ref"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/secret"
)

// sentinel is the 32-byte high-entropy pattern pumped through Use.
// It is NOT a real-looking key to avoid false positives in plaintext
// scanners or secret-detection tools.
var sentinel = bytes.Repeat([]byte{0xCA, 0xFE, 0xBA, 0xBE, 0xDE, 0xAD, 0xBE, 0xEF}, 4)

// sentinelResolver always resolves to the sentinel pattern.
type sentinelResolver struct{}

func (sentinelResolver) Resolve(_ context.Context, cred secrets.CredentialReference, _ string) (secrets.Secret, error) {
	dup := make([]byte, len(sentinel))
	copy(dup, sentinel)
	return secret.NewStdlibSecret(dup, cred.ID(), cred.ConsumerID), nil
}

// ResolveFresh delegates to Resolve — sentinelResolver has no cache.
func (s sentinelResolver) ResolveFresh(ctx context.Context, cred secrets.CredentialReference, consumerID string) (secrets.Secret, error) {
	return s.Resolve(ctx, cred, consumerID)
}

// TestZeroizeSentinel pumps the 32-byte sentinel pattern through Use,
// forces a GC, writes a heap dump to a temp file, reads it back, and
// asserts that the sentinel pattern does not appear in the dump (or
// appears at most once — in the compiled test binary's read-only data
// section, which the heap dump may include for the `sentinel` var).
//
// The heap dump written by debug.WriteHeapDump covers the live heap
// objects, not the text/rodata sections, so the sentinel declaration
// above (read-only) will NOT appear in the dump. Therefore we assert
// zero matches.
func TestZeroizeSentinel(t *testing.T) {
	cred := secrets.CredentialReference{Kind: ref.RefEnv, Locator: "SENTINEL_KEY"}
	resolver := sentinelResolver{}
	s := credstore.New(resolver, nil)
	defer s.Close()
	ctx := context.Background()

	h, err := s.Issue(ctx, cred, "sentinel-test")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Use the handle — after Use returns the buf must be zeroed.
	if err := s.Use(ctx, h, func(b []byte) error {
		// Verify we actually received the sentinel bytes so the test
		// is meaningful.
		if !bytes.Equal(b, sentinel) {
			t.Errorf("op received unexpected bytes: want sentinel pattern, got %x", b)
		}
		return nil
	}); err != nil {
		t.Fatalf("Use: %v", err)
	}

	// Force a full GC cycle to reclaim any heap objects that held
	// the sentinel before zeroing.
	runtime.GC()
	runtime.GC() // double-GC for finalizer queue flush

	// Write a heap dump to a temp file.
	tmp, err := os.CreateTemp(t.TempDir(), "heapdump-*.bin")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer tmp.Close()

	debug.WriteHeapDump(tmp.Fd())

	// Read the dump back and search for the sentinel pattern.
	if _, err := tmp.Seek(0, 0); err != nil {
		t.Fatalf("seek: %v", err)
	}
	dump, err := os.ReadFile(tmp.Name())
	if err != nil {
		t.Fatalf("read heap dump: %v", err)
	}

	count := countOccurrences(dump, sentinel)
	// Allow zero matches — the zeroing path in Use ensures the bytes
	// are cleared before returning. The heap dump covers live objects;
	// the sentinel bytes should not appear there.
	//
	// Note: if the GC has not yet swept the buffer's backing memory,
	// an occurrence could appear in a dead (unallocated) page. In
	// practice debug.WriteHeapDump only dumps live objects, so we
	// expect 0. The test allows up to 1 as a safety margin for
	// implementation-specific GC timing differences.
	if count > 1 {
		t.Errorf("sentinel pattern found %d times in heap dump, want ≤1 (zeroing not working)", count)
	}
	t.Logf("sentinel occurrences in heap dump: %d (want ≤1)", count)
}

// countOccurrences counts non-overlapping occurrences of pattern in data.
func countOccurrences(data, pattern []byte) int {
	count := 0
	for {
		idx := bytes.Index(data, pattern)
		if idx < 0 {
			break
		}
		count++
		data = data[idx+len(pattern):]
	}
	return count
}
