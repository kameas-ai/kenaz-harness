package fleet

// context_sync.go — append-only event-stream primitive for fleet context sync.
//
// Each EventStream wraps a fleet stream ID + encryption key. Callers push events
// via Append / Backfill and pull via Replay. All event bytes are encrypted with
// the AEAD key from context_crypto.go before they leave the process.
//
// Fleet API paths:
//   POST /api/v1/context/append  — append a batch of encrypted events
//   GET  /api/v1/context/replay  — replay events from seq N
//   DELETE /api/v1/context/{streamID} — purge all events for a stream
//
// Privacy invariant: no event payload bytes appear in slog. The client only
// logs stream IDs (short-form) and event counts.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// maxChunkBytes is the maximum plaintext size per POST chunk during backfill.
// Fleet spec NFR-002: 1MB chunks.
const maxChunkBytes = 1 << 20 // 1 MB

// backfillThrottle is the minimum delay between backfill chunk POSTs.
// Fleet spec NFR-002: 1 chunk / 2s.
const backfillThrottle = 2 * time.Second

// Event is a single context event ready for encryption + send (or post-decrypt
// for apply). Callers fill Seq + Payload; PrevHash is computed by the stream.
type Event struct {
	// Seq is the monotonic sequence number for this stream (1-based).
	Seq uint64 `json:"seq"`
	// Payload is the raw event bytes (before encryption / after decryption).
	// Privacy invariant: NEVER log Payload.
	Payload []byte `json:"-"` // excluded from JSON; wire form is EncryptedPayload
}

// wireEvent is the JSON shape POSTed to fleet. All content bytes are encrypted.
type wireEvent struct {
	Seq              uint64 `json:"seq"`
	EncryptedPayload []byte `json:"encrypted_payload"`
	Nonce            []byte `json:"nonce"`
	PrevHash         string `json:"prev_hash,omitempty"`
}

// appendRequest is the body posted to /api/v1/context/append.
type appendRequest struct {
	StreamID string      `json:"stream_id"`
	Events   []wireEvent `json:"events"`
}

// replayResponse is the JSON envelope returned by /api/v1/context/replay.
type replayResponse struct {
	Events  []wireEvent `json:"events"`
	HasMore bool        `json:"has_more"`
	NextSeq uint64      `json:"next_seq,omitempty"`
}

// EventStream is the append-only encrypted event stream primitive.
// It is safe for concurrent use.
type EventStream struct {
	client   *Client
	key      []byte   // 32-byte AEAD key; never logs
	streamID string   // fleet stream identifier (stream-scoped UUID)
	lastSeq  atomic.Uint64
}

// NewEventStream constructs an EventStream for the given stream ID and AEAD
// key. key must be exactly 32 bytes.
func NewEventStream(client *Client, streamID string, key []byte) (*EventStream, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("fleet: EventStream key must be 32 bytes, got %d", len(key))
	}
	if streamID == "" {
		return nil, errors.New("fleet: EventStream streamID must be non-empty")
	}
	es := &EventStream{
		client:   client,
		key:      key,
		streamID: streamID,
	}
	return es, nil
}

// SetLastSeq sets the watermark used by Append to number new events.
// Should be called with the local high-water mark before streaming.
func (es *EventStream) SetLastSeq(seq uint64) {
	es.lastSeq.Store(seq)
}

// LastSeq returns the current watermark.
func (es *EventStream) LastSeq() uint64 {
	return es.lastSeq.Load()
}

// Append encrypts events and POSTs them to fleet as a single chunk.
// Each event's Seq is assigned automatically from lastSeq+1.
// Safe to call from multiple goroutines; seq assignment is atomic.
func (es *EventStream) Append(ctx context.Context, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	wires, err := es.encryptEvents(events)
	if err != nil {
		return err
	}
	return es.postChunk(ctx, wires)
}

