package fleet

// handoff_fake_fleet_test.go — an in-memory fake of kenaz-fleet's handoff
// + identity routes for device-keys-handoff-01DEVKH01 WP04/WP05 tests.
//
// It enforces the server rules a client can get wrong (mirroring
// service/handlers_handoff.go @ 97a1c12): v2 recipients[] must EQUAL the
// recipient's active key set (else 409 recipient_keys_stale with
// details.public_keys), wrapped_key exactly 48 B, wrap_nonce/nonce exactly
// 24 B, key_id a unique UUID, unique ephemerals, ≤16 recipients, non-empty
// events (422 handoff_empty), client_handoff_id dedupe; GET returns
// recipients ordered by fingerprint with mode wrapped|direct; inbox marks
// undecryptable when no wrap targets an active key; DELETE is idempotent.
// Race-safe: handler goroutines mutate under mu; tests read through
// accessor methods.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeStoredItem struct {
	ID         string
	SessionID  string
	Sender     string
	Recipient  string
	Mode       string
	EphTop     []byte
	Recipients []fleetHandoffRecipientOut
	Events     json.RawMessage
	Fetched    bool
	Deleted    bool
	ClientID   string
	ReceivedAt time.Time
}

type fakeV2Fleet struct {
	srv *httptest.Server

	mu         sync.Mutex
	keys       map[string][]fleetPublicKeyEntry // user → active keys (newest first)
	items      []*fakeStoredItem
	sends      int
	sendBodies [][]byte
	// staleOnce: answer the next send with 409 recipient_keys_stale even
	// if correct, after swapping the key set to staleNext (a rotation
	// racing the send).
	staleNext []fleetPublicKeyEntry
	// forceErr: next send answers this (status, body, retryAfter).
	forceStatus int
	forceBody   string
	forceRetry  string
	forceLeft   int
	deletes     []string
	gets        int
	getStatus   int // 0 = normal
}

const fakeSender = fxSenderUser

