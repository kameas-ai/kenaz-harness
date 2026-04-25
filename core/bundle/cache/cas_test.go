package cache_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	bundle "github.com/sigil-tech/kaneaz-harness/core/bundle"
	"github.com/sigil-tech/kaneaz-harness/core/bundle/cache"
)

func mkCAS(t *testing.T) cache.CAS {
	t.Helper()
	dir := t.TempDir()
	c, err := cache.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

func TestPutMatch(t *testing.T) {
	c := mkCAS(t)
	data := []byte("hello, kaneaz")
	digest := sha(data)
	r, err := c.Put(context.Background(), bytes.NewReader(data), digest)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if r.Digest != digest {
		t.Errorf("digest: %s vs %s", r.Digest, digest)
	}
	if !c.Has(digest) {
		t.Errorf("Has=false after Put")
	}
	rd, err := c.Get(digest)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got, _ := io.ReadAll(rd)
	rd.Close()
	if !bytes.Equal(got, data) {
		t.Errorf("Get returned %q want %q", got, data)
	}
}

func TestPutMismatch(t *testing.T) {
	c := mkCAS(t)
	data := []byte("hello, kaneaz")
	wrongDigest := sha([]byte("different"))
	_, err := c.Put(context.Background(), bytes.NewReader(data), wrongDigest)
	if !errors.Is(err, bundle.ErrIntegrityMismatch) {
		t.Fatalf("err=%v, want ErrIntegrityMismatch", err)
	}
	if c.Has(wrongDigest) {
		t.Errorf("Has=true after mismatched Put")
	}
	// Staging dir should be empty.
	root := c.Root()
	staging := filepath.Join(root, "staging")
	entries, _ := os.ReadDir(staging)
	if len(entries) > 0 {
		t.Errorf("staging not cleaned: %v", entries)
	}
}

func TestPutCancel(t *testing.T) {
	c := mkCAS(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Use a slow reader so the ctx check trips early.
	slow := &slowReader{data: bytes.Repeat([]byte("x"), 1024), each: time.Millisecond}
	digest := sha(slow.data)
	_, err := c.Put(ctx, slow, digest)
	if !errors.Is(err, bundle.ErrCancelled) {
		t.Fatalf("err=%v, want ErrCancelled", err)
	}
	root := c.Root()
	staging := filepath.Join(root, "staging")
	entries, _ := os.ReadDir(staging)
	if len(entries) > 0 {
		t.Errorf("staging not cleaned: %v", entries)
	}
}

func TestEvict(t *testing.T) {
	c := mkCAS(t)
	data := []byte("evict-me")
	digest := sha(data)
	if _, err := c.Put(context.Background(), bytes.NewReader(data), digest); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := c.Evict(digest); err != nil {
		t.Fatalf("Evict: %v", err)
	}
	if c.Has(digest) {
		t.Errorf("Has=true after Evict")
	}
	// Evict on non-existent digest is a no-op.
	if err := c.Evict(digest); err != nil {
		t.Errorf("second Evict: %v", err)
	}
}

func TestPathLayout(t *testing.T) {
	c := mkCAS(t)
	data := []byte("layout")
	digest := sha(data)
	r, err := c.Put(context.Background(), bytes.NewReader(data), digest)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	hexPart := strings.TrimPrefix(digest, "sha256:")
	want := filepath.Join(c.Root(), "sha256", hexPart[0:2], hexPart[2:4], hexPart)
	if r.Path != want {
		t.Errorf("path=%s want=%s", r.Path, want)
	}
}

func TestConcurrent(t *testing.T) {
	// 100 random-ish digests in parallel — exercise no collisions / no
	// goroutine corruption.
	c := mkCAS(t)
	const N = 100
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(n int) {
			defer wg.Done()
			data := []byte(strings.Repeat("a", n+1))
			d := sha(data)
			_, err := c.Put(context.Background(), bytes.NewReader(data), d)
			if err != nil {
				t.Errorf("Put %d: %v", n, err)
				return
			}
			if !c.Has(d) {
				t.Errorf("Has=false for %s", d)
			}
		}(i)
	}
	wg.Wait()
}

func TestInvalidDigest(t *testing.T) {
	c := mkCAS(t)
	_, err := c.Put(context.Background(), bytes.NewReader([]byte("x")), "not-a-digest")
	if err == nil {
		t.Errorf("expected error for invalid digest")
	}
	if c.Has("not-a-digest") {
		t.Errorf("Has=true for invalid digest")
	}
}

// slowReader emits one byte per Read with a small delay, useful for
// cancellation tests.
type slowReader struct {
	data []byte
	pos  int
	each time.Duration
}

func (s *slowReader) Read(p []byte) (int, error) {
	if s.pos >= len(s.data) {
		return 0, io.EOF
	}
	time.Sleep(s.each)
	p[0] = s.data[s.pos]
	s.pos++
	return 1, nil
}
