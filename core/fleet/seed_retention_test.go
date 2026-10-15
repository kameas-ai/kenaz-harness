package fleet

// seed_retention_test.go — provider-credentials-client-01PCCLI01 A3: a
// recovery-code import (StoreContextSeed) must keep the replaced seed so
// everything this device encrypted under it stays decryptable here.
//
// Persistence goes through the real core/keyring seam (which selects
// go-keyring's mock backend under `go test`), not a hand-rolled map: the
// base64 encode/decode and account naming are exercised end to end. The
// failure-injection test wraps keyring.Set rather than replacing it.

import (
	"bytes"
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

// clearSeedSlots deletes the current seed and every retained slot from the
// keychain, now and on cleanup (the mock keychain is process-global and
// other tests StoreContextSeed freely).
func clearSeedSlots(t *testing.T) {
	t.Helper()
	wipe := func() {
		svc := paths.FleetKeychainService()
		_ = keyring.Delete(svc, contextSeedAccount)
		for i := 1; i <= maxRetainedSeeds+2; i++ {
			_ = keyring.Delete(svc, retainedSeedAccount(i))
		}
	}
	wipe()
	t.Cleanup(wipe)
}

// testSeed returns a deterministic seed distinct per tag.
func testSeed(tag byte) []byte {
	s := make([]byte, seedSize)
	for i := range s {
		s[i] = tag ^ byte(i*7)
	}
	return s
}

func mustStore(t *testing.T, seed []byte) {
	t.Helper()
	if err := StoreContextSeed(seed); err != nil {
		t.Fatalf("StoreContextSeed: %v", err)
	}
}

func mustSeeds(t *testing.T) [][]byte {
	t.Helper()
	seeds, err := ContextSeedsForDecrypt()
	if err != nil {
		t.Fatalf("ContextSeedsForDecrypt: %v", err)
	}
	return seeds
}

func assertSeeds(t *testing.T, got [][]byte, want ...[]byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("seeds len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("seed[%d] mismatch", i)
		}
	}
}

func TestStoreContextSeed_ImportRetainsPrevious(t *testing.T) {
	clearSeedSlots(t)
	a, b := testSeed(0x11), testSeed(0x22)
	mustStore(t, a) // first-run: nothing to retain
	mustStore(t, b) // recovery-code import over A

	cur, err := LoadContextSeed()
	if err != nil || !bytes.Equal(cur, b) {
		t.Fatalf("current seed after import = %v, want B", err)
	}
	raw, err := keyring.Get(paths.FleetKeychainService(), retainedSeedAccount(1))
	if err != nil {
		t.Fatalf("retained slot 1 read: %v", err)
	}
	if raw != base64.StdEncoding.EncodeToString(a) {
		t.Fatal("retained slot 1 does not hold the replaced seed A")
	}
	if !strings.HasSuffix(retainedSeedAccount(1), ":context_seed_prev.1") {
		t.Fatalf("retained account = %q", retainedSeedAccount(1))
	}
	assertSeeds(t, mustSeeds(t), b, a)
}

func TestStoreContextSeed_SameSeedIsNoOp(t *testing.T) {
	clearSeedSlots(t)
	a := testSeed(0x33)
	mustStore(t, a)

	var sets int
	og := seedKeyringSet
	seedKeyringSet = func(s, k, v string) error { sets++; return og(s, k, v) }
	t.Cleanup(func() { seedKeyringSet = og })

	mustStore(t, a)
	if sets != 0 {
		t.Fatalf("same-seed re-import wrote the keychain %d times, want 0", sets)
	}
	if _, err := keyring.Get(paths.FleetKeychainService(), retainedSeedAccount(1)); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("same-seed re-import created a retained slot (err=%v)", err)
	}
	assertSeeds(t, mustSeeds(t), a)
}