func newFakeV2Fleet(t *testing.T) *fakeV2Fleet {
	t.Helper()
	f := &fakeV2Fleet{keys: map[string][]fleetPublicKeyEntry{}}
	mux := http.NewServeMux()
	writeErr := func(w http.ResponseWriter, status int, code, msg string, details map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(fleetErrorResponse{Code: code, Message: msg, Details: details})
	}
	mux.HandleFunc("GET /api/v1/identity/public-key", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		keys := f.keys[r.URL.Query().Get("user_id")]
		f.mu.Unlock()
		if len(keys) == 0 {
			writeErr(w, http.StatusNotFound, "public_key_not_found", "no public key for that user", nil)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fleetPublicKeyResponse{
			UserID: r.URL.Query().Get("user_id"), PublicKey: keys[0].PublicKey, Fingerprint: keys[0].Fingerprint, PublicKeys: keys,
		})
	})
	mux.HandleFunc("POST /api/v1/handoff/send", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			SessionID       string `json:"session_id"`
			RecipientUserID string `json:"recipient_user_id"`
			ClientHandoffID string `json:"client_handoff_id"`
			Recipients      []struct {
				KeyID              string `json:"key_id"`
				EphemeralPublicKey []byte `json:"ephemeral_public_key"`
				WrappedKey         []byte `json:"wrapped_key"`
				WrapNonce          []byte `json:"wrap_nonce"`
			} `json:"recipients"`
			EphemeralPubKey []byte `json:"ephemeral_public_key"`
			Events          []struct {
				Seq              uint64 `json:"seq"`
				EncryptedPayload []byte `json:"encrypted_payload"`
				Nonce            []byte `json:"nonce"`
			} `json:"events"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", "request body could not be decoded as JSON", nil)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.sends++
		f.sendBodies = append(f.sendBodies, body)
		if f.forceStatus != 0 {
			st, b, ra := f.forceStatus, f.forceBody, f.forceRetry
			if f.forceLeft--; f.forceLeft <= 0 {
				f.forceStatus = 0
			}
			if ra != "" {
				w.Header().Set("Retry-After", ra)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(st)
			_, _ = w.Write([]byte(b))
			return
		}
		if len(req.Events) == 0 {
			writeErr(w, http.StatusUnprocessableEntity, "handoff_empty", "a handoff must contain at least one event", nil)
			return
		}
		for _, e := range req.Events {
			if len(e.Nonce) != 24 || len(e.EncryptedPayload) < 16 {
				writeErr(w, http.StatusBadRequest, "invalid_request", "events: nonce must be 24 bytes", nil)
				return
			}
		}
		if len(req.Recipients) == 0 {
			writeErr(w, http.StatusBadRequest, "invalid_request", "v1 sends are retired in this fake", nil)
			return
		}
		if len(req.Recipients) > 16 {
			writeErr(w, http.StatusBadRequest, "invalid_request", "too many recipients entries", nil)
			return
		}
		seenK, seenE := map[string]bool{}, map[string]bool{}
		for _, rc := range req.Recipients {
			if _, err := uuid.Parse(rc.KeyID); err != nil || seenK[rc.KeyID] || seenE[string(rc.EphemeralPublicKey)] ||
				len(rc.WrappedKey) != 48 || len(rc.WrapNonce) != 24 || len(rc.EphemeralPublicKey) != 32 {
				writeErr(w, http.StatusBadRequest, "invalid_request", "recipients: bad entry", nil)
				return
			}
			seenK[rc.KeyID], seenE[string(rc.EphemeralPublicKey)] = true, true
		}
		if f.staleNext != nil {
			f.keys[req.RecipientUserID] = f.staleNext
			f.staleNext = nil
		}
		keys := f.keys[req.RecipientUserID]
		if len(keys) == 0 {
			writeErr(w, http.StatusConflict, "recipient_no_key", "recipient has no active handoff key", nil)
			return
		}
		byID := map[string]fleetPublicKeyEntry{}
		for _, k := range keys {
			byID[k.KeyID] = k
		}
		stale := len(req.Recipients) != len(keys)
		for _, rc := range req.Recipients {
			if _, ok := byID[rc.KeyID]; !ok {
				stale = true
			}
		}
		if stale {
			writeErr(w, http.StatusConflict, "recipient_keys_stale", "recipient key set changed; re-wrap to the current keys",
				map[string]any{"public_keys": keys})
			return
		}
		for _, it := range f.items {
			if req.ClientHandoffID != "" && it.ClientID == req.ClientHandoffID && !it.Deleted {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(fleetHandoffSendResponse{InboxItemID: it.ID, ExpiresAt: it.ReceivedAt.Add(7 * 24 * time.Hour)})
				return
			}
		}
		evs, _ := json.Marshal(req.Events)
		it := &fakeStoredItem{
			ID: uuid.NewString(), SessionID: req.SessionID, Sender: fakeSender, Recipient: req.RecipientUserID,
			Mode: "wrapped", Events: evs, ClientID: req.ClientHandoffID, ReceivedAt: time.Now().UTC(),
		}
		for _, rc := range req.Recipients {
			it.Recipients = append(it.Recipients, fleetHandoffRecipientOut{
				KeyID: rc.KeyID, Fingerprint: byID[rc.KeyID].Fingerprint,
				EphemeralPublicKey: rc.EphemeralPublicKey, WrappedKey: rc.WrappedKey, WrapNonce: rc.WrapNonce,
			})
		}
		sort.Slice(it.Recipients, func(i, j int) bool { return it.Recipients[i].Fingerprint < it.Recipients[j].Fingerprint })
		f.items = append(f.items, it)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fleetHandoffSendResponse{InboxItemID: it.ID, ExpiresAt: it.ReceivedAt.Add(7 * 24 * time.Hour)})
	})
	mux.HandleFunc("GET /api/v1/handoff/inbox", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		out := []fleetHandoffInboxItem{}
		for i := len(f.items) - 1; i >= 0; i-- {
			it := f.items[i]
			if it.Deleted {
				continue
			}
			active := map[string]bool{}
			for _, k := range f.keys[it.Recipient] {
				active[k.KeyID] = true
			}
			undec := true
			for _, rc := range it.Recipients {
				if active[rc.KeyID] {
					undec = false
				}
			}
			out = append(out, fleetHandoffInboxItem{InboxItemID: it.ID, SessionID: it.SessionID, SenderUserID: it.Sender,
				SenderEmail: "alice@example.com", ReceivedAt: it.ReceivedAt, Undecryptable: undec})
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("GET /api/v1/handoff/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.gets++
		if f.getStatus == http.StatusTooManyRequests {
			f.getStatus = 0
			f.mu.Unlock()
			w.Header().Set("Retry-After", "1")
			writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many handoff fetches; retry shortly", nil)
			return
		}
		var hit *fakeStoredItem
		for _, it := range f.items {
			if it.ID == r.PathValue("id") && !it.Deleted {
				hit = it
				it.Fetched = true
			}
		}
		f.mu.Unlock()
		if hit == nil {
			writeErr(w, http.StatusNotFound, "handoff_not_found", "handoff not found", nil)
			return
		}
		out := fleetHandoffGetResponse{InboxItemID: hit.ID, SessionID: hit.SessionID, SenderUserID: hit.Sender,
			Mode: hit.Mode, Recipients: hit.Recipients, Events: hit.Events}
		if hit.Mode == "direct" {
			out.EphemeralPublicKey = hit.EphTop
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("DELETE /api/v1/handoff/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.deletes = append(f.deletes, r.PathValue("id"))
		for _, it := range f.items {
			if it.ID == r.PathValue("id") {
				it.Deleted = true
			}
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeV2Fleet) setKeys(user string, keys []fleetPublicKeyEntry) {
	f.mu.Lock()
	f.keys[user] = keys
	f.mu.Unlock()
}

func (f *fakeV2Fleet) rotateBeforeNextSend(keys []fleetPublicKeyEntry) {
	f.mu.Lock()
	f.staleNext = keys
	f.mu.Unlock()
}

func (f *fakeV2Fleet) forceNext(status int, body, retryAfter string) {
	f.forceN(status, body, retryAfter, 1)
}

// forceN answers the next n sends with (status, body, retryAfter).
func (f *fakeV2Fleet) forceN(status int, body, retryAfter string, n int) {
	f.mu.Lock()
	f.forceStatus, f.forceBody, f.forceRetry, f.forceLeft = status, body, retryAfter, n
	f.mu.Unlock()
}

func (f *fakeV2Fleet) sendCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.sends }
func (f *fakeV2Fleet) lastSendBody() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sendBodies) == 0 {
		return nil
	}
	return f.sendBodies[len(f.sendBodies)-1]
}
func (f *fakeV2Fleet) deleted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deletes...)
}
func (f *fakeV2Fleet) getCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.gets }
func (f *fakeV2Fleet) item(id string) *fakeStoredItem {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, it := range f.items {
		if it.ID == id {
			cp := *it
			return &cp
		}
	}
	return nil
}

// addDirectItem stores a legacy v1 "direct" item (what a v0.91 sender
// produced for a single-key recipient).
func (f *fakeV2Fleet) addDirectItem(it *fakeStoredItem) {
	f.mu.Lock()
	f.items = append(f.items, it)
	f.mu.Unlock()
}

// keyIDs reads the recipient key ids a send body addressed.
func sendBodyKeyIDs(body []byte) []string {
	var req struct {
		Recipients []struct {
			KeyID string `json:"key_id"`
		} `json:"recipients"`
	}
	_ = json.Unmarshal(body, &req)
	var out []string
	for _, r := range req.Recipients {
		out = append(out, r.KeyID)
	}
	sort.Strings(out)
	return out
}
