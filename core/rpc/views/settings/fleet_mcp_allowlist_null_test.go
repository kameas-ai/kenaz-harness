package settings

// Pins the MCP allowlist null-vs-[] decode distinction ahead of the
// first-ever fleet bundle delivery.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

// TestBundle_MCPAllowlist_NullVsEmptyDecode pins a distinction that is
// correct today and becomes load-bearing on the first-ever bundle delivery:
//
//   - "mcp_allowlist": null, or the key absent → nil slice → the applier
//     SKIPS the section → no fleet restriction (every recipe allowed).
//   - "mcp_allowlist": []                      → non-nil EMPTY slice → the
//     applier installs it → BLOCK-ALL (no recipe allowed).
//
// Do not "simplify" either side (e.g. len()==0 checks, omitempty on the
// field, or nil-coalescing on decode): collapsing them turns an org's
// "block every MCP recipe" into "allow every MCP recipe".
func TestBundle_MCPAllowlist_NullVsEmptyDecode(t *testing.T) {
	if recipes.AllowlistSealed() {
		t.Skip("global allow-list sealed (served mode) in this test binary")
	}
	reset := func() { recipes.ApplyFleetAllowlist(nil) }
	reset()
	t.Cleanup(reset)

	applier := &compositeConfigApplier{state: &fleetState{}}
	cases := []struct {
		name      string
		json      string
		wantNil   bool
		wantAllow bool // is an arbitrary recipe allowed after apply?
	}{
		{name: "null", json: `{"bundle_id":1,"mcp_allowlist":null}`, wantNil: true, wantAllow: true},
		{name: "absent", json: `{"bundle_id":1}`, wantNil: true, wantAllow: true},
		{name: "empty array", json: `{"bundle_id":1,"mcp_allowlist":[]}`, wantNil: false, wantAllow: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reset()
			var b fleet.Bundle
			if err := json.Unmarshal([]byte(tc.json), &b); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if (b.MCPAllowlist == nil) != tc.wantNil {
				t.Fatalf("decoded MCPAllowlist nil=%v, want nil=%v", b.MCPAllowlist == nil, tc.wantNil)
			}
			if !tc.wantNil && len(b.MCPAllowlist) != 0 {
				t.Fatalf("[] must decode to an EMPTY slice, got %v", b.MCPAllowlist)
			}
			if errs := applier.ApplyBundle(context.Background(), &b); len(errs) != 0 {
				t.Fatalf("apply: %v", errs)
			}
			if got := recipes.IsAllowed("github"); got != tc.wantAllow {
				t.Errorf("IsAllowed(github) after apply = %v, want %v", got, tc.wantAllow)
			}
		})
	}

	// The applier SKIPS a nil section rather than clearing: a prior
	// restriction survives a bundle whose mcp_allowlist is null/absent.
	recipes.ApplyFleetAllowlist([]string{"slack"})
	var b fleet.Bundle
	if err := json.Unmarshal([]byte(`{"bundle_id":2,"mcp_allowlist":null}`), &b); err != nil {
		t.Fatal(err)
	}
	if errs := applier.ApplyBundle(context.Background(), &b); len(errs) != 0 {
		t.Fatalf("apply: %v", errs)
	}
	if recipes.IsAllowed("github") || !recipes.IsAllowed("slack") {
		t.Error("a null mcp_allowlist must skip the section (prior [slack] restriction kept), not overwrite it")
	}

	// The distinction is covered by the signature: null and [] produce
	// different signed payloads.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signedNull := &fleet.Bundle{BundleID: 3}
	if err := fleet.SignBundleForTesting(signedNull, priv); err != nil {
		t.Fatal(err)
	}
	asEmpty := *signedNull
	asEmpty.MCPAllowlist = []string{}
	if err := fleet.Verify(&asEmpty, pub, 0); err == nil {
		t.Error("rewriting mcp_allowlist null → [] in transit must break the signature")
	}
}