func TestStoreContextSeed_RetentionBoundedAtN(t *testing.T) {
	clearSeedSlots(t)
	var seeds [][]byte
	for i := 0; i < maxRetainedSeeds+2; i++ {
		s := testSeed(byte(0x40 + i))
		seeds = append(seeds, s)
		mustStore(t, s)
	}
	// current = last; retained = the N before it, most recent first; the
	// oldest one is dropped.
	last := len(seeds) - 1
	want := [][]byte{seeds[last]}
	for i := last - 1; i >= last-maxRetainedSeeds; i-- {
		want = append(want, seeds[i])
	}
	assertSeeds(t, mustSeeds(t), want...)
	if _, err := keyring.Get(paths.FleetKeychainService(), retainedSeedAccount(maxRetainedSeeds+1)); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("slot beyond the bound was written (err=%v)", err)
	}
}

func TestStoreContextSeed_ReimportOfRetainedSeedDoesNotDuplicate(t *testing.T) {
	clearSeedSlots(t)
	a, b := testSeed(0x51), testSeed(0x52)
	mustStore(t, a)
	mustStore(t, b) // retained [A]
	mustStore(t, a) // retained [B, A]
	mustStore(t, b) // A already retained: no shift
	assertSeeds(t, mustSeeds(t), b, a)
	if _, err := keyring.Get(paths.FleetKeychainService(), retainedSeedAccount(3)); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("flip-flop import grew the retained list (err=%v)", err)
	}
}

func TestStoreContextSeed_RetentionFailureKeepsOriginal(t *testing.T) {
	clearSeedSlots(t)
	a, b := testSeed(0x61), testSeed(0x62)
	mustStore(t, a)

	og := seedKeyringSet
	seedKeyringSet = func(s, k, v string) error {
		if strings.Contains(k, "_prev.") {
			return errors.New("keychain write denied")
		}
		return og(s, k, v)
	}
	t.Cleanup(func() { seedKeyringSet = og })

	if err := StoreContextSeed(b); err == nil {
		t.Fatal("import must fail when the previous seed cannot be retained")
	}
	cur, err := LoadContextSeed()
	if err != nil || !bytes.Equal(cur, a) {
		t.Fatalf("original seed lost after a retention failure (err=%v)", err)
	}
}

func TestStoreContextSeed_UnreadableKeychainNeverOverwrites(t *testing.T) {
	clearSeedSlots(t)
	a := testSeed(0x71)
	mustStore(t, a)
	og := seedKeyringGet
	seedKeyringGet = func(string, string) (string, error) { return "", errors.New("user interaction is not allowed") }
	t.Cleanup(func() { seedKeyringGet = og })

	if err := StoreContextSeed(testSeed(0x72)); !errors.Is(err, ErrKeychainUnavailable) {
		t.Fatalf("err = %v, want ErrKeychainUnavailable", err)
	}
	seedKeyringGet = og
	cur, err := LoadContextSeed()
	if err != nil || !bytes.Equal(cur, a) {
		t.Fatalf("seed overwritten while the keychain was unreadable (err=%v)", err)
	}
}

// TestContextSeedsForDecrypt_UnreadableRetainedSlotDegradesToCurrent: a
// retained slot the keychain cannot read must not block decrypting data
// written under the current seed.
func TestContextSeedsForDecrypt_UnreadableRetainedSlotDegradesToCurrent(t *testing.T) {
	clearSeedSlots(t)
	a, b := testSeed(0x81), testSeed(0x82)
	mustStore(t, a)
	mustStore(t, b) // a is now retained in slot 1
	og := seedKeyringGet
	seedKeyringGet = func(service, account string) (string, error) {
		if account == retainedSeedAccount(1) {
			return "", errors.New("user interaction is not allowed")
		}
		return og(service, account)
	}
	t.Cleanup(func() { seedKeyringGet = og })

	seeds, err := ContextSeedsForDecrypt()
	if err != nil {
		t.Fatalf("ContextSeedsForDecrypt: %v, want current-only, not an error", err)
	}
	if len(seeds) != 1 || !bytes.Equal(seeds[0], b) {
		t.Fatalf("seeds = %d, want exactly the current seed", len(seeds))
	}
}

