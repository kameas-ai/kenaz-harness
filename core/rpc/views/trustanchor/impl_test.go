package trustanchor_test

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/rpc/views/trustanchor"
	"github.com/kameas-ai/kenaz-harness/core/trust"
)

func TestNilEngine_DegradesGracefully(t *testing.T) {
	api := trustanchor.New(nil)

	got, err := api.ListAnchors(context.Background())
	if err != nil {
		t.Fatalf("ListAnchors: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListAnchors on nil engine = %v, want empty", got)
	}

	if _, err := api.InstallAnchor(context.Background(), trustanchor.InstallAnchorRequest{
		AnchorID: "x", KeyB64: base64.StdEncoding.EncodeToString(make([]byte, 32)),
	}); err == nil {
		t.Fatal("expected InstallAnchor on nil engine to error, not panic or silently succeed")
	}
}

func TestInstallAnchor_ThenListAnchors_RoundTrips(t *testing.T) {
	engine, err := trust.NewEngine(trust.Config{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	api := trustanchor.New(engine)

	keyBytes := make([]byte, 32)
	keyBytes[0] = 0x01
	req := trustanchor.InstallAnchorRequest{
		AnchorID:  "anchor-1",
		Kind:      "pinned_peer",
		PeerID:    "peer-1",
		Algorithm: "ed25519",
		KeyB64:    base64.StdEncoding.EncodeToString(keyBytes),
	}

	installed, err := api.InstallAnchor(context.Background(), req)
	if err != nil {
		t.Fatalf("InstallAnchor: %v", err)
	}
	if installed.AnchorID != "anchor-1" {
		t.Errorf("AnchorID=%q, want anchor-1", installed.AnchorID)
	}
	if installed.Kind != "pinned_peer" {
		t.Errorf("Kind=%q, want pinned_peer", installed.Kind)
	}
	if installed.PublicKey.Fingerprint == "" {
		t.Error("Fingerprint is empty — should be server-computed from the key bytes")
	}
	if installed.PublicKey.KeyB64 != req.KeyB64 {
		t.Errorf("KeyB64 round-trip mismatch: got=%q want=%q", installed.PublicKey.KeyB64, req.KeyB64)
	}

	list, err := api.ListAnchors(context.Background())
	if err != nil {
		t.Fatalf("ListAnchors: %v", err)
	}
	found := false
	for _, a := range list {
		if a.AnchorID == "anchor-1" {
			found = true
			if a.Removed {
				t.Error("Removed=true for a freshly installed anchor")
			}
		}
	}
	if !found {
		t.Fatal("installed anchor not present in ListAnchors")
	}
}

func TestInstallAnchor_MissingFields(t *testing.T) {
	engine, err := trust.NewEngine(trust.Config{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	api := trustanchor.New(engine)

	if _, err := api.InstallAnchor(context.Background(), trustanchor.InstallAnchorRequest{}); err == nil {
		t.Fatal("expected error for empty AnchorID/KeyB64")
	}
	if _, err := api.InstallAnchor(context.Background(), trustanchor.InstallAnchorRequest{AnchorID: "a"}); err == nil {
		t.Fatal("expected error for missing KeyB64")
	}
	if _, err := api.InstallAnchor(context.Background(), trustanchor.InstallAnchorRequest{
		AnchorID: "a", KeyB64: "not-valid-base64!!!",
	}); err == nil {
		t.Fatal("expected error for invalid base64 KeyB64")
	}
}

// TestInstallAnchor_DefaultsAlgorithmAndKind confirms an operator who
// omits Algorithm/Kind gets the documented defaults (ed25519,
// raw_public_key) rather than a zero-value that silently fails deeper
// in the verify pipeline.
func TestInstallAnchor_DefaultsAlgorithmAndKind(t *testing.T) {
	engine, err := trust.NewEngine(trust.Config{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	api := trustanchor.New(engine)

	got, err := api.InstallAnchor(context.Background(), trustanchor.InstallAnchorRequest{
		AnchorID: "defaults-anchor",
		KeyB64:   base64.StdEncoding.EncodeToString(make([]byte, 32)),
	})
	if err != nil {
		t.Fatalf("InstallAnchor: %v", err)
	}
	if got.Algorithm != "ed25519" {
		t.Errorf("Algorithm=%q, want ed25519 default", got.Algorithm)
	}
	if got.Kind != "raw_public_key" {
		t.Errorf("Kind=%q, want raw_public_key default", got.Kind)
	}
}