// Backfill encrypts and POSTs events in 1MB chunks, throttled to 1 chunk/2s.
// Designed for initial backfill of existing session events.
func (es *EventStream) Backfill(ctx context.Context, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	chunks := chunkedEvents(events, maxChunkBytes)
	for i, chunk := range chunks {
		wires, err := es.encryptEvents(chunk)
		if err != nil {
			return fmt.Errorf("fleet: backfill chunk %d encrypt: %w", i, err)
		}
		if err := es.postChunk(ctx, wires); err != nil {
			return fmt.Errorf("fleet: backfill chunk %d post: %w", i, err)
		}
		logging.L().Info("fleet.context_sync.backfill_chunk",
			"stream_id", shortID(es.streamID),
			"chunk", i+1,
			"chunks_total", len(chunks),
			"events_in_chunk", len(chunk),
		)
		// Throttle: wait between chunks (but not after the last chunk).
		if i < len(chunks)-1 {
			select {
			case <-time.After(backfillThrottle):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return nil
}

// Replay fetches events from fleet starting at sinceSeq, decrypts them, and
// calls apply for each. Replay stops when fleet reports no more events or when
// ctx is cancelled.
//
// apply is called in seq order. If apply returns an error, Replay halts and
// propagates it.
func (es *EventStream) Replay(ctx context.Context, sinceSeq uint64, apply func(Event) error) error {
	nextSeq := sinceSeq
	for {
		path := fmt.Sprintf("/api/v1/context/replay?stream_id=%s&since=%d", es.streamID, nextSeq)
		resp, err := es.client.Get(ctx, path)
		if err != nil {
			return fmt.Errorf("fleet: context replay GET: %w", err)
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("fleet: context replay read body: %w", readErr)
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("fleet: context replay status %d", resp.StatusCode)
		}

		var rr replayResponse
		if err := json.Unmarshal(body, &rr); err != nil {
			return fmt.Errorf("fleet: context replay parse: %w", err)
		}

		for _, we := range rr.Events {
			pt, decErr := Decrypt(es.key, we.EncryptedPayload, we.Nonce)
			if decErr != nil {
				return fmt.Errorf("fleet: replay decrypt seq %d: %w", we.Seq, decErr)
			}
			ev := Event{Seq: we.Seq, Payload: pt}
			if applyErr := apply(ev); applyErr != nil {
				return applyErr
			}
			if we.Seq > es.lastSeq.Load() {
				es.lastSeq.Store(we.Seq)
			}
		}

		logging.L().Info("fleet.context_sync.replay_batch",
			"stream_id", shortID(es.streamID),
			"events_received", len(rr.Events),
			"has_more", rr.HasMore,
		)

		if !rr.HasMore {
			break
		}
		nextSeq = rr.NextSeq
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	return nil
}

// DeleteRemote sends DELETE /api/v1/context/<streamID> to purge all events.
func (es *EventStream) DeleteRemote(ctx context.Context) error {
	path := "/api/v1/context/" + es.streamID
	resp, err := es.client.Delete(ctx, path)
	if err != nil {
		return fmt.Errorf("fleet: context delete: %w", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("fleet: context delete status %d", resp.StatusCode)
	}
	logging.L().Info("fleet.context_sync.remote_deleted",
		"stream_id", shortID(es.streamID),
	)
	return nil
}

// ── internal helpers ──────────────────────────────────────────────────────────

// encryptEvents encrypts a slice of events and returns wire-ready structs.
// Seq numbers are assigned atomically if Event.Seq == 0.
func (es *EventStream) encryptEvents(events []Event) ([]wireEvent, error) {
	out := make([]wireEvent, 0, len(events))
	for _, ev := range events {
		seq := ev.Seq
		if seq == 0 {
			seq = es.lastSeq.Add(1)
		}
		ct, nonce, err := Encrypt(es.key, ev.Payload)
		if err != nil {
			return nil, fmt.Errorf("fleet: encrypt event seq=%d: %w", seq, err)
		}
		out = append(out, wireEvent{
			Seq:              seq,
			EncryptedPayload: ct,
			Nonce:            nonce,
		})
	}
	return out, nil
}

// postChunk sends a single appendRequest to fleet.
func (es *EventStream) postChunk(ctx context.Context, events []wireEvent) error {
	req := appendRequest{
		StreamID: es.streamID,
		Events:   events,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("fleet: marshal append request: %w", err)
	}
	resp, err := es.client.Post(ctx, "/api/v1/context/append", "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("fleet: context append POST: %w", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("fleet: context append status %d", resp.StatusCode)
	}
	logging.L().Info("fleet.context_sync.appended",
		"stream_id", shortID(es.streamID),
		"events", len(events),
	)
	return nil
}

// chunkedEvents splits events into groups so each group's estimated plaintext
// size stays under maxBytes. Estimation is based on event payload size only;
// wire overhead (JSON, nonces, ciphertext overhead) is additive but bounded.
func chunkedEvents(events []Event, maxBytes int) [][]Event {
	var chunks [][]Event
	var current []Event
	currentSize := 0
	for _, ev := range events {
		sz := len(ev.Payload)
		if len(current) > 0 && currentSize+sz > maxBytes {
			chunks = append(chunks, current)
			current = nil
			currentSize = 0
		}
		current = append(current, ev)
		currentSize += sz
	}
	if len(current) > 0 {
		chunks = append(chunks, current)
	}
	return chunks
}
