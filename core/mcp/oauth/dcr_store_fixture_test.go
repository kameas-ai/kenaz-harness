package oauth

// dcr_store_fixture_test.go — connector-lifecycle-truth-01PMZ303 UNIT-14
// (WP-PI, AC-PI-1).
//
// CLAUDE.md blind spot #3, in its JSON form: a test that only ever loads
// a file it just wrote via Save() is blind to any defect in Load's
// decoding of a file it did not produce itself — the exact shape every
// *real* dcr_clients.json on disk will always be, since a process never
// loads a file in the same call that wrote it.
//
// dcr_clients.json has NO PREVIOUS RELEASE SHAPE to draw a fixture from —
// NewDCRStore had zero production callers before this mission (spec.md
// §11 R-12), so no shipped release has ever written one. AC-PI-1's
// strict "load a fixture a previous release produced" therefore cannot
// be satisfied for this file the way it can for sqlite. What CAN be
// done, and is done here: hand-author the JSON literally (never via
// Save in the same test) in exactly the shape DCRStore.saveFile writes,
// and drive Load against it. This is the closest a JSON-file store gets
// to the sqlite upgrade-path discipline when no prior release exists.
import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// handWrittenDCRFile writes literal JSON to path — never through
// DCRStore.Save — so the test exercises exactly what Load must parse
// from a file this process did not just produce.
func handWrittenDCRFile(t *testing.T, path string, raw string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

// TestDCRStore_Load_HandWrittenFixture_PublicClient is AC-PI-1(a): a
// hand-authored fixture in the exact on-disk shape (one entries map,
// keyed by DCRKey.String(), dcrEntry fields) loads correctly through the
// real DCRStore.Load — never produced by calling Save in this test.
func TestDCRStore_Load_HandWrittenFixture_PublicClient(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dcr_clients.json")
	key := DCRKey{Issuer: "https://as.example.com", Resource: "https://mcp.example.com", Scopes: []string{"repo"}}

	// Hand-authored, matching dcrFile/dcrEntry's json tags exactly.
	// No client_secret_expires_at / has_secret — a public client, the
	// common shape (30 of 36 mcp_oauth recipes use browser_oauth_dcr,
	// none of which ships confidential-client support yet — spec.md
	// §1.12 R-3).
	handWrittenDCRFile(t, path, `{
  "entries": {
    "`+key.String()+`": {
      "client_id": "hand-written-client-id",
      "client_id_issued_at": 1700000000
    }
  }
}`)

	store := NewDCRStore(path, nil, nil)
	got, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(hand-written fixture): %v", err)
	}
	if got.ClientID != "hand-written-client-id" {
		t.Errorf("ClientID = %q, want hand-written-client-id", got.ClientID)
	}
	if got.ClientIDIssuedAt != 1700000000 {
		t.Errorf("ClientIDIssuedAt = %d, want 1700000000", got.ClientIDIssuedAt)
	}
	if got.ClientSecret != "" {
		t.Errorf("ClientSecret = %q, want empty (public client, no HasSecret)", got.ClientSecret)
	}
}

// TestDCRStore_Load_HandWrittenFixture_HasSecretButCredstoreMissing is
// AC-PI-1(b): a hand-authored fixture with has_secret:true whose
// credstore entry is missing must return a clean error, never a silent
// empty-secret credential downgrading a confidential client to public.
func TestDCRStore_Load_HandWrittenFixture_HasSecretButCredstoreMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dcr_clients.json")
	key := DCRKey{Issuer: "https://as.example.com", Resource: "r", Scopes: nil}

	handWrittenDCRFile(t, path, `{
  "entries": {
    "`+key.String()+`": {
      "client_id": "confidential-client-id",
      "has_secret": true
    }
  }
}`)

	// loadFn returns ("", nil) — "no secret exists" — simulating a
	// credstore row that vanished out from under the JSON file (the
	// production newDCRStore closure in core/rpc/views/tools/oauth.go
	// converts ANY resolve failure to ("", nil), per dcr_store.go's own
	// comment on this exact scenario).
	store := NewDCRStore(path, nil, func(string) (string, error) { return "", nil })
	_, err := store.Load(key)
	if !errors.Is(err, ErrDCRNotFound) {
		t.Fatalf("Load with has_secret=true and missing credstore row: got %v, want ErrDCRNotFound (never a silently-downgraded public client)", err)
	}
}

// TestDCRStore_Load_HandWrittenFixture_ExpiredSecretRemovesEntry is
// AC-PI-1(c): a hand-authored fixture whose client_secret_expires_at is
// already in the past takes the ErrDCRExpired path AND its entry is
// removed from the file on disk — asserted by re-reading the file
// (dcr_store.go's own comment: "best-effort; ignore write error here"),
// not by trusting the return value alone.
func TestDCRStore_Load_HandWrittenFixture_ExpiredSecretRemovesEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dcr_clients.json")
	key := DCRKey{Issuer: "https://as.example.com", Resource: "r", Scopes: nil}

	handWrittenDCRFile(t, path, `{
  "entries": {
    "`+key.String()+`": {
      "client_id": "expired-client-id",
      "client_secret_expires_at": 1000
    }
  }
}`)

	store := NewDCRStore(path, nil, nil)
	store.nowFn = func() time.Time { return time.Unix(2000, 0) } // well past 1000

	_, err := store.Load(key)
	if !errors.Is(err, ErrDCRExpired) {
		t.Fatalf("Load(expired fixture): got %v, want ErrDCRExpired", err)
	}

	// Assert the FILE, not the return value: re-read it directly (not
	// through the store, which would just re-report ErrDCRNotFound —
	// that's the behaviour, not the persisted evidence for it).
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read fixture file: %v", err)
	}
	var f struct {
		Entries map[string]json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("unmarshal re-read file: %v", err)
	}
	if _, stillPresent := f.Entries[key.String()]; stillPresent {
		t.Fatalf("expired entry still present in the on-disk file after Load; want it purged (dcr_store.go's expiry-purge write)")
	}
}
