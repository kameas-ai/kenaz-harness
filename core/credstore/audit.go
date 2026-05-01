package credstore

// Audit-emission wiring for credential-store-01KQ8TDD WP03.
//
// Design:
//   - emitAccessed is called by Use after every op invocation (success
//     or failure). It fires exactly once per Use call.
//   - The store optionally holds an audit.Emitter (injected via
//     WithAuditEmitter). nil emitter is a no-op (FR-008).
//   - NFR-002: the emitter write is non-blocking. An internal 256-slot
//     channel fans the event to the background drain goroutine; if the
//     channel is full the emit is dropped — the caller is never blocked.
//   - NFR-001: benchmarked in audit_test.go to confirm < 1ms overhead.

import (
	"context"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/context/audit"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
)

const auditChanCap = 256

// auditEmitJob is the message sent over the internal drain channel.
type auditEmitJob struct {
	ctx     context.Context
	payload audit.CredentialAccessedPayload
}

// WithAuditEmitter injects an audit.Emitter into the store. Emits for
// every credstore.Use call will be sent to em asynchronously through a
// 256-slot buffered channel; overflow is silently dropped (NFR-002).
func WithAuditEmitter(em audit.Emitter) StoreOption {
	return func(s *store) {
		if em == nil {
			return
		}
		s.auditEmitter = em
		s.auditCh = make(chan auditEmitJob, auditChanCap)
		go s.auditDrain()
	}
}

// auditDrain reads from auditCh and forwards each job to the real
// emitter. It stops when the store is closed (s.done) and drains any
// remaining buffered events before returning.
func (s *store) auditDrain() {
	for {
		select {
		case job, ok := <-s.auditCh:
			if !ok {
				return
			}
			_ = audit.Emit(job.ctx, s.auditEmitter, audit.KindCredentialAccessed, job.payload, job.payload.AccessedAt)
		case <-s.done:
			// Drain remaining jobs before exit.
			for {
				select {
				case job := <-s.auditCh:
					_ = audit.Emit(job.ctx, s.auditEmitter, audit.KindCredentialAccessed, job.payload, job.payload.AccessedAt)
				default:
					return
				}
			}
		}
	}
}

// emitAccessed is called by Use after op returns. It builds the
// CredentialAccessedPayload from the entry record and the active
// context, then non-blockingly dispatches it to auditCh. If the
// channel is full the event is dropped (NFR-002).
//
// Neither the raw bytes, the locator string, nor the display string
// appear in the payload (FR-008 / DIRECTIVE_001).
func (s *store) emitAccessed(ctx context.Context, ref secrets.CredentialReference, purpose AccessPurpose, _ error) {
	if s.auditCh == nil {
		return // no emitter wired — no-op
	}
	payload := audit.CredentialAccessedPayload{
		RefID:      ref.ID(),
		Purpose:    string(purpose),
		AccessedAt: time.Now(),
	}
	// Non-blocking send: drop on full channel (NFR-002).
	select {
	case s.auditCh <- auditEmitJob{ctx: ctx, payload: payload}:
	default:
		// channel full — drop emit, never block caller
	}
}
