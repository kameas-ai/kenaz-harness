package credstore_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/credstore"
)

// TestUse_OpPanicZeroesBuf verifies that when op panics the credential
// bytes are still zeroed before propagation leaves Use.
//
// Technique: op sends a copy of buf over a channel, then panics.
// A recovering goroutine catches the panic via recover(). After the
// recover, the original buf slice that Use held is checked via the
// channel copy vs direct inspection — but since Use zeroes ITS copy,
// the bytes passed INTO op will be all zeros if we read them AFTER
// the panic has been recovered inside Use.
//
// Simpler verification: we use a sentinelResolver that gives us a
// mirrored view of the buffer allocated inside Use, then verify all
// zeros post-panic.
func TestUse_OpPanicZeroesBuf(t *testing.T) {
	payload := []byte("supersecretvalue")
	resolver := newFakeResolver(map[string][]byte{"PK": payload})
	s := newStore(t, resolver, nil)
	ctx := context.Background()

	h, err := s.Issue(ctx, validRef("PK"), "panic-test")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// bufMirrorCh receives a copy of the buf slice header (pointer +
	// length) before the panic so we can inspect the underlying array
	// after Use has zeroed it.
	type mirror struct {
		backing []byte
	}
	mirrorCh := make(chan mirror, 1)

	func() {
		defer func() { recover() }() //nolint:errcheck
		_ = s.Use(ctx, h, func(b []byte) error {
			// Send a slice pointing at the SAME underlying array.
			// We deliberately do NOT copy — we want to inspect the
			// original backing array after Use zeroes it.
			mirrorCh <- mirror{backing: b}
			panic("deliberate test panic")
		})
	}()

	m := <-mirrorCh
	// After Use's deferred zero loop has run, the backing array
	// should be all zeros.
	for i, v := range m.backing {
		if v != 0 {
			t.Fatalf("buf[%d] = %d, want 0 after panic (buf not zeroed)", i, v)
		}
	}
}

// TestUse_OpErrorPropagated verifies that a non-nil error from op is
// returned by Use.
func TestUse_OpErrorPropagated(t *testing.T) {
	sentinel := errors.New("op-error-sentinel")
	resolver := newFakeResolver(map[string][]byte{"EK": []byte("value")})
	s := newStore(t, resolver, nil)
	ctx := context.Background()

	h, err := s.Issue(ctx, validRef("EK"), "err-test")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	err = s.Use(ctx, h, func([]byte) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel error, got %v", err)
	}
}

// TestUse_ConcurrentIssueUse exercises the store under -race. Multiple
// goroutines each Issue their own handle and Use it concurrently.
func TestUse_ConcurrentIssueUse(t *testing.T) {
	const workers = 32
	resolver := newFakeResolver(map[string][]byte{"CK": []byte("concurrent-value")})
	s := newStore(t, resolver, nil)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, workers*2)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, err := s.Issue(ctx, validRef("CK"), "concurrent")
			if err != nil {
				errs <- err
				return
			}
			if err := s.Use(ctx, h, func([]byte) error { return nil }); err != nil {
				errs <- err
			}
		}()
	}

	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("concurrent error: %v", e)
	}
}

// TestUse_ConcurrentClose verifies that Close racing with Use is
// race-free (the race detector is the real assertion here).
func TestUse_ConcurrentClose(t *testing.T) {
	resolver := newFakeResolver(map[string][]byte{"CC": []byte("val")})
	s := credstore.New(resolver, nil) // don't register Cleanup — we close manually

	ctx := context.Background()
	var wg sync.WaitGroup

	// Issue a batch of handles before closing.
	handles := make([]credstore.Handle, 20)
	for i := range handles {
		h, err := s.Issue(ctx, validRef("CC"), "close-race")
		if err != nil {
			t.Logf("Issue pre-close: %v (acceptable)", err)
			break
		}
		handles[i] = h
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.Close()
	}()

	for _, h := range handles {
		if h == "" {
			continue
		}
		wg.Add(1)
		go func(hh credstore.Handle) {
			defer wg.Done()
			// Error is acceptable (ErrUnknownHandle, ErrStoreClosed, etc.)
			_ = s.Use(ctx, hh, func([]byte) error { return nil })
		}(h)
	}

	wg.Wait()
}
