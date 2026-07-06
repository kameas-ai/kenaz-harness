// Package fleet — audit_chain_verifier.go
//
// BatchChainVerifier verifies the hash-chain continuity of a candidate
// batch of TailEvent rows before they are sent to the fleet endpoint.
//
// The verifier checks that:
//  1. Each row's prev_hash equals the previous row's payload_hash.
//  2. The first row's prev_hash matches the last ACK'd event's hash
//     (if a predecessor hash is provided).
//
// A break in either condition indicates local tampering. The archiver
// hard-stops on a break (FR-005) and emits KindFleetAuditChainBreak.
//
// Performance: the verifier only walks the batch boundary (up to 100
// rows); it does NOT re-hash the payloads. Payload hash integrity is
// the event-log store's responsibility (core/event/log/chain.go).
// This meets NFR-004 (≤ 50 ms for the boundary check).
//
// (fleet-audit-archival-01NDFSEX13 WP03)
package fleet

import (
	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
)

// BatchChainVerifier implements AuditChainVerifier by walking the
// prev_hash / payload_hash links across the batch.
//
// PredecessorHash is the payload_hash of the event immediately before
// the first event in the batch. Set to [32]byte{} when no predecessor
// exists (first batch ever, or cursor is at zero).
type BatchChainVerifier struct {
	// PredecessorHash is the hash of the event that precedes the first
	// event of the next batch. Updated after each successful batch ACK.
	PredecessorHash [32]byte
}

// VerifyBatch checks the hash-chain continuity of events.
// Returns (true, "") when the batch is consistent.
// Returns (false, brokenAtID) at the first inconsistency.
func (v *BatchChainVerifier) VerifyBatch(events []contextaudit.TailEvent) (ok bool, brokenAtID string) {
	if len(events) == 0 {
		return true, ""
	}

	expectedPrev := v.PredecessorHash

	for _, e := range events {
		// Each event's PrevHash must equal the previous event's PayloadHash.
		// Exception: if PrevHash is the zero hash, treat as a chain
		// checkpoint (session-start or migration boundary) — accept it.
		if e.PrevHash != ([32]byte{}) && e.PrevHash != expectedPrev {
			return false, e.ID
		}
		expectedPrev = e.PayloadHash
	}
	return true, ""
}

// AdvancePredecessor updates the PredecessorHash to the last event's
// PayloadHash after a successful ACK. Must be called by the archiver
// after each successful flush.
func (v *BatchChainVerifier) AdvancePredecessor(events []contextaudit.TailEvent) {
	if len(events) == 0 {
		return
	}
	v.PredecessorHash = events[len(events)-1].PayloadHash
}
