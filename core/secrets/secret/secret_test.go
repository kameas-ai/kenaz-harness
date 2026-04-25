package secret

import (
	"bytes"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestStdlibSecret_UseReturnsBytes(t *testing.T) {
	want := []byte("hunter2-very-long-credential-value")
	cp := append([]byte(nil), want...)
	s := NewStdlibSecret(cp, "ref-id", "consumer")

	called := false
	err := s.Use(func(v []byte) error {
		called = true
		if !bytes.Equal(v, want) {
			t.Errorf("Use got %q want %q", v, want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("closure not called")
	}
}

func TestStdlibSecret_UsePropagatesError(t *testing.T) {
	s := NewStdlibSecret([]byte("x"), "id", "c")
	defer s.Destroy()

	sentinel := errors.New("boom")
	err := s.Use(func(v []byte) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Errorf("got %v, want %v", err, sentinel)
	}
}

func TestStdlibSecret_NilUseClosure(t *testing.T) {
	s := NewStdlibSecret([]byte("x"), "id", "c")
	defer s.Destroy()
	if err := s.Use(nil); err == nil {
		t.Error("expected error on nil closure")
	}
}

func TestStdlibSecret_DestroyZeroizes(t *testing.T) {
	buf := []byte("a-real-long-credential-value-for-zeroization-test")
	want := append([]byte(nil), buf...)
	s := NewStdlibSecret(buf, "id", "c")

	// Capture a reference into the internal buffer via Use so we can
	// verify it is zeroed AFTER Destroy. (In normal use the caller
	// MUST NOT do this; here we are testing the zeroization).
	var capture []byte
	if err := s.Use(func(v []byte) error {
		capture = v // illegal in production; OK in test
		if !bytes.Equal(v, want) {
			t.Errorf("Use got %q want %q", v, want)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.Destroy()

	// runtime.KeepAlive defeats elision; the buffer should now be all
	// zeros.
	if !allZero(capture) {
		t.Errorf("Destroy did not zero the buffer: %q", capture)
	}
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

func TestStdlibSecret_DestroyIdempotent(t *testing.T) {
	s := NewStdlibSecret([]byte("x"), "id", "c")
	s.Destroy()
	s.Destroy() // must not panic
	if !s.IsDestroyed() {
		t.Error("expected destroyed")
	}
	if s.Len() != 0 {
		t.Error("expected zero length after destroy")
	}
}

func TestStdlibSecret_UseAfterDestroy(t *testing.T) {
	s := NewStdlibSecret([]byte("x"), "id", "c")
	s.Destroy()
	err := s.Use(func(v []byte) error { return nil })
	if !errors.Is(err, ErrSecretDestroyed) {
		t.Errorf("expected ErrSecretDestroyed, got %v", err)
	}
}

func TestStdlibSecret_ConcurrentUse(t *testing.T) {
	s := NewStdlibSecret(bytes.Repeat([]byte("x"), 32), "id", "c")
	defer s.Destroy()

	var wg sync.WaitGroup
	var inUseErr int
	var okCount int
	var mu sync.Mutex

	// Force one goroutine to hold Use long enough that another
	// concurrent caller sees ErrSecretInUse.
	start := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := s.Use(func(v []byte) error {
				time.Sleep(20 * time.Millisecond)
				return nil
			})
			mu.Lock()
			defer mu.Unlock()
			if errors.Is(err, ErrSecretInUse) {
				inUseErr++
			} else if err == nil {
				okCount++
			} else {
				t.Errorf("unexpected err: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if okCount == 0 {
		t.Error("at least one Use should succeed")
	}
	if inUseErr == 0 {
		t.Error("at least one Use should report ErrSecretInUse under concurrency")
	}
}

func TestStdlibSecret_Metadata(t *testing.T) {
	s := NewStdlibSecret([]byte("x"), "rid", "cid")
	defer s.Destroy()
	if s.ReferenceID() != "rid" {
		t.Errorf("ReferenceID=%q", s.ReferenceID())
	}
	if s.ConsumerID() != "cid" {
		t.Errorf("ConsumerID=%q", s.ConsumerID())
	}
	if s.AcquiredAt().IsZero() {
		t.Error("AcquiredAt should be set")
	}
}

func TestZeroize_KeepAliveDefeatsElision(t *testing.T) {
	// Best-effort advisory: write a sentinel, zeroize, confirm the
	// buffer no longer contains the sentinel. Documented as advisory
	// per plan §4.
	buf := []byte("sentinel-credential-value-1234567890")
	zeroize(buf)
	runtime.KeepAlive(buf)
	if !allZero(buf) {
		t.Errorf("zeroize failed: %q", buf)
	}
}
