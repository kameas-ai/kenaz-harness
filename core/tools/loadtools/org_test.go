package loadtools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
)

type fakePins struct{ p toolexposure.OrgPolicy }

func (f fakePins) ToolExposurePolicy(context.Context) (toolexposure.OrgPolicy, error) {
	return f.p, nil
}

func orgPolicy() toolexposure.OrgPolicy {
	return toolexposure.OrgPolicy{
		Pins: toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{
			"secret": {Tier: toolexposure.TierOff},
			"mixed":  {Tools: map[string]toolexposure.Tier{"drop": toolexposure.TierOff}},
		}},
		Defaults: toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{
			"fetch": {Tier: toolexposure.TierOff},
		}},
		BundleID: 42,
	}
}

func newOrgFixture(t *testing.T, user toolexposure.Exposure, sess *fakeSessions, projects fakeProjects) fixture {
	t.Helper()
	resolver, err := toolexposure.NewResolver(toolexposure.Deps{
		Settings: fakeSettings{s: toolexposure.Settings{Exposure: user}},
		Sessions: sess,
		Projects: projects,
		Pins:     fakePins{p: orgPolicy()},
	})
	if err != nil {
		t.Fatal(err)
	}
	au := &recordingAudit{}
	svc, err := NewService(Deps{
		Catalog:     fakeCatalog{entries: catalogFixture()},
		Resolver:    resolver,
		Servers:     fakeServers{list: serversFixture()},
		Activations: sess,
		Turns:       fakeTurns{n: 3},
		Audit:       au,
		Now:         func() time.Time { return time.Unix(1700000000, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{svc: svc, sessions: sess, audit: au}
}

func wantPinned(t *testing.T, err error, server, tool string) {
	t.Helper()
	var pe *toolexposure.PinnedError
	if !errors.Is(err, toolexposure.ErrPinnedByOrg) || !errors.As(err, &pe) {
		t.Fatalf("err = %v, want a *PinnedError", err)
	}
	if pe.Server != server || pe.Tool != tool || pe.BundleID != 42 {
		t.Fatalf("pinned error = %+v, want server %q tool %q from bundle 42", pe, server, tool)
	}
	if !strings.Contains(err.Error(), "set by your organisation (fleet config bundle 42)") {
		t.Fatalf("message %q lacks the pin's origin", err)
	}
}

// Sessions_LoadTools (by user) on a server or tool the organisation
// pinned off is refused with the pin and activates nothing; the model's
// kenaz__load_tools call is told per name and does not error.
func TestLoad_OrgOffRefusedForUser(t *testing.T) {
	ctx := context.Background()
	f := newOrgFixture(t, toolexposure.Exposure{}, &fakeSessions{}, fakeProjects{})

	_, err := f.svc.Load(ctx, "s1", Request{Servers: []string{"secret", "outlook"}}, audit.ToolsActivatedByUser)
	wantPinned(t, err, "secret", "")
	if f.sessions.writes != 0 || len(f.audit.snapshot()) != 0 {
		t.Fatalf("refused load still wrote %d activation set(s) / %d audit row(s)", f.sessions.writes, len(f.audit.snapshot()))
	}

	_, err = f.svc.Load(ctx, "s1", Request{Tools: []string{"mixed__drop"}}, audit.ToolsActivatedByUser)
	wantPinned(t, err, "mixed", "drop")

	_, err = f.svc.Load(ctx, "s1", Request{Tools: []string{"secret__*"}}, audit.ToolsActivatedByUser)
	wantPinned(t, err, "secret", "")

	// A server only partly pinned off loads the rest and names the pinned tool.
	res, err := f.svc.Load(ctx, "s1", Request{Servers: []string{"mixed"}}, audit.ToolsActivatedByUser)
	if err != nil {
		t.Fatalf("partly pinned server: %v", err)
	}
	if len(res.Loaded) != 1 || res.Loaded[0] != "mixed__keep" || len(res.NotLoaded) != 1 || res.NotLoaded[0].Reason != "off — set by your organisation" {
		t.Fatalf("partly pinned server result = %+v", res)
	}

	// An org default (pinned:false) off is not a pin: the user's own
	// request is answered per name, no PinnedError.
	res, err = f.svc.Load(ctx, "s1", Request{Servers: []string{"fetch"}}, audit.ToolsActivatedByUser)
	if err != nil || len(res.NotLoaded) != 1 {
		t.Fatalf("org-default off: res=%+v err=%v, want a per-name refusal and no error", res, err)
	}

	res, err = f.svc.Load(ctx, "s1", Request{Servers: []string{"secret"}}, audit.ToolsActivatedByModel)
	if err != nil {
		t.Fatalf("model load errored: %v", err)
	}
	if len(res.NotLoaded) != 1 || res.NotLoaded[0].Reason != "off — set by your organisation" {
		t.Fatalf("model load result = %+v, want the org reason", res)
	}
}

// Every writer's layer goes through CheckLayerWrite: a change to a pinned
// entry is refused at the user, project and session level; writing the
// stored value back, or touching an unpinned entry, is not.
func TestCheckLayerWrite_RefusesPinnedEntriesAtEveryLevel(t *testing.T) {
	ctx := context.Background()
	stored := toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{"secret": {Tier: toolexposure.TierFull}}}
	sess := &fakeSessions{override: stored, project: "p1"}
	f := newOrgFixture(t, stored, sess, fakeProjects{"p1": stored})

	change := toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{"secret": {Tier: toolexposure.TierSummary}}}
	toolChange := toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{"mixed": {Tools: map[string]toolexposure.Tier{"drop": toolexposure.TierFull}}}}
	free := toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{
		"secret":  {Tier: toolexposure.TierFull},
		"fetch":   {Tier: toolexposure.TierFull},
		"outlook": {Tier: toolexposure.TierOff},
	}}
	for _, w := range []toolexposure.LayerWrite{
		{Level: toolexposure.LevelUser},
		{Level: toolexposure.LevelProject, ProjectID: "p1"},
		{Level: toolexposure.LevelSession, SessionID: "s1"},
	} {
		t.Run(string(w.Level), func(t *testing.T) {
			w.Exposure = change
			wantPinned(t, f.svc.CheckLayerWrite(ctx, w), "secret", "")
			w.Exposure = toolChange
			wantPinned(t, f.svc.CheckLayerWrite(ctx, w), "mixed", "drop")
			w.Exposure = free
			if err := f.svc.CheckLayerWrite(ctx, w); err != nil {
				t.Fatalf("stored pinned value + unpinned changes refused: %v", err)
			}
		})
	}
}
