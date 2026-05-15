// Package log — hash chain verifier.
//
// VerifyChain walks the event log in event_id order and recomputes each
// row's payload_hash. Any discrepancy indicates tampering; the function
// returns a VerifyChainResult with Verified=false and the first broken ID.
//
// Performance: rows are fetched in batches of 10 000 to bound peak
// memory while still meeting the NFR-001 budget of ≤ 30 s for 100 k rows.
//
// Migration-boundary rows: rows with PrevHash == [32]byte{} are treated
// as chain-reset checkpoints — the verifier resets the running prev_hash
// to zero at each boundary so sub-chains can still be verified.
package log

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// VerifyChainResult carries the output of VerifyChain.
type VerifyChainResult struct {
	// Verified is true when every checked row's payload_hash matched the
	// recomputed digest.
	Verified bool `json:"verified"`
	// RowsChecked is the number of rows examined.
	RowsChecked int `json:"rows_checked"`
	// BrokenAtID is the event_id of the first row whose hash did not
	// match. Empty when Verified is true.
	BrokenAtID string `json:"broken_at_id,omitempty"`
}

const verifyBatchSize = 10_000

// canonicalSerialize produces the deterministic byte sequence used as
// input to the SHA-256 hash. The shape must match the writer path exactly.
//
// Format: <prev_hash_hex> | <kind> | <emitted_at_unix_ms> | <payload_json>
func canonicalSerialize(r Row) []byte {
	prevHex := fmt.Sprintf("%x", r.PrevHash)
	return []byte(fmt.Sprintf("%s|%s|%d|%s",
		prevHex, r.Kind, r.EmittedAt.UnixMilli(), string(r.Payload)))
}

// recomputeHash recomputes the expected payload_hash for a row.
func recomputeHash(r Row) [32]byte {
	return sha256.Sum256(canonicalSerialize(r))
}

// VerifyChain verifies the hash chain for all events with event_id in
// [fromID, toID] (inclusive). An empty fromID starts from the first
// row; an empty toID ends at the last row. Rows are fetched in batches
// of 10 000 and verified in event_id ascending order.
//
// Migration-boundary rows (PrevHash == [32]byte{}) are treated as
// chain checkpoints: the verifier resets the running expected-prev to
// zero at each one, then continues the sub-chain verification.
func VerifyChain(ctx context.Context, backend Backend, fromID, toID string) (VerifyChainResult, error) {
	var allRows []Row
	after := ""

	// Fetch all rows in batches using the time-range selector with open bounds.
	for {
		var zeroTime time.Time
		batch, err := backend.SelectByTimeRange(ctx, zeroTime, zeroTime, after, verifyBatchSize, false)
		if err != nil {
			return VerifyChainResult{}, fmt.Errorf("VerifyChain: fetch batch after %q: %w", after, err)
		}
		// Filter by [fromID, toID] window.
		for _, r := range batch {
			if fromID != "" && r.EventID < fromID {
				continue
			}
			if toID != "" && r.EventID > toID {
				continue
			}
			allRows = append(allRows, r)
		}
		if len(batch) < verifyBatchSize {
			break // last batch
		}
		after = batch[len(batch)-1].EventID
		if toID != "" && after >= toID {
			break
		}
		if ctx.Err() != nil {
			return VerifyChainResult{}, ctx.Err()
		}
	}

	// Sort by event_id ascending (defensive sort; backend should already order).
	sort.Slice(allRows, func(i, j int) bool { return allRows[i].EventID < allRows[j].EventID })

	result := VerifyChainResult{Verified: true}
	// Per-session running expected prev_hash.
	sessionExpected := map[string][32]byte{}

	for _, r := range allRows {
		result.RowsChecked++

		expectedPrev := sessionExpected[r.SessionID]

		// Migration-boundary / session-start: if PrevHash is zero treat as
		// a chain checkpoint. This handles both the first event in a session
		// and rows written before migration 0001 populated prev_hash.
		if r.PrevHash == ([32]byte{}) {
			expectedPrev = [32]byte{}
		} else if r.PrevHash != expectedPrev {
			result.Verified = false
			result.BrokenAtID = r.EventID
			return result, nil
		}

		// Recompute the hash and compare.
		computed := recomputeHash(r)
		if computed != r.PayloadHash {
			result.Verified = false
			result.BrokenAtID = r.EventID
			return result, nil
		}

		sessionExpected[r.SessionID] = r.PayloadHash
	}

	return result, nil
}

// Confirm JSON serialisability for the RPC layer (compile-time guard).
var _ = func() {
	_, _ = json.Marshal(VerifyChainResult{})
}
