package fleet

// device_keys_test.go — device-keys-handoff-01DEVKH01 WP02 (FR-2, AC-1,
// AC-2 fleet half, fleet answers 2026-10-07).
//
// The fake fleet below answers with the EXACT envelopes kenaz-fleet's
// handlers write (service/device_keys.go writeKeyRegErr /
// handlers_v2.go handleEnroll / handlePutNodeKeys @ 97a1c12):
//
//	403 {"code":"node_removed","message":"this node was removed by an administrator; ..."}
//	409 {"code":"too_many_device_keys","message":"...","details":{"max":16}}
//	400 {"code":"invalid_public_key","message":"handoff_public_key low-order point","details":{"field":"handoff_public_key"}}
//
// and enforces fleet's atomic enroll-with-keys semantics (a key rejection
// fails the WHOLE enroll; the same body without keys succeeds).

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/keyring"
	"github.com/kameas-ai/kenaz-harness/core/paths"
)

type keyFleetReq struct {
	Method string
	Path   string
	Body   map[string]any
}

// keyFleet is a fake fleet for enroll / PUT keys / DELETE node. Race-safe:
// the handler goroutine appends under mu; tests read via snapshot().
type keyFleet struct {
	srv *httptest.Server

	mu       sync.Mutex
	reqs     []keyFleetReq
	keyMode  string // "" ok | "too_many" | "invalid" | "removed"
	putMode  string
	delCode  int
	authSeen []string
}

func newKeyFleet(t *testing.T) *keyFleet {
	t.Helper()
	f := &keyFleet{delCode: http.StatusNoContent}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.reqs = append(f.reqs, keyFleetReq{Method: r.Method, Path: r.URL.Path, Body: body})
		keyMode, putMode, delCode := f.keyMode, f.putMode, f.delCode
		f.mu.Unlock()
		hasKeys := body["handoff_public_key"] != nil || body["signing_public_key"] != nil
		writeEnv := func(status int, code, msg string, details map[string]any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "message": msg, "details": details})
		}
		reject := func(mode string) bool {
			switch mode {
			case "removed":
				writeEnv(http.StatusForbidden, "node_removed",
					"this node was removed by an administrator; enroll under a new node_id after signing in again", nil)
			case "too_many":
				if !hasKeys {
					return false
				}
				writeEnv(http.StatusConflict, "too_many_device_keys",
					"too many active device keys; unenroll an old device first", map[string]any{"max": 16})
			case "invalid":
				if !hasKeys {
					return false
				}
				writeEnv(http.StatusBadRequest, "invalid_public_key", "handoff_public_key low-order point",
					map[string]any{"field": "handoff_public_key"})
			default:
				return false
			}
			return true
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/enroll":
			if reject(keyMode) {
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"org_id": "org-1", "team_id": "t1", "role": "org_member", "tier": "team", "user_id": "u-1",
			})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v1/me/nodes/"):
			if reject(putMode) {
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{
				{"purpose": "handoff", "fingerprint": "sha256:aa", "created_at": time.Now().UTC()},
			}})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v1/me/nodes/"):
			if delCode == http.StatusNotFound {
				writeEnv(http.StatusNotFound, "node_not_found", "node not found", nil)
				return
			}
			w.WriteHeader(delCode)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *keyFleet) set(keyMode, putMode string) {
	f.mu.Lock()
	f.keyMode, f.putMode = keyMode, putMode
	f.mu.Unlock()
}

func (f *keyFleet) snapshot() []keyFleetReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]keyFleetReq(nil), f.reqs...)
}

func keyedClient(t *testing.T, f *keyFleet) (*Client, string) {
	t.Helper()
	stubTokens(t, TokenSet{AccessToken: "at-keys", RefreshToken: "rt-keys", ExpiresAt: time.Now().Add(time.Hour)})
	c := makeTestClient(t, f.srv.URL)
	c.dataDir = t.TempDir()
	return c, c.dataDir
}

