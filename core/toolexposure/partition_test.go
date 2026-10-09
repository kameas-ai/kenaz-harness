package toolexposure

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
)

type countingSettings struct {
	s     Settings
	reads atomic.Int32
}

func (c *countingSettings) GetToolExposure(context.Context) (Settings, error) {
	c.reads.Add(1)
	return c.s, nil
}

type stateSessions struct{ st SessionState }

func (s stateSessions) SessionToolExposure(_ context.Context, id string) (SessionState, error) {
	if id == "" {
		return SessionState{}, errors.New("no session")
	}
	return s.st, nil
}

type mapProjects map[string]Exposure

func (m mapProjects) ProjectToolExposure(_ context.Context, id string) (Exposure, error) {
	return m[id], nil
}

func names(ts []ResolvedTool) []string {
	out := []string{}
	for _, t := range ts {
		out = append(out, t.Name)
	}
	return out
}

// TestPartition_SegmentsAndOrder: hot (default full) and pinned (full by
// a layer, or sticky) are alphabetical; non-sticky activations are most
// recently used first; summary tools that are not activated are the
// digest; off tools and stopped probes are never sent.
func TestPartition_SegmentsAndOrder(t *testing.T) {
	catalog := []CatalogTool{
		{Name: "kenaz__read_file", Server: BuiltinServer, Running: true},
		{Name: LoadToolsName, Server: BuiltinServer, Running: true},
		{Name: "kenaz__monitor", Server: BuiltinServer, Running: true},
		{Name: "outlook__send-mail", Server: "outlook", Running: true},
		{Name: "outlook__list-messages", Server: "outlook", Running: true},
		{Name: "outlook__create-event", Server: "outlook", Running: true},
		{Name: "fetch__fetch", Server: "fetch", Running: true},
		{Name: "git__status", Server: "git", Running: true},
		{Name: "secret__dump", Server: "secret", Running: true},
		{Name: ServerProbeName("github"), Server: "github"},
	}
	deps := Deps{
		Settings: &countingSettings{s: Settings{Exposure: Exposure{Servers: map[string]ServerExposure{
			"git":    {Tier: TierFull},
			"secret": {Tier: TierOff},
		}}}},
		Sessions: stateSessions{st: SessionState{Activations: []Activation{
			{Name: "outlook__send-mail", Server: "outlook", LastUsedTurn: 2},
			{Name: "outlook__list-messages", Server: "outlook", LastUsedTurn: 5},
			{Name: "fetch__fetch", Server: "fetch", LastUsedTurn: 1, Sticky: true},
			{Name: "secret__dump", Server: "secret", LastUsedTurn: 9},
		}}},
		Projects: mapProjects{},
	}
	rc, err := Resolve(context.Background(), deps, "s1", catalog)
	if err != nil {
		t.Fatal(err)
	}
	p := rc.Partition()
	check := func(seg string, got []ResolvedTool, want ...string) {
		t.Helper()
		if want == nil {
			want = []string{}
		}
		if g := names(got); !reflect.DeepEqual(g, want) {
			t.Errorf("%s = %v, want %v", seg, g, want)
		}
	}
	check("Hot", p.Hot, LoadToolsName, "kenaz__read_file")
	check("Pinned", p.Pinned, "fetch__fetch", "git__status")
	check("Activated", p.Activated, "outlook__list-messages", "outlook__send-mail")
	check("Digest", p.Digest, "kenaz__monitor", "outlook__create-event")
	check("Stopped", p.Stopped, ServerProbeName("github"))

	send := p.Send()
	for _, n := range send {
		if !rc.Sendable(n) {
			t.Errorf("Send() includes %q but Sendable reports false", n)
		}
	}
	for _, n := range []string{"secret__dump", "kenaz__monitor", "outlook__create-event", ServerProbeName("github"), "nope__x"} {
		if rc.Sendable(n) {
			t.Errorf("Sendable(%q) = true, want false (off, summary-not-activated, stopped or unknown)", n)
		}
	}
}

// TestResolver_ForTurnReadsSettingsOnce: the turn snapshot reuses one
// settings read across every Resolve; session state is still read each
// time.
func TestResolver_ForTurnReadsSettingsOnce(t *testing.T) {
	cs := &countingSettings{}
	r, err := NewResolver(Deps{Settings: cs, Sessions: stateSessions{}, Projects: mapProjects{}})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := r.ForTurn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := turn.Resolve(context.Background(), "s1", nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := cs.reads.Load(); got != 1 {
		t.Fatalf("settings reads = %d, want 1 for a turn of three calls", got)
	}
}

// TestDeps_WithLayer: a proposed layer replaces the stored one at its
// level only, and user/project writes resolve without a real session.
func TestDeps_WithLayer(t *testing.T) {
	off := Exposure{Servers: map[string]ServerExposure{"fetch": {Tier: TierOff}}}
	base := Deps{
		Settings: &countingSettings{},
		Sessions: stateSessions{st: SessionState{ProjectID: "p1"}},
		Projects: mapProjects{},
	}
	cat := []CatalogTool{{Name: "fetch__fetch", Server: "fetch", Running: true}}
	for _, w := range []LayerWrite{
		{Level: LevelUser, Exposure: off},
		{Level: LevelProject, ProjectID: "p1", Exposure: off},
		{Level: LevelSession, SessionID: "s1", Exposure: off},
	} {
		sid := w.SessionID
		rc, err := Resolve(context.Background(), base.WithLayer(w), sid, cat)
		if err != nil {
			t.Fatalf("%s: %v", w.Level, err)
		}
		if got, _ := rc.Tool("fetch__fetch"); got.Tier != TierOff || got.Source != w.Level {
			t.Errorf("%s write: fetch__fetch = %+v, want off from %s", w.Level, got, w.Level)
		}
	}
}

func TestServerProbeName(t *testing.T) {
	if n := ServerProbeName("outlook"); n != "outlook__" || !IsServerProbe(n) {
		t.Fatalf("ServerProbeName = %q", n)
	}
	if IsServerProbe("outlook__send") || IsServerProbe("__") {
		t.Fatal("IsServerProbe matched a tool name")
	}
}
