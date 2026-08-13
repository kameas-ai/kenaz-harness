package toolloop

// Session grants + headless policy tests
// (confirm-each-enforcement-01PMAG05 WP03 + WP05).

import (
	"sync"
	"testing"
)

func TestSessionGrantCache_ScopedToSessionAndTool(t *testing.T) {
	t.Parallel()

	c := NewSessionGrantCache()
	if c.Has("s1", "fs", "write_file") {
		t.Fatal("empty cache reported a grant")
	}

	c.Grant("s1", "fs", "write_file")
	if !c.Has("s1", "fs", "write_file") {
		t.Fatal("granted tool not reported")
	}
	// A grant is per-tool…
	if c.Has("s1", "fs", "delete_file") {
		t.Error("grant leaked to a sibling tool")
	}
	// …per-server…
	if c.Has("s1", "github", "write_file") {
		t.Error("grant leaked to a different server")
	}
	// …and per-session. This is the one that matters: "allow for this
	// session" must not quietly become "allow everywhere".
	if c.Has("s2", "fs", "write_file") {
		t.Error("grant leaked into another session")
	}
}

func TestSessionGrantCache_NilAndIdempotence(t *testing.T) {
	t.Parallel()

	var nilCache *SessionGrantCache
	// A nil cache reports no grants and tolerates writes: an adapter with
	// grants unwired must prompt, not panic.
	if nilCache.Has("s", "srv", "tool") {
		t.Fatal("nil cache reported a grant")
	}
	nilCache.Grant("s", "srv", "tool")
	if nilCache.Count() != 0 {
		t.Fatal("nil cache counted a grant")
	}

	c := NewSessionGrantCache()
	c.Grant("s", "srv", "tool")
	c.Grant("s", "srv", "tool")
	if got := c.Count(); got != 1 {
		t.Fatalf("Count after duplicate grants = %d, want 1", got)
	}
	// An empty tool name is not a grant — it would match nothing useful
	// and could only ever be a caller bug.
	c.Grant("s", "srv", "")
	if got := c.Count(); got != 1 {
		t.Fatalf("Count after empty-tool grant = %d, want 1", got)
	}
}

func TestSessionGrantCache_RevokeSession(t *testing.T) {
	t.Parallel()

	c := NewSessionGrantCache()
	c.Grant("s1", "fs", "a")
	c.Grant("s1", "fs", "b")
	c.Grant("s2", "fs", "a")

	if n := c.RevokeSession("s1"); n != 2 {
		t.Fatalf("RevokeSession = %d, want 2", n)
	}
	if c.Has("s1", "fs", "a") || c.Has("s1", "fs", "b") {
		t.Error("revoked session still holds grants")
	}
	if !c.Has("s2", "fs", "a") {
		t.Error("revoking one session dropped another session's grants")
	}
}

// The cache is read on the dispatch hot path from a worker pool and
// written from whichever goroutine answered the prompt.
func TestSessionGrantCache_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	c := NewSessionGrantCache()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); c.Grant("s", "srv", "tool") }()
		go func() { defer wg.Done(); _ = c.Has("s", "srv", "tool") }()
	}
	wg.Wait()
	if got := c.Count(); got != 1 {
		t.Fatalf("Count = %d, want 1", got)
	}
}

// Owner decision 4: the default is deny, and "allow" is reachable only by
// an operator typing it. Every unparseable value lands on deny.
func TestParseHeadlessConfirmPolicy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in         string
		want       HeadlessConfirmPolicy
		recognised bool
	}{
		{"deny", HeadlessDeny, true},
		{"allow", HeadlessAllow, true},
		{"ALLOW", HeadlessAllow, true},
		{"  Deny  ", HeadlessDeny, true},
		// Everything below is a misconfiguration, and every one of them
		// resolves to deny. A typo must never be the permissive answer.
		{"", HeadlessDeny, false},
		{"   ", HeadlessDeny, false},
		{"true", HeadlessDeny, false},
		{"yes", HeadlessDeny, false},
		{"1", HeadlessDeny, false},
		{"auto_allow", HeadlessDeny, false},
		{"allow-all", HeadlessDeny, false},
	}
	for _, tc := range cases {
		got, ok := ParseHeadlessConfirmPolicy(tc.in)
		if got != tc.want || ok != tc.recognised {
			t.Errorf("ParseHeadlessConfirmPolicy(%q) = (%q, %v), want (%q, %v)",
				tc.in, got, ok, tc.want, tc.recognised)
		}
	}
}

func TestHeadlessConfirmPolicyFromEnv(t *testing.T) {
	// Not parallel: mutates process env.
	t.Setenv(EnvConfirmEachHeadless, "")
	got, ok, _ := HeadlessConfirmPolicyFromEnv()
	if got != HeadlessDeny || !ok {
		t.Fatalf("unset env = (%q, %v), want (deny, true) — unset is the documented default, not a misconfiguration", got, ok)
	}

	t.Setenv(EnvConfirmEachHeadless, "allow")
	got, ok, _ = HeadlessConfirmPolicyFromEnv()
	if got != HeadlessAllow || !ok {
		t.Fatalf("env=allow = (%q, %v), want (allow, true)", got, ok)
	}

	t.Setenv(EnvConfirmEachHeadless, "sure why not")
	got, ok, raw := HeadlessConfirmPolicyFromEnv()
	if got != HeadlessDeny || ok {
		t.Fatalf("env=garbage = (%q, %v), want (deny, false)", got, ok)
	}
	if raw != "sure why not" {
		t.Fatalf("raw = %q, want the original value so the warning can quote it", raw)
	}
}

// The zero value of the type is deny. A struct field left unset by a
// caller that forgot to configure it must not open the gate.
func TestHeadlessConfirmPolicy_ZeroValueIsNotAllow(t *testing.T) {
	t.Parallel()

	var p HeadlessConfirmPolicy
	if p == HeadlessAllow {
		t.Fatal("the zero value of HeadlessConfirmPolicy is allow — an unconfigured deployment would auto-allow every confirm_each verdict")
	}
}