func wantHandoffPubB64(t *testing.T, nodeID string) string {
	t.Helper()
	seed, err := LoadContextSeed()
	if err != nil {
		t.Fatal(err)
	}
	priv, err := DeriveHandoffPrivKey(seed, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes())
}

// AC-1: enroll carries both keys (handoff = derived from seed + the SAME
// node id it enrolls, signing = the device ed25519 key); a re-enroll with
// unchanged seed + node id sends identical keys (fleet: no-op).
func TestEnroll_RegistersDeviceKeys(t *testing.T) {
	f := newKeyFleet(t)
	c, dir := keyedClient(t, f)
	if err := StoreContextSeed(seq32()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RefreshIdentity(t.Context(), "node-A", "darwin", "0.93.0"); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if _, err := c.RefreshIdentity(t.Context(), "node-A", "darwin", "0.93.0"); err != nil {
		t.Fatalf("re-enroll: %v", err)
	}
	reqs := f.snapshot()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	signer, err := NewDeviceSigner(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range reqs {
		if got := r.Body["handoff_public_key"]; got != wantHandoffPubB64(t, "node-A") {
			t.Fatalf("enroll %d handoff_public_key = %v", i, got)
		}
		if got := r.Body["signing_public_key"]; got != base64.StdEncoding.EncodeToString(signer.PublicKey()) {
			t.Fatalf("enroll %d signing_public_key = %v", i, got)
		}
	}
	kr := c.KeyRegistration()
	if kr.Status != KeyRegRegistered || kr.Message != "" {
		t.Fatalf("key registration = %+v", kr)
	}
	pubRaw, _ := base64.StdEncoding.DecodeString(wantHandoffPubB64(t, "node-A"))
	if kr.HandoffFingerprint != KeyFingerprint(pubRaw) || kr.SigningFingerprint != signer.PubkeyFingerprint() {
		t.Fatalf("fingerprints = %+v", kr)
	}
}

// OQ-4: enroll mints the context seed when absent so the device can
// receive handoffs even if the user never toggled context sync.
func TestEnroll_MintsSeedWhenAbsent(t *testing.T) {
	f := newKeyFleet(t)
	c, _ := keyedClient(t, f)
	_ = keyring.Delete(paths.FleetKeychainService(), contextSeedAccount)
	if _, err := LoadContextSeed(); !errors.Is(err, ErrContextSeedNotFound) {
		t.Fatalf("precondition: seed should be absent, got %v", err)
	}
	if _, err := c.RefreshIdentity(t.Context(), "node-S", "darwin", "0.93.0"); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if _, err := LoadContextSeed(); err != nil {
		t.Fatalf("enroll must have minted the seed: %v", err)
	}
	if got := f.snapshot()[0].Body["handoff_public_key"]; got != wantHandoffPubB64(t, "node-S") {
		t.Fatalf("handoff key = %v", got)
	}
}

// Fleet answers 2026-10-07: enroll-with-keys is ATOMIC; on 409
// too_many_device_keys / 400 invalid_public_key retry WITHOUT keys so the
// device still enrolls, and record the readable reason.
func TestEnroll_KeyRejection_RetriesWithoutKeys(t *testing.T) {
	for _, tc := range []struct{ mode, status, copyHas string }{
		{"too_many", KeyRegTooManyDevices, "too many devices"},
		{"invalid", KeyRegInvalidKey, "rejected its device key"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			f := newKeyFleet(t)
			f.set(tc.mode, "")
			c, _ := keyedClient(t, f)
			_ = StoreContextSeed(seq32())
			id, err := c.RefreshIdentity(t.Context(), "node-R", "darwin", "0.93.0")
			if err != nil {
				t.Fatalf("enroll must still succeed without keys: %v", err)
			}
			if id.OrgID != "org-1" {
				t.Fatalf("identity = %+v", id)
			}
			reqs := f.snapshot()
			if len(reqs) != 2 {
				t.Fatalf("POSTs = %d, want exactly 2 (with keys, then without)", len(reqs))
			}
			if reqs[0].Body["handoff_public_key"] == nil {
				t.Fatal("first enroll must carry keys")
			}
			if reqs[1].Body["handoff_public_key"] != nil || reqs[1].Body["signing_public_key"] != nil {
				t.Fatalf("retry must omit both keys: %v", reqs[1].Body)
			}
			if reqs[1].Body["node_id"] != "node-R" {
				t.Fatalf("retry body = %v", reqs[1].Body)
			}
			kr := c.KeyRegistration()
			if kr.Status != tc.status || !strings.Contains(kr.Message, tc.copyHas) {
				t.Fatalf("key registration = %+v", kr)
			}
		})
	}
}

// 403 node_removed → the typed sentinel, one request, no retry loop.
func TestEnroll_NodeRemoved_IsTypedSentinel(t *testing.T) {
	f := newKeyFleet(t)
	f.set("removed", "")
	c, _ := keyedClient(t, f)
	_ = StoreContextSeed(seq32())
	_, err := c.RefreshIdentity(t.Context(), "node-X", "darwin", "0.93.0")
	if !errors.Is(err, ErrNodeRemoved) {
		t.Fatalf("err = %v, want ErrNodeRemoved", err)
	}
	if n := len(f.snapshot()); n != 1 {
		t.Fatalf("requests = %d, want 1 (no retry on node_removed)", n)
	}
}

// PUT /me/nodes/{id}/keys: rotation after a recovery import; same 403/409
// handling.
func TestRegisterDeviceKeys_PUT(t *testing.T) {
	f := newKeyFleet(t)
	c, _ := keyedClient(t, f)
	_ = StoreContextSeed(seq32())
	if err := c.RegisterDeviceKeys(t.Context(), "node P/1"); err != nil {
		t.Fatalf("PUT keys: %v", err)
	}
	r := f.snapshot()[0]
	if r.Method != http.MethodPut || r.Path != "/api/v1/me/nodes/node P/1/keys" {
		t.Fatalf("request = %s %s", r.Method, r.Path)
	}
	if r.Body["handoff_public_key"] != wantHandoffPubB64(t, "node P/1") || r.Body["signing_public_key"] == nil {
		t.Fatalf("PUT body = %v", r.Body)
	}
	if c.KeyRegistration().Status != KeyRegRegistered {
		t.Fatalf("status = %+v", c.KeyRegistration())
	}

	f.set("", "too_many")
	if err := c.RegisterDeviceKeys(t.Context(), "node-P"); !errors.Is(err, ErrDeviceKeysRejected) || !strings.Contains(err.Error(), "too many devices") {
		t.Fatalf("409: err = %v", err)
	}
	f.set("", "removed")
	if err := c.RegisterDeviceKeys(t.Context(), "node-P"); !errors.Is(err, ErrNodeRemoved) {
		t.Fatalf("403: err = %v", err)
	}
}

func TestUnenrollNode(t *testing.T) {
	f := newKeyFleet(t)
	c, _ := keyedClient(t, f)
	if err := c.UnenrollNode(context.Background(), "node-U"); err != nil {
		t.Fatalf("204: %v", err)
	}
	if r := f.snapshot()[0]; r.Method != http.MethodDelete || r.Path != "/api/v1/me/nodes/node-U" {
		t.Fatalf("request = %s %s", r.Method, r.Path)
	}
	f.mu.Lock()
	f.delCode = http.StatusNotFound
	f.mu.Unlock()
	if err := c.UnenrollNode(context.Background(), "node-U"); err != nil {
		t.Fatalf("404 node_not_found is idempotent success: %v", err)
	}
	f.mu.Lock()
	f.delCode = http.StatusInternalServerError
	f.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.UnenrollNode(ctx, "node-U"); err == nil {
		t.Fatal("500 must be an error")
	}
}
