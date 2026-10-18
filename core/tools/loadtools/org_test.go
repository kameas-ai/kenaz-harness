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

type fakeOrgPolicy struct{ p toolexposure.OrgPolicy }

func (f fakeOrgPolicy) ToolExposurePolicy(context.Context) (toolexposure.OrgPolicy, error) {
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
		Pins:     fakeOrgPolicy{p: orgPolicy()},
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

// Loading is per name for a person and for the model alike: a server or
// tool the organisation pinned off is reported in not_loaded with the org
// reason, and every other requested name still loads.
func TestLoad_OrgOffReportedPerName(t *testing.T) {
	ctx := context.Background()
	for _, by := range []string{audit.ToolsActivatedByUser, audit.ToolsActivatedByModel} {
		t.Run(by, func(t *testing.T) {
			f := newOrgFixture(t, toolexposure.Exposure{}, &fakeSessions{}, fakeProjects{})
			res, err := f.svc.Load(ctx, "s1", Request{Servers: []string{"secret", "fetch"}, Tools: []string{"mixed__drop", "outlook__send-mail", "secret__*"}}, by)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if len(res.Loaded) != 1 || res.Loaded[0] != "outlook__send-mail" {
				t.Fatalf("loaded = %v, want the unpinned tool", res.Loaded)
			}
			reasons := map[string]string{}
			for _, nl := range res.NotLoaded {
				reasons[nl.Name] = nl.Reason
			}
			const org = "off — set by your organisation"
			for _, n := range []string{"secret", "mixed__drop", "secret__*"} {
				if reasons[n] != org {
					t.Errorf("%s: reason %q, want %q (all: %+v)", n, reasons[n], org, res.NotLoaded)
				}
			}
			// An org default (pinned:false) off is not a pin.
			if reasons["fetch"] == "" || reasons["fetch"] == org {
				t.Errorf("fetch (org default off): reason %q, want a non-org off reason", reasons["fetch"])
			}
			if got := f.sessions.snapshot(); len(got) != 1 || got[0].Name != "outlook__send-mail" {
				t.Fatalf("activations = %+v, want only the unpinned tool", got)
			}
		})
	}
}

// Project and session layers go through CheckLayerWrite: a change to a
// pinned entry is refused; writing the stored value back, or touching an
// unpinned entry, is not. The user layer is checked by the settings
// writer (covered in core/rpc/views/settings), so the guard passes it.
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
	if err := f.svc.CheckLayerWrite(ctx, toolexposure.LayerWrite{Level: toolexposure.LevelUser, Exposure: change}); err != nil {
		t.Fatalf("user layer: %v, want the guard to leave it to the settings writer", err)
	}
	for _, w := range []toolexposure.LayerWrite{
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
