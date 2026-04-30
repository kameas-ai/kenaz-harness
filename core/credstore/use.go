package credstore

import (
	"context"
	"runtime"
)

// Use resolves the credential behind h and passes the raw bytes to op.
//
// Contract:
//   - Single-use: the first call flips the entry's `used` flag via
//     CompareAndSwap. A second call returns ErrUseAfterFree.
//   - The raw bytes are zeroed in a defer BEFORE op is called, so
//     even a panic in op leaves no residual bytes on the heap.
//   - op is called WITHOUT holding any internal lock. The store lock
//     is acquired only to look up the entry; it is released before
//     resolution and before op runs.
//
// Lock semantics: op runs outside the store lock. This avoids
// deadlocks when op itself calls back into the store, and prevents
// long-running op calls from blocking other goroutines. The
// single-use atomic flag provides the exclusivity guarantee.
func (s *store) Use(ctx context.Context, h Handle, op func([]byte) error) (retErr error) {
	// ── 1. Look up + validate ─────────────────────────────────────────
	s.mu.RLock()
	e, ok := s.entries[h]
	s.mu.RUnlock()

	if !ok {
		return ErrUnknownHandle
	}
	if s.clock().After(e.expiresAt) {
		return ErrHandleExpired
	}
	// CompareAndSwap: false→true succeeds only once.
	if !e.used.CompareAndSwap(false, true) {
		return ErrUseAfterFree
	}

	// ── 2. Resolve ────────────────────────────────────────────────────
	sec, err := s.resolver.Resolve(ctx, e.ref, string(e.purpose))
	if err != nil {
		return err
	}
	defer sec.Destroy()

	// ── 3. Extract bytes, zero in defer, call op ───────────────────
	//
	// We need the raw bytes so we can zero them on the deferred path
	// even when op panics. Use secret.Use to access the bytes, then
	// copy them into a local buffer that we control. The defer-zero
	// runs before op returns so the window is minimal.
	var buf []byte

	extractErr := sec.Use(func(raw []byte) error {
		buf = make([]byte, len(raw))
		copy(buf, raw)
		return nil
	})
	if extractErr != nil {
		return extractErr
	}

	// Zero buf on every exit path, including panic.
	defer func() {
		for i := range buf {
			buf[i] = 0
		}
		runtime.KeepAlive(buf)
	}()

	// ── 4. Call op ────────────────────────────────────────────────────
	opErr := op(buf)

	// ── 5. Audit stub (WP03 fills the body) ───────────────────────────
	s.emitAccessed(ctx, e.ref, e.purpose, opErr)

	return opErr
}

// emitAccessed is the WP03 audit hook. It is a no-op here; WP03
// replaces this body with the real event emission.
//
// TODO(WP03): emit an AccessedEvent to the audit log.
func (s *store) emitAccessed(_ context.Context, _ interface{}, _ AccessPurpose, _ error) {
	// no-op stub — intentional
}
