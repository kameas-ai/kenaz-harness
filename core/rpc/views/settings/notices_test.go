package settings

// notices_test.go — kenaz-fleet spec 017 T23 (contract-harness-sync §17): the
// member notices view. Dormant while Fleet serves nothing, verbatim text with
// a checked hash once it does, ack / withdraw for memory-access only, and
// plain wording for paused / lapsed / 422 refusals. A real fleet.Client
// against a scripted Fleet, tokens from the external token source.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

type noticeFakeFleet struct {
	mu        sync.Mutex
	srv       *httptest.Server
	texts     map[string]fleet.NoticeText // by family; absent = 404
	rows      []fleet.NoticeAckState
	ackStatus int
	ackCode   string
	posts     []map[string]string
}

func newNoticeFakeFleet(t *testing.T) *noticeFakeFleet {
	f := &noticeFakeFleet{texts: map[string]fleet.NoticeText{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		reply := func(code int, v any) { w.WriteHeader(code); _ = json.NewEncoder(w).Encode(v) }
		switch {
		case r.URL.Path == "/config.json":
			reply(200, map[string]string{"api_base_url": f.srv.URL})
		case r.URL.Path == "/api/v1/me/notice-text":
			nt, ok := f.texts[r.URL.Query().Get("family")]
			ed := r.URL.Query().Get("edition")
			if !ok || r.URL.Query().Get("surface") != "kenaz-harness" || (ed != "" && ed != nt.Edition) {
				reply(404, map[string]any{"code": "notice_not_registered"})
				return
			}
			reply(200, nt)
		case r.URL.Path == "/api/v1/me/notice-acks" && r.Method == http.MethodGet:
			reply(200, map[string]any{"families": f.rows})
		case r.URL.Path == "/api/v1/me/notice-acks" && r.Method == http.MethodPost:
			raw, _ := io.ReadAll(r.Body)
			var m map[string]string
			_ = json.Unmarshal(raw, &m)
			f.posts = append(f.posts, m)
			if f.ackStatus != 0 {
				reply(f.ackStatus, map[string]any{"code": f.ackCode})
				return
			}
			at := "2026-12-01T10:00:00Z"
			row := fleet.NoticeAckState{Family: m["family"], Edition: m["edition"], TextHash: m["text_hash"],
				Surface: m["surface"], Action: m["action"], Acked: m["action"] == "ack", ActedAt: at}
			if row.Acked {
				row.AckedSince = &at
			}
			f.rows = []fleet.NoticeAckState{row}
			reply(201, map[string]any{"family": row.Family, "edition": row.Edition, "action": row.Action, "acted_at": at})
		default:
			reply(404, map[string]any{"code": "not_found"})
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *noticeFakeFleet) set(fn func(*noticeFakeFleet)) { f.mu.Lock(); fn(f); f.mu.Unlock() }
func (f *noticeFakeFleet) postLog() []map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]string(nil), f.posts...)
}

func servedNotice(family, edition, text string) fleet.NoticeText {
	return fleet.NoticeText{Family: family, Edition: edition, Surface: "kenaz-harness", Text: text, TextHash: fleet.NoticeTextHash(text)}
}

func newNoticeRig(t *testing.T) (*API, *noticeFakeFleet) {
	t.Helper() // NOT t.Parallel: SetExternalTokenSource is process-global
	if fleet.Disabled() {
		t.Skip("HARNESS_FLEET_DISABLED=1: rig requires fleet enabled")
	}
	f := newNoticeFakeFleet(t)
	tok := jwtFor("sub-alice", "zitadel-org-1")
	fleet.SetExternalTokenSource(func() string { return tok })
	t.Cleanup(func() { fleet.SetExternalTokenSource(nil) })
	api := &API{}
	api.SetFleetClient(fleet.NewClientForTesting(f.srv.URL), t.TempDir())
	t.Cleanup(api.StopFleetBackground)
	return api, f
}

func TestFleetMemberNotices_DormantShowsNothing(t *testing.T) {
	api, f := newNoticeRig(t)
	v, err := api.FleetMemberNotices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Notices) != 0 || v.StateError != "" || v.Notices == nil {
		t.Fatalf("dormant view = %+v, want an empty (non-nil) list", v)
	}
	if _, err := api.FleetRespondMemberNotice(context.Background(), "memory-access", "memory-access/v1",
		fleet.NoticeTextHash("x"), "ack"); !errors.Is(err, ErrNoticeChanged) {
		t.Fatalf("ack with nothing served: err = %v", err)
	}
	if len(f.postLog()) != 0 {
		t.Fatal("nothing is served: no ack may be posted")
	}
	// Unwired API: also dormant, no error.
	if v, err := (&API{}).FleetMemberNotices(context.Background()); err != nil || len(v.Notices) != 0 {
		t.Fatalf("unwired = %+v, %v", v, err)
	}
}

