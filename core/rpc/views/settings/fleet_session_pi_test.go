package settings

// fleet_session_pi_test.go — fleet-session-truth-01DOGF0A WP-PI.
//
// Persistence surface this mission makes user-visible: the identity.json
// cache (a FILE, not sqlite). Degraded sessions now SHOW it (FR-3), so a
// cache written by a previous release must (a) parse and render, and (b)
// never surface after sign-out (spec §6). The fixture below is the literal
// on-disk shape fleet.SaveIdentity wrote at v0.85.2 (b8079d48) — the
// file-surface analogue of AC-PI-1's "start from data a previous release
// produced" — not an Identity marshalled by this build.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

const v0852IdentityJSON = `{"user_id":"0e0e0e0e-fleet-internal-user-uuid","org_id":"412","team_id":"t-9","tier":"enterprise","org_name":"Kameas Dogfood","team_name":"Everyone","roles":["org_owner"],"fetched_at":"2026-10-04T19:40:56.123456-04:00"}`

func TestPI_IdentityCacheFromPreviousRelease_ShownWhileDegraded_GoneAfterSignOut(t *testing.T) {
	r := newSessionRig(t)
	dir := filepath.Join(r.dataDir, "fleet")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fleet.IdentityFilePath(r.dataDir), []byte(v0852IdentityJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	// A new process, tokens usable, enroll failing: degraded, showing the
	// previous release's cache.
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	r.fleet.setMode("server_error")
	_, _ = r.api.FleetRefreshIdentity(context.Background())
	v := snap(t, r.api)
	if v.State != FleetSessionDegraded || v.IdentitySource != "cache" || v.Identity == nil {
		t.Fatalf("state=%q source=%q identity=%+v, want degraded showing the cached identity", v.State, v.IdentitySource, v.Identity)
	}
	if v.Identity.OrgName != "Kameas Dogfood" || v.Identity.Tier != "enterprise" ||
		len(v.Identity.Roles) != 1 || v.Identity.Roles[0] != "org_owner" {
		t.Fatalf("cached identity = %+v, want the v0.85.2 fields round-tripped", v.Identity)
	}

	// Sign-out removes the file and the snapshot shows no identity.
	r.setToken("")
	_ = r.api.FleetSignOut(context.Background())
	if _, err := os.Stat(fleet.IdentityFilePath(r.dataDir)); !os.IsNotExist(err) {
		t.Fatalf("identity.json survived sign-out (err=%v)", err)
	}
	if v := snap(t, r.api); v.Identity != nil {
		t.Fatalf("identity shown after sign-out: %+v", v.Identity)
	}
}

func TestPI_CorruptIdentityCache_IsNotAnIdentity(t *testing.T) {
	r := newSessionRig(t)
	_ = os.MkdirAll(filepath.Join(r.dataDir, "fleet"), 0o700)
	_ = os.WriteFile(fleet.IdentityFilePath(r.dataDir), []byte(`{"org_name":`), 0o600)
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	r.fleet.setMode("server_error")
	_, _ = r.api.FleetRefreshIdentity(context.Background())
	v := snap(t, r.api)
	if v.State != FleetSessionDegraded {
		t.Fatalf("state = %q, want degraded (a corrupt cache must not change the session state)", v.State)
	}
	if v.Identity != nil && v.IdentitySource == "cache" {
		t.Fatalf("a corrupt cache rendered as identity: %+v", v.Identity)
	}
}
