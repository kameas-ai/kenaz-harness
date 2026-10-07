package fleet

// handoff_fixtures_test.go — device-keys-handoff-01DEVKH01 AC-7: the
// harness client decodes fleet's real response shapes (fixtures encoded
// from verbatim mirrors of kenaz-fleet's structs — see
// handoff_fixtures_gen_test.go and testdata/handoff/PROVENANCE.md).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "handoff", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return b
}

// fixtureServer answers every request with (status, fixture body, JSON).
func fixtureServer(t *testing.T, status int, name string) *httptest.Server {
	t.Helper()
	body := readFixture(t, name)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHandoffFleetFixtures_PublicKeySet(t *testing.T) {
	withExternalToken(t, "tok")
	srv := fixtureServer(t, http.StatusOK, "public_key_set.json")
	h := NewHandoffHandler(makeTestClient(t, srv.URL), nil, nil)
	keys, err := h.fetchRecipientKeys(context.Background(), fxRecipientUser)
	if err != nil {
		t.Fatal(err)
	}
	want := fxKeySet(t)
	if len(keys) != 2 {
		t.Fatalf("keys = %d, want the full set of 2 (encrypt to ALL)", len(keys))
	}
	for i := range want {
		if keys[i].KeyID != want[i].KeyID || keys[i].NodeID != want[i].NodeID ||
			string(keys[i].PublicKey) != string(want[i].PublicKey) || keys[i].Fingerprint != want[i].Fingerprint ||
			!keys[i].CreatedAt.Equal(want[i].CreatedAt) {
			t.Fatalf("key %d = %+v", i, keys[i])
		}
	}
	if err := validateKeySet(keys); err != nil {
		t.Fatalf("fixture key set must validate: %v", err)
	}
	devs, err := h.RecipientDevices(context.Background(), fxRecipientUser)
	if err != nil || len(devs) != 2 || devs[1].Fingerprint != want[1].Fingerprint {
		t.Fatalf("RecipientDevices = %+v, %v", devs, err)
	}
}

func TestHandoffFleetFixtures_PublicKeyNotFound(t *testing.T) {
	withExternalToken(t, "tok")
	srv := fixtureServer(t, http.StatusNotFound, "public_key_not_found.json")
	h := NewHandoffHandler(makeTestClient(t, srv.URL), nil, nil)
	if _, err := h.fetchRecipientKeys(context.Background(), fxRecipientUser); !errors.Is(err, ErrHandoffRecipientNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateKeySet_RejectsCorruptEntries(t *testing.T) {
	good := fxKeySet(t)
	conv := func(in []fleetPublicKeyEntry) []publicKeyEntry {
		out := make([]publicKeyEntry, len(in))
		for i, k := range in {
			out[i] = publicKeyEntry{KeyID: k.KeyID, NodeID: k.NodeID, PublicKey: k.PublicKey, Fingerprint: k.Fingerprint}
		}
		return out
	}
	bad := conv(good)
	bad[1].Fingerprint = good[0].Fingerprint
	if err := validateKeySet(bad); !errors.Is(err, ErrHandoffRecipientKeyInvalid) {
		t.Fatalf("fingerprint mismatch: %v", err)
	}
	bad = conv(good)
	bad[1].KeyID = bad[0].KeyID
	if err := validateKeySet(bad); !errors.Is(err, ErrHandoffRecipientKeyInvalid) {
		t.Fatalf("duplicate key_id: %v", err)
	}
	bad = conv(good)
	bad[0].PublicKey = bad[0].PublicKey[:31]
	if err := validateKeySet(bad); !errors.Is(err, ErrHandoffRecipientKeyInvalid) {
		t.Fatalf("short key: %v", err)
	}
}

func TestHandoffFleetFixtures_Send(t *testing.T) {
	var ok handoffSendResponse
	if err := json.Unmarshal(readFixture(t, "send_success.json"), &ok); err != nil || ok.InboxItemID == "" || ok.ExpiresAt.IsZero() {
		t.Fatalf("send success = %+v, %v", ok, err)
	}
	staleBody := readFixture(t, "send_recipient_keys_stale.json")
	var d staleKeysDetails
	if err := json.Unmarshal(staleBody, &d); err != nil || len(d.Details.PublicKeys) != 2 {
		t.Fatalf("stale details = %+v, %v", d, err)
	}
	if err := validateKeySet(d.Details.PublicKeys); err != nil {
		t.Fatalf("stale details keys must be usable for the re-wrap: %v", err)
	}
	if he := mapHandoffHTTPError(http.StatusConflict, http.Header{}, staleBody); he.Code != "recipient_keys_stale" {
		t.Fatalf("stale code = %q", he.Code)
	}
	if he := mapHandoffHTTPError(http.StatusUnprocessableEntity, http.Header{}, readFixture(t, "send_handoff_empty.json")); he.Code != "handoff_empty" || !strings.Contains(he.Error(), "no messages") {
		t.Fatalf("empty = %+v", he)
	}
	h := http.Header{}
	h.Set("Retry-After", "1800")
	if he := mapHandoffHTTPError(http.StatusTooManyRequests, h, readFixture(t, "send_rate_limited.json")); !strings.Contains(he.Error(), "30 minutes") {
		t.Fatalf("rate limited copy = %q", he.Error())
	}
}

func TestHandoffFleetFixtures_InboxAndAccept(t *testing.T) {
	withExternalToken(t, "tok")
	inboxSrv := fixtureServer(t, http.StatusOK, "inbox.json")
	h := NewHandoffHandler(makeTestClient(t, inboxSrv.URL), nil, nil)
	items, err := h.Inbox(context.Background())
	if err != nil || len(items) != 2 || items[0].Undecryptable || !items[1].Undecryptable || items[0].SenderEmail == "" {
		t.Fatalf("inbox = %+v, %v", items, err)
	}
	for _, tc := range []struct{ fixture, node string }{
		{"handoff_wrapped.json", fxNodeA},
		{"handoff_wrapped.json", fxNodeB},
		{"handoff_direct.json", fxNodeA},
	} {
		srv := fixtureServer(t, http.StatusOK, tc.fixture)
		fake := &fakeV2Fleet{srv: srv}
		got, err := recipientDevice(t, fake, tc.node).AcceptShare(context.Background(), "x")
		if err != nil {
			t.Fatalf("%s on %s: %v", tc.fixture, tc.node, err)
		}
		if len(got.Events) != len(fxPlainEvents) || string(got.Events[1].Bytes) != fxPlainEvents[1] || got.SessionID != fxSessionID {
			t.Fatalf("%s on %s: %+v", tc.fixture, tc.node, got)
		}
	}
	direct := &fakeV2Fleet{srv: fixtureServer(t, http.StatusOK, "handoff_direct.json")}
	if _, err := recipientDevice(t, direct, fxNodeB).AcceptShare(context.Background(), "x"); !errors.Is(err, ErrHandoffNotForThisDevice) {
		t.Fatalf("direct item on device B: %v", err)
	}
	gone := &fakeV2Fleet{srv: fixtureServer(t, http.StatusNotFound, "handoff_not_found.json")}
	if _, err := recipientDevice(t, gone, fxNodeA).AcceptShare(context.Background(), "x"); !errors.Is(err, ErrHandoffItemGone) {
		t.Fatalf("not found: %v", err)
	}
}