func TestFleetMemberNotices_VerbatimAndTamperedRefused(t *testing.T) {
	api, f := newNoticeRig(t)
	text := "Line one.\n\n  Line two keeps its spacing — and “quotes”."
	mem := servedNotice("memory-access", "memory-access/v1", text)
	tampered := servedNotice("agent-telemetry", "agent-telemetry/v4", "Usage notice.")
	tampered.Text = "Usage notice, edited."
	f.set(func(f *noticeFakeFleet) {
		f.texts["memory-access"] = mem
		f.texts["agent-telemetry"] = tampered
	})
	v, err := api.FleetMemberNotices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Notices) != 1 {
		t.Fatalf("notices = %+v, want only the verified memory-access text", v.Notices)
	}
	n := v.Notices[0]
	if n.Text != text || n.TextHash != mem.TextHash || n.Edition != "memory-access/v1" || n.Action != "" {
		t.Fatalf("notice = %+v", n)
	}
}

func TestFleetRespondMemberNotice_AckAndWithdraw(t *testing.T) {
	api, f := newNoticeRig(t)
	mem := servedNotice("memory-access", "memory-access/v1", "Memory notice.")
	f.set(func(f *noticeFakeFleet) { f.texts["memory-access"] = mem })

	v, err := api.FleetRespondMemberNotice(context.Background(), "memory-access", mem.Edition, mem.TextHash, "ack")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Notices) != 1 || v.Notices[0].Action != "ack" || !v.Notices[0].Acked || v.Notices[0].AckedSince == "" {
		t.Fatalf("after ack = %+v", v)
	}
	v, err = api.FleetRespondMemberNotice(context.Background(), "memory-access", mem.Edition, mem.TextHash, "withdraw")
	if err != nil {
		t.Fatal(err)
	}
	if v.Notices[0].Action != "withdraw" || v.Notices[0].Acked {
		t.Fatalf("after withdraw = %+v", v.Notices[0])
	}
	posts := f.postLog()
	if len(posts) != 2 || posts[0]["surface"] != "kenaz-harness" || posts[0]["text_hash"] != mem.TextHash ||
		posts[0]["edition"] != mem.Edition || posts[1]["action"] != "withdraw" {
		t.Fatalf("posts = %v", posts)
	}

	// A hash other than the one served now is refused without posting.
	if _, err := api.FleetRespondMemberNotice(context.Background(), "memory-access", mem.Edition,
		fleet.NoticeTextHash("old words"), "ack"); !errors.Is(err, ErrNoticeChanged) {
		t.Fatalf("stale hash: err = %v", err)
	}
	// Telemetry is acked via the opt-in PUT, never here; unknown actions refused.
	if _, err := api.FleetRespondMemberNotice(context.Background(), "agent-telemetry", "agent-telemetry/v4",
		mem.TextHash, "ack"); !errors.Is(err, ErrNoticeUnsupported) {
		t.Fatalf("telemetry: err = %v", err)
	}
	if _, err := api.FleetRespondMemberNotice(context.Background(), "memory-access", mem.Edition,
		mem.TextHash, "agree"); !errors.Is(err, ErrNoticeUnsupported) {
		t.Fatalf("bad action: err = %v", err)
	}
	if n := len(f.postLog()); n != 2 {
		t.Fatalf("refusals posted: %d posts", n)
	}
}

func TestFleetRespondMemberNotice_PlainWordingOnRefusals(t *testing.T) {
	api, f := newNoticeRig(t)
	mem := servedNotice("memory-access", "memory-access/v1", "Memory notice.")
	f.set(func(f *noticeFakeFleet) { f.texts["memory-access"] = mem })
	for _, tc := range []struct {
		status int
		code   string
		want   error
	}{
		{403, "org_paused", ErrNoticeAckOrgPaused},
		{403, "subscription_inactive", ErrNoticeAckOrgInactive},
		{422, "notice_not_registered", ErrNoticeAckNotRecorded},
	} {
		f.set(func(f *noticeFakeFleet) { f.ackStatus, f.ackCode = tc.status, tc.code })
		_, err := api.FleetRespondMemberNotice(context.Background(), "memory-access", mem.Edition, mem.TextHash, "ack")
		if !errors.Is(err, tc.want) {
			t.Errorf("%d %s: err = %v, want %v", tc.status, tc.code, err, tc.want)
		}
		if err != nil && (strings.Contains(strings.ToLower(err.Error()), "regist") || strings.Contains(err.Error(), "422")) {
			t.Errorf("%s: member-facing wording mentions registration or a status: %q", tc.code, err)
		}
	}
}