// recordingStreamServer stores every appended wire event and serves them
// all back on replay — a minimal fleet context-stream fake.
func recordingStreamServer(t *testing.T) (*httptest.Server, func() []wireEvent) {
	t.Helper()
	var mu sync.Mutex
	var stored []wireEvent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/context/append":
			body, _ := io.ReadAll(r.Body)
			var req appendRequest
			if err := json.Unmarshal(body, &req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			stored = append(stored, req.Events...)
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/context/replay":
			mu.Lock()
			resp := replayResponse{Events: append([]wireEvent(nil), stored...)}
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []wireEvent {
		mu.Lock()
		defer mu.Unlock()
		return append([]wireEvent(nil), stored...)
	}
}

func TestSessionResume_DecryptsPreImportEvents_NewWritesUseNewSeed(t *testing.T) {
	clearSeedSlots(t)
	srv, snapshot := recordingStreamServer(t)
	stubTokens(t, TokenSet{AccessToken: "at-" + "retain", RefreshToken: "rt-" + "retain", ExpiresAt: time.Now().Add(time.Hour)})
	ss := NewSessionSyncer(makeTestClient(t, srv.URL), &fakeEmitter{}, nil)
	sessionID := "seed-retention-session"
	t.Cleanup(func() { _ = setSessionSyncEnabled(sessionID, false) })

	oldSeed, newSeed := testSeed(0x81), testSeed(0x82)
	mustStore(t, oldSeed)
	pre := []byte(`{"role":"user","content":"before import"}`)
	if err := ss.EnableSync(context.Background(), sessionID, []SessionEventRecord{{Seq: 1, Bytes: pre}}); err != nil {
		t.Fatalf("EnableSync: %v", err)
	}

	mustStore(t, newSeed) // recovery-code import

	post := []byte(`{"role":"user","content":"after import"}`)
	if err := ss.AppendEvent(context.Background(), sessionID, SessionEventRecord{Seq: 2, Bytes: post}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	var got [][]byte
	if err := ss.Resume(context.Background(), sessionID, 0, func(r SessionEventRecord) error {
		got = append(got, r.Bytes)
		return nil
	}); err != nil {
		t.Fatalf("Resume after import: %v", err)
	}
	if len(got) != 2 || !bytes.Equal(got[0], pre) || !bytes.Equal(got[1], post) {
		t.Fatalf("Resume replayed %d events, want both pre- and post-import", len(got))
	}

	// The post-import write is under the NEW seed only.
	wires := snapshot()
	if len(wires) != 2 {
		t.Fatalf("stored %d wire events, want 2", len(wires))
	}
	oldKey, _ := DeriveKey(oldSeed, LabelSessionEvents)
	newKey, _ := DeriveKey(newSeed, LabelSessionEvents)
	if _, err := Decrypt(oldKey, wires[1].EncryptedPayload, wires[1].Nonce); !errors.Is(err, ErrDecryptionFailed) {
		t.Fatal("post-import write is decryptable with the OLD seed: encrypt used a retained seed")
	}
	if _, err := Decrypt(newKey, wires[1].EncryptedPayload, wires[1].Nonce); err != nil {
		t.Fatalf("post-import write not decryptable with the new seed: %v", err)
	}
}

func TestProjectResume_DecryptsPreImportEvents(t *testing.T) {
	clearSeedSlots(t)
	srv, _ := recordingStreamServer(t)
	stubTokens(t, TokenSet{AccessToken: "at-" + "retainp", RefreshToken: "rt-" + "retainp", ExpiresAt: time.Now().Add(time.Hour)})
	ps := NewProjectSyncer(makeTestClient(t, srv.URL), &fakeEmitter{}, nil)
	projectID := "seed-retention-project"
	t.Cleanup(func() { _ = setProjectSyncEnabled(projectID, false) })

	mustStore(t, testSeed(0x91))
	pre := []byte(`{"kind":"note","body":"before import"}`)
	if err := ps.EnableSync(context.Background(), projectID, []ProjectEventRecord{{Seq: 1, Bytes: pre}}, ArtifactClassOptions{}); err != nil {
		t.Fatalf("EnableSync: %v", err)
	}
	mustStore(t, testSeed(0x92)) // recovery-code import

	var got [][]byte
	if err := ps.Resume(context.Background(), projectID, 0, func(r ProjectEventRecord) error {
		got = append(got, r.Bytes)
		return nil
	}); err != nil {
		t.Fatalf("project Resume after import: %v", err)
	}
	if len(got) != 1 || !bytes.Equal(got[0], pre) {
		t.Fatalf("project Resume replayed %d events, want the pre-import one", len(got))
	}
}
