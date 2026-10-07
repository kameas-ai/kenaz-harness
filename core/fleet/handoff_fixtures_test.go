package fleet

// handoff_fixtures_test.go — device-keys-handoff-01DEVKH01 AC-7: the
// harness client decodes fleet's real response shapes (fixtures encoded
// from verbatim mirrors of kenaz-fleet's structs — see
// handoff_fixtures_gen_test.go and testdata/handoff/PROVENANCE.md).

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
