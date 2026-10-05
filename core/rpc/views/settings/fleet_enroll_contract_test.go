package settings

// fleet_enroll_contract_test.go — fleet-session-truth-01DOGF0A WP08.
//
//   P-11  enroll carries the build's version string (it sent "0.18.0").
//   P-9   enroll with empty email → the snapshot falls back to the access
//         token's email/name claims, labelled token_claim.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
)

func jwtWithProfile(sub, org, email, name string) string {
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	claims := map[string]string{"sub": sub, "iss": "https://issuer.test", resourceOwnerClaim: org}
	if email != "" {
		claims["email"] = email
	}
	if name != "" {
		claims["name"] = name
	}
	return enc(map[string]string{"alg": "none"}) + "." + enc(claims) + ".sig"
}

func TestEnroll_SendsTheBuildVersion(t *testing.T) {
	r := newSessionRig(t)
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	r.api.SetFleetClientVersion("v0.85.3")
	if _, err := r.api.FleetRefreshIdentity(context.Background()); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	got := r.fleet.sentVersions()
	if len(got) != 1 || got[0] != "v0.85.3" {
		t.Fatalf("enroll versions = %v, want [v0.85.3] (P-11: was hard-coded \"0.18.0\")", got)
	}
}

func TestEnroll_UnsetVersionIsDevNotAFakeRelease(t *testing.T) {
	r := newSessionRig(t)
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	if _, err := r.api.FleetRefreshIdentity(context.Background()); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if got := r.fleet.sentVersions(); len(got) != 1 || got[0] != "dev" {
		t.Fatalf("enroll versions = %v, want [dev]", got)
	}
}

func TestFleetSession_EmptyEnrollEmail_FallsBackToTokenClaims(t *testing.T) {
	r := newSessionRig(t)
	r.fleet.setEmail("") // dogfood F8a: enroll 200 with email:""
	r.setToken(jwtWithProfile("sub-alice", "zitadel-org-1", "alice@kameas.ai", "Alice Feeman"))
	if _, err := r.api.FleetRefreshIdentity(context.Background()); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	v := snap(t, r.api)
	if v.Identity == nil || v.Identity.Email != "alice@kameas.ai" || v.EmailSource != "token_claim" {
		t.Fatalf("identity=%+v emailSource=%q, want the claim email labelled token_claim", v.Identity, v.EmailSource)
	}
	if v.Identity.DisplayName != "Alice Feeman" || v.NameSource != "token_claim" {
		t.Fatalf("displayName=%q nameSource=%q, want the claim name", v.Identity.DisplayName, v.NameSource)
	}
	if len(v.Identity.Roles) != 1 || v.Identity.Roles[0] != "org_owner" {
		t.Fatalf("roles = %v, want [org_owner] carried through to the snapshot", v.Identity.Roles)
	}
}

func TestFleetSession_EnrollEmailWinsOverClaim(t *testing.T) {
	r := newSessionRig(t)
	r.setToken(jwtWithProfile("sub-alice", "zitadel-org-1", "claim@kameas.ai", ""))
	if _, err := r.api.FleetRefreshIdentity(context.Background()); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if v := snap(t, r.api); v.Identity.Email != "alice@example.com" || v.EmailSource != "enroll" {
		t.Fatalf("email=%q source=%q, want the enroll email", v.Identity.Email, v.EmailSource)
	}
}
