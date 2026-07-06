package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// makeTestStream creates an EventStream backed by a test fleet client.
// The key is derived from a fixed seed for determinism.
func makeTestStream(t *testing.T, serverURL, streamID string) *EventStream {
	t.Helper()
	stubTokens(t, TokenSet{
		AccessToken:  "at-test",
		RefreshToken: "rt-test",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, serverURL)
	seed := make([]byte, seedSize)
	for i := range seed {
		seed[i] = byte(i + 7)
	}
	key, err := DeriveKey(seed, LabelSessionEvents)
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	es, err := NewEventStream(c, streamID, key)
	if err != nil {
		t.Fatalf("NewEventStream: %v", err)
	}
	return es
}

// ── Append ─────────────────────────────────────────────────────────────────

func TestEventStream_Append_RoundTrip(t *testing.T) {
	var receivedBodies []appendRequest
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/context/append" {
			var req appendRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			receivedBodies = append(receivedBodies, req)
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	es := makeTestStream(t, srv.URL, "stream-abc")
	events := []Event{
		{Payload: []byte("event one")},
		{Payload: []byte("event two")},
	}
	if err := es.Append(context.Background(), events); err != nil {
		t.Fatalf("Append: %v", err)
	}
	mu.Lock()
	got := receivedBodies
	mu.Unlock()

	if len(got) != 1 {
		t.Fatalf("expected 1 append request, got %d", len(got))
	}
	if got[0].StreamID != "stream-abc" {
		t.Errorf("stream_id = %q, want stream-abc", got[0].StreamID)
	}
	if len(got[0].Events) != 2 {
		t.Fatalf("expected 2 wire events, got %d", len(got[0].Events))
	}
	// Ciphertext must not be empty.
	if len(got[0].Events[0].EncryptedPayload) == 0 {
		t.Error("encrypted_payload must be non-empty")
	}
	// Plaintext must not appear in wire events.
	for _, we := range got[0].Events {
		raw, _ := json.Marshal(we)
		if bytes.Contains(raw, []byte("event one")) || bytes.Contains(raw, []byte("event two")) {
			t.Error("plaintext event payload must not appear in wire events")
		}
	}
}

// ── Replay ─────────────────────────────────────────────────────────────────

func TestEventStream_Replay_RoundTrip(t *testing.T) {
	// Pre-encrypt some events to serve as the fleet's "stored" events.
	seed := make([]byte, seedSize)
	for i := range seed {
		seed[i] = byte(i + 7)
	}
	key, _ := DeriveKey(seed, LabelSessionEvents)

	wantPayloads := [][]byte{
		[]byte("hello"),
		[]byte("world"),
		[]byte("third event"),
	}
	var wireEvents []wireEvent
	for i, p := range wantPayloads {
		ct, nonce, err := Encrypt(key, p)
		if err != nil {
			t.Fatalf("encrypt fixture event %d: %v", i, err)
		}
		wireEvents = append(wireEvents, wireEvent{
			Seq:              uint64(i + 1),
			EncryptedPayload: ct,
			Nonce:            nonce,
		})
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/context/replay" {
			resp := replayResponse{
				Events:  wireEvents,
				HasMore: false,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	es := makeTestStream(t, srv.URL, "stream-replay")
	var got [][]byte
	err := es.Replay(context.Background(), 0, func(ev Event) error {
		got = append(got, ev.Payload)
		return nil
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(got) != len(wantPayloads) {
		t.Fatalf("got %d events, want %d", len(got), len(wantPayloads))
	}
	for i, w := range wantPayloads {
		if !bytes.Equal(got[i], w) {
			t.Errorf("event %d: got %q, want %q", i, got[i], w)
		}
	}
}

// ── Replay from mid-stream ─────────────────────────────────────────────────

func TestEventStream_ReplayFromMid(t *testing.T) {
	seed := make([]byte, seedSize)
	for i := range seed {
		seed[i] = byte(i + 7)
	}
	key, _ := DeriveKey(seed, LabelSessionEvents)

	allEvents := make([]wireEvent, 5)
	for i := range allEvents {
		ct, nonce, _ := Encrypt(key, []byte{byte(i)})
		allEvents[i] = wireEvent{Seq: uint64(i + 1), EncryptedPayload: ct, Nonce: nonce}
	}

	var receivedSince uint64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/context/replay" {
			q := r.URL.Query()
			sinceStr := q.Get("since")
			var since uint64
			if sinceStr != "" {
				_, _ = parseUint64(sinceStr, &since)
			}
			receivedSince = since

			// Return only events after since.
			var filtered []wireEvent
			for _, e := range allEvents {
				if e.Seq > since {
					filtered = append(filtered, e)
				}
			}
			resp := replayResponse{Events: filtered, HasMore: false}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	es := makeTestStream(t, srv.URL, "stream-mid")
	es.SetLastSeq(3)

	var got []uint64
	_ = es.Replay(context.Background(), 3, func(ev Event) error {
		got = append(got, ev.Seq)
		return nil
	})

	if receivedSince != 3 {
		t.Errorf("fleet received since=%d, want 3", receivedSince)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 events from seq 4+5, got %d", len(got))
	}
}

// ── Tampered payload rejected ──────────────────────────────────────────────

func TestEventStream_Replay_TamperedRejected(t *testing.T) {
	seed := make([]byte, seedSize)
	key, _ := DeriveKey(seed, LabelSessionEvents)
	ct, nonce, _ := Encrypt(key, []byte("real"))
	// Tamper with the ciphertext.
	ct[0] ^= 0xFF

	wireEvts := []wireEvent{{Seq: 1, EncryptedPayload: ct, Nonce: nonce}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := replayResponse{Events: wireEvts, HasMore: false}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	es := makeTestStream(t, srv.URL, "stream-tamper")
	err := es.Replay(context.Background(), 0, func(Event) error { return nil })
	if err == nil {
		t.Fatal("expected error for tampered payload")
	}
}

// ── DeleteRemote ───────────────────────────────────────────────────────────

func TestEventStream_DeleteRemote(t *testing.T) {
	var deletedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletedPath = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()

	es := makeTestStream(t, srv.URL, "stream-del")
	if err := es.DeleteRemote(context.Background()); err != nil {
		t.Fatalf("DeleteRemote: %v", err)
	}
	if deletedPath != "/api/v1/context/stream-del" {
		t.Errorf("deleted path = %q, want /api/v1/context/stream-del", deletedPath)
	}
}

// ── chunkedEvents ──────────────────────────────────────────────────────────

func TestChunkedEvents_SmallPayloads(t *testing.T) {
	events := make([]Event, 10)
	for i := range events {
		events[i] = Event{Payload: make([]byte, 100)}
	}
	chunks := chunkedEvents(events, 1000) // 10 × 100 = 1000 → 1 chunk
	if len(chunks) != 1 {
		t.Errorf("got %d chunks, want 1", len(chunks))
	}
}

func TestChunkedEvents_LargePayloads(t *testing.T) {
	events := make([]Event, 3)
	for i := range events {
		events[i] = Event{Payload: make([]byte, 600)}
	}
	// maxBytes=1000: first chunk = events[0]+events[1] (1200 > 1000, so actually events[0] alone since
	// 600+600=1200>1000). Let's verify chunking is sane.
	chunks := chunkedEvents(events, 1000)
	totalEvents := 0
	for _, c := range chunks {
		totalEvents += len(c)
	}
	if totalEvents != 3 {
		t.Errorf("total events across chunks = %d, want 3", totalEvents)
	}
}

// ── helpers ────────────────────────────────────────────────────────────────

// parseUint64 parses s into *dst. Used by the test handler to parse the since= param.
func parseUint64(s string, dst *uint64) (int, error) {
	var v uint64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, nil
		}
		v = v*10 + uint64(c-'0')
	}
	*dst = v
	return len(s), nil
}
