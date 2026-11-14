package fleet

// Member notice acks, client side (kenaz-fleet spec 017 T23,
// contract-harness-sync §17): the text endpoint, the ack store, the
// telemetry notice_version form and the D168(2) memory push hold. The hold
// tests drive the production MemorySync against fakeMemoryFleet over HTTP.

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

	"github.com/kameas-ai/kenaz-harness/core/memory"
)

const testMemoryNoticeText = "Team leads and admins in your organization can see the notes you sync.\nThis is a test text."

func testMemoryNotice() *NoticeText {
	return &NoticeText{Family: NoticeFamilyMemoryAccess, Edition: "memory-access/v1", Surface: NoticeSurfaceHarness,
		Text: testMemoryNoticeText, TextHash: NoticeTextHash(testMemoryNoticeText)}
}

// ── editions, hashes, notice_version ────────────────────────────────────────

func TestNoticeEditions(t *testing.T) {
	cases := []struct {
		ed, family string
		min        int
		want       bool
	}{
		{"memory-access/v1", NoticeFamilyMemoryAccess, 1, true},
		{"memory-access/v10", NoticeFamilyMemoryAccess, 4, true},
		{"agent-telemetry/v4-small", NoticeFamilyAgentTelemetry, 4, true},
		{"agent-telemetry/v3", NoticeFamilyAgentTelemetry, 4, false},
		{"memory-sync/v4", NoticeFamilyMemoryAccess, 1, false},
		{"v4", NoticeFamilyAgentTelemetry, 1, false},
		{"agent-telemetry/v4+p1", NoticeFamilyAgentTelemetry, 1, false},
	}
	for _, c := range cases {
		if got := NoticeEditionMeets(c.ed, c.family, c.min); got != c.want {
			t.Errorf("NoticeEditionMeets(%q, %q, %d) = %v", c.ed, c.family, c.min, got)
		}
	}
	// sha256("") — the hash is lowercase hex of the UTF-8 bytes.
	if got := NoticeTextHash(""); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("NoticeTextHash(\"\") = %s", got)
	}
}

func TestHarnessTelemetryNoticeVersion_OnlyTheHarnessForm(t *testing.T) {
	v, err := HarnessTelemetryNoticeVersion("agent-telemetry/v4")
	if err != nil || v != "kenaz-harness/agent-telemetry/v4" {
		t.Fatalf("got %q, %v", v, err)
	}
	for _, bad := range []string{"", "v4", "memory-access/v1", "agent-telemetry/v4+p1",
		"agent-telemetry/v4-" + strings.Repeat("x", 40)} {
		if v, err := HarnessTelemetryNoticeVersion(bad); err == nil {
			t.Errorf("HarnessTelemetryNoticeVersion(%q) = %q, want an error", bad, v)
		}
	}
}

// ── the HTTP client ─────────────────────────────────────────────────────────

// noticeServer is a scripted Fleet for the notice routes. Race-safe.
type noticeServer struct {
	mu        sync.Mutex
	text      *NoticeText // nil = 404 notice_not_registered
	ackStatus int
	ackCode   string
	posts     []map[string]string
	puts      []map[string]any
}

func (s *noticeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	switch {
	case r.URL.Path == "/api/v1/me/notice-text":
		if s.text == nil || r.URL.Query().Get("family") != s.text.Family ||
			r.URL.Query().Get("surface") != NoticeSurfaceHarness {
			writeJSON(w, 404, map[string]any{"code": "notice_not_registered"})
			return
		}
		writeJSON(w, 200, s.text)
	case r.URL.Path == "/api/v1/me/notice-acks" && r.Method == http.MethodPost:
		var m map[string]string
		_ = json.Unmarshal(body, &m)
		s.posts = append(s.posts, m)
		if s.ackStatus != 0 {
			writeJSON(w, s.ackStatus, map[string]any{"code": s.ackCode, "details": map[string]any{"paused_category": "billing"}})
			return
		}
		writeJSON(w, 201, map[string]any{"family": m["family"], "edition": m["edition"], "action": m["action"], "acted_at": "2026-12-01T10:00:00Z"})
	case r.URL.Path == "/api/v1/me/telemetry-opt-ins" && r.Method == http.MethodPut:
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		s.puts = append(s.puts, m)
		writeJSON(w, 200, map[string]any{"opt_ins": []any{}})
	default:
		writeJSON(w, 404, map[string]any{"code": "not_found"})
	}
}

func (s *noticeServer) set(fn func(*noticeServer)) { s.mu.Lock(); fn(s); s.mu.Unlock() }

func (s *noticeServer) snapshot() (posts []map[string]string, puts []map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(posts, s.posts...), append(puts, s.puts...)
}

func newNoticeClient(t *testing.T) (*Client, *noticeServer) {
	t.Helper()
	ns := &noticeServer{}
	srv := httptest.NewServer(ns)
	t.Cleanup(srv.Close)
	saveTestTokens(t, "user-test")
	return newTestClient(t, srv), ns
}

func TestGetNoticeText_NotRegisteredIsDormant(t *testing.T) {
	c, _ := newNoticeClient(t)
	_, err := c.GetNoticeText(context.Background(), NoticeFamilyMemoryAccess, "")
	if !errors.Is(err, ErrNoticeNotRegistered) {
		t.Fatalf("err = %v, want ErrNoticeNotRegistered", err)
	}
}

func TestGetNoticeText_ServedTextVerifiedAndTamperedRefused(t *testing.T) {
	c, ns := newNoticeClient(t)
	ns.set(func(s *noticeServer) { s.text = testMemoryNotice() })
	nt, err := c.GetNoticeText(context.Background(), NoticeFamilyMemoryAccess, "")
	if err != nil {
		t.Fatal(err)
	}
	if nt.Text != testMemoryNoticeText || nt.Edition != "memory-access/v1" {
		t.Fatalf("served = %+v", nt)
	}

	tampered := []func(*NoticeText){
		func(n *NoticeText) { n.Text += " (edited)" },                                    // bytes changed
		func(n *NoticeText) { n.TextHash = strings.ToUpper(n.TextHash) },                 // not lowercase hex
		func(n *NoticeText) { n.Surface = "fleet-dashboard" },                            // another surface's text
		func(n *NoticeText) { n.Edition = "memory-sync/v4" },                             // another family's edition
		func(n *NoticeText) { n.Text, n.TextHash = "", NoticeTextHash("") },              // empty
		func(n *NoticeText) { n.TextHash = NoticeTextHash(testMemoryNoticeText + "\n") }, // trailing byte
	}
	for i, mut := range tampered {
		bad := testMemoryNotice()
		mut(bad)
		bad.Family = NoticeFamilyMemoryAccess
		ns.set(func(s *noticeServer) { s.text = bad })
		if _, err := c.GetNoticeText(context.Background(), NoticeFamilyMemoryAccess, ""); !errors.Is(err, ErrNoticeTextMismatch) {
			t.Errorf("tamper %d: err = %v, want ErrNoticeTextMismatch", i, err)
		}
	}
}

func TestPostNoticeAck_AckWithdrawAndRefusals(t *testing.T) {
	c, ns := newNoticeClient(t)
	nt := *testMemoryNotice()
	for _, action := range []string{NoticeActionAck, NoticeActionWithdraw} {
		res, err := c.PostNoticeAck(context.Background(), nt, action)
		if err != nil || res.Action != action {
			t.Fatalf("%s: %+v, %v", action, res, err)
		}
	}
	posts, _ := ns.snapshot()
	if len(posts) != 2 {
		t.Fatalf("posts = %d", len(posts))
	}
	want := map[string]string{"family": "memory-access", "edition": "memory-access/v1",
		"text_hash": nt.TextHash, "surface": "kenaz-harness", "action": "ack"}
	for k, v := range want {
		if posts[0][k] != v {
			t.Errorf("post[%s] = %q, want %q", k, posts[0][k], v)
		}
	}
	if posts[1]["action"] != "withdraw" {
		t.Errorf("second post action = %q", posts[1]["action"])
	}

	if _, err := c.PostNoticeAck(context.Background(), nt, "accept"); err == nil {
		t.Error("an action other than ack/withdraw must be refused before sending")
	}
	tele := nt
	tele.Family = NoticeFamilyAgentTelemetry
	if _, err := c.PostNoticeAck(context.Background(), tele, NoticeActionAck); err == nil {
		t.Error("agent-telemetry must never be posted to /me/notice-acks")
	}
	forged := nt
	forged.TextHash = NoticeTextHash("other words")
	if _, err := c.PostNoticeAck(context.Background(), forged, NoticeActionAck); !errors.Is(err, ErrNoticeTextMismatch) {
		t.Errorf("forged hash: err = %v", err)
	}
	if posts, _ := ns.snapshot(); len(posts) != 2 {
		t.Fatalf("refused acks must send nothing; posts = %d", len(posts))
	}

	for _, tc := range []struct {
		status int
		code   string
		check  func(error) bool
	}{
		{403, "org_paused", IsOrgPaused},
		{403, "subscription_inactive", func(err error) bool { return errors.Is(err, ErrNoticeOrgInactive) }},
		{422, "notice_not_registered", func(err error) bool { return errors.Is(err, ErrNoticeNotRegistered) }},
	} {
		ns.set(func(s *noticeServer) { s.ackStatus, s.ackCode = tc.status, tc.code })
		if _, err := c.PostNoticeAck(context.Background(), nt, NoticeActionAck); !tc.check(err) {
			t.Errorf("%d %s: err = %v", tc.status, tc.code, err)
		}
	}
	c.ResetOrgPause()
}

func TestPutTelemetryOptIns_NoticeVersionForm(t *testing.T) {
	c, ns := newNoticeClient(t)
	upd := []TelemetryOptInItem{{Class: "harness.usage_counts", OptedIn: true}}
	if err := c.PutTelemetryOptIns(context.Background(), upd); err != nil {
		t.Fatal(err)
	}
	if err := c.PutTelemetryOptInsWithNotice(context.Background(), upd, "kenaz-harness/agent-telemetry/v4"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"fleet-dashboard/agent-telemetry/v4", "agent-telemetry/v4+p1",
		"agent-telemetry/v4", "kenaz-harness/memory-access/v1"} {
		if err := c.PutTelemetryOptInsWithNotice(context.Background(), upd, bad); err == nil {
			t.Errorf("notice_version %q must be refused", bad)
		}
	}
	_, puts := ns.snapshot()
	if len(puts) != 2 {
		t.Fatalf("puts = %d, want 2 (refused forms send nothing)", len(puts))
	}
	if _, has := puts[0]["notice_version"]; has {
		t.Errorf("no notice shown: notice_version must be absent, got %v", puts[0]["notice_version"])
	}
	if puts[1]["notice_version"] != "kenaz-harness/agent-telemetry/v4" {
		t.Errorf("notice_version = %v", puts[1]["notice_version"])
	}
}

func TestTelemetryPusher_PushWithNotice(t *testing.T) {
	c, ns := newNoticeClient(t)
	p := NewTelemetryOptInPusher("", func() *Client { return c })

	// Dormant: the edition isn't served, so the PUT goes without it.
	if err := p.PushWithNotice(context.Background(), ConsentAggregate, "agent-telemetry/v4"); err != nil {
		t.Fatal(err)
	}
	text := "Agent telemetry notice text."
	ns.set(func(s *noticeServer) {
		s.text = &NoticeText{Family: NoticeFamilyAgentTelemetry, Edition: "agent-telemetry/v4",
			Surface: NoticeSurfaceHarness, Text: text, TextHash: NoticeTextHash(text)}
	})
	if err := p.PushWithNotice(context.Background(), ConsentNone, "agent-telemetry/v4"); err != nil {
		t.Fatal(err)
	}
	// An edition other than the one served records nothing.
	if err := p.PushWithNotice(context.Background(), ConsentNone, "agent-telemetry/v5"); err != nil {
		t.Fatal(err)
	}
	_, puts := ns.snapshot()
	if len(puts) != 3 {
		t.Fatalf("puts = %d", len(puts))
	}
	if _, has := puts[0]["notice_version"]; has {
		t.Error("unregistered edition must send no notice_version")
	}
	if puts[1]["notice_version"] != "kenaz-harness/agent-telemetry/v4" {
		t.Errorf("served edition: notice_version = %v", puts[1]["notice_version"])
	}
	if _, has := puts[2]["notice_version"]; has {
		t.Error("an edition Fleet doesn't serve must send no notice_version")
	}
}

// ── the D168(2) memory push hold ────────────────────────────────────────────

// upsertedIDs lists ids pushed as upserts (forgets excluded).
func upsertedIDs(f *fakeMemoryFleet) map[string]bool {
	out := map[string]bool{}
	for _, req := range f.pushLog() {
		for _, it := range req.Items {
			if it.Op != "forget" {
				out[it.ID] = true
			}
		}
	}
	return out
}

func forgottenIDs(f *fakeMemoryFleet) map[string]bool {
	out := map[string]bool{}
	for _, req := range f.pushLog() {
		for _, it := range req.Items {
			if it.Op == "forget" {
				out[it.ID] = true
			}
		}
	}
	return out
}

// ackAs records action for the served text under the device's current token.
func ackAs(t *testing.T, d *memDevice, action string) {
	t.Helper()
	nt, err := d.ms.cfg.Client.GetNoticeText(context.Background(), NoticeFamilyMemoryAccess, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ms.cfg.Client.PostNoticeAck(context.Background(), nt, action); err != nil {
		t.Fatal(err)
	}
	d.ms.InvalidateNoticeHold()
}

func TestNoticeHold_DormantRegistryNeverHolds(t *testing.T) {
	w := newMemWorld(t)
	d := w.device("node-1", 0)
	d.enable()
	d.add("n1", memory.ScopeKindLongTerm, "a note")
	d.sync()
	if !upsertedIDs(w.fleet)["n1"] {
		t.Fatal("with nothing registered, sync must push exactly as before")
	}
	if st := d.ms.Status(context.Background()); st.NoticeHold {
		t.Fatalf("dormant registry reported a hold: %+v", st)
	}
	// The dormant answer is cached, not asked every cycle.
	asked := w.fleet.noticeTextRequests()
	d.add("n2", memory.ScopeKindLongTerm, "another")
	d.sync()
	if got := w.fleet.noticeTextRequests(); got != asked {
		t.Fatalf("dormant registry re-asked within the window: %d → %d", asked, got)
	}
	if !upsertedIDs(w.fleet)["n2"] {
		t.Fatal("n2 must push")
	}
}

func TestNoticeHold_HeldUntilAckForgetNeverHeld(t *testing.T) {
	w := newMemWorld(t)
	d := w.device("node-1", 0)
	d.enable()
	d.add("old", memory.ScopeKindLongTerm, "synced before the notice existed")
	d.sync()

	w.fleet.setNotice(testMemoryNotice())
	d.ms.InvalidateNoticeHold() // the dormant answer would otherwise stand for 15 min
	d.add("held", memory.ScopeKindLongTerm, "written while unread")
	d.forget("old")
	d.sync()

	if upsertedIDs(w.fleet)["held"] {
		t.Fatal("an upsert was pushed while the memory-access notice was unread")
	}
	if !forgottenIDs(w.fleet)["old"] {
		t.Fatal("a forget must never be held")
	}
	c := accountChunk(t, d, "held")
	if !c.SyncedAt.IsZero() && !c.SyncDirty {
		t.Fatalf("held note must stay pending locally: %+v", c)
	}
	st := d.ms.Status(context.Background())
	if !st.NoticeHold || st.NoticeHoldReason != NoticeHoldUnread {
		t.Fatalf("status = hold %v reason %q", st.NoticeHold, st.NoticeHoldReason)
	}
	if st.PendingPush == 0 {
		t.Fatal("the held note must be counted as pending, not dropped")
	}

	// Still held next cycle (held decisions are re-checked every cycle).
	d.sync()
	if upsertedIDs(w.fleet)["held"] {
		t.Fatal("still unread: must stay held")
	}

	ackAs(t, d, NoticeActionAck)
	d.sync()
	if !upsertedIDs(w.fleet)["held"] {
		t.Fatal("after the ack the held note must push")
	}
	if st := d.ms.Status(context.Background()); st.NoticeHold {
		t.Fatal("hold must be released after the ack")
	}
}

func TestNoticeHold_WithdrawAlsoReleases(t *testing.T) {
	w := newMemWorld(t)
	w.fleet.setNotice(testMemoryNotice())
	d := w.device("node-1", 0)
	d.enable()
	d.add("n", memory.ScopeKindLongTerm, "x")
	d.sync()
	if upsertedIDs(w.fleet)["n"] {
		t.Fatal("must hold while unread")
	}
	ackAs(t, d, NoticeActionWithdraw)
	d.sync()
	if !upsertedIDs(w.fleet)["n"] {
		t.Fatal("a withdraw has seen the notice: it must not re-impose the hold")
	}
}

func TestNoticeHold_ForgetAllAndSyncOffNeverHeld(t *testing.T) {
	w := newMemWorld(t)
	w.fleet.setNotice(testMemoryNotice())
	d := w.device("node-1", 0)
	d.enable()
	d.add("n", memory.ScopeKindLongTerm, "x")
	d.sync()
	if _, err := d.ms.Disable(context.Background(), true, "forget-all"); err != nil {
		t.Fatalf("Disable with forget-all while held: %v", err)
	}
	if w.fleet.forgetAllCount() != 1 {
		t.Fatal("forget-all must be sent while held")
	}
}

func TestNoticeHold_OrgSwitchRechecks(t *testing.T) {
	w := newMemWorld(t)
	w.fleet.setNotice(testMemoryNotice())
	d := w.device("node-1", 0)
	d.signIn(accountA)
	d.enable()
	ackAs(t, d, NoticeActionAck)
	d.add("a-note", memory.ScopeKindLongTerm, "A's")
	d.sync()
	if !upsertedIDs(w.fleet)["a-note"] {
		t.Fatal("A has read the notice: A's note must push")
	}

	// B (another org) has not read it. A's ack must not carry over.
	d.signIn(accountB)
	d.enable()
	d.add("b-note", memory.ScopeKindLongTerm, "B's")
	d.sync()
	if upsertedIDs(w.fleet)["b-note"] {
		t.Fatal("after an org switch the hold must be re-checked for the new org")
	}
	ackAs(t, d, NoticeActionAck)
	d.sync()
	if !upsertedIDs(w.fleet)["b-note"] {
		t.Fatal("B's note must push after B reads the notice")
	}
}

func TestNoticeHold_TamperedTextHoldsAnd422KeepsHolding(t *testing.T) {
	w := newMemWorld(t)
	bad := testMemoryNotice()
	bad.Text += " (edited)"
	w.fleet.setNotice(bad)
	d := w.device("node-1", 0)
	d.enable()
	d.add("n", memory.ScopeKindLongTerm, "x")
	d.sync()
	if upsertedIDs(w.fleet)["n"] {
		t.Fatal("a registered text that fails its hash must not release the hold")
	}
	if st := d.ms.Status(context.Background()); st.NoticeHoldReason != NoticeHoldTextRefused {
		t.Fatalf("reason = %q", st.NoticeHoldReason)
	}

	// A good text, but Fleet refuses the ack with 422: still held.
	w.fleet.setNotice(testMemoryNotice())
	w.fleet.mu.Lock()
	w.fleet.noticeAckStatus, w.fleet.noticeAckCode = 422, "notice_not_registered"
	w.fleet.mu.Unlock()
	nt, _ := d.ms.cfg.Client.GetNoticeText(context.Background(), NoticeFamilyMemoryAccess, "")
	if _, err := d.ms.cfg.Client.PostNoticeAck(context.Background(), nt, NoticeActionAck); !errors.Is(err, ErrNoticeNotRegistered) {
		t.Fatalf("422: err = %v", err)
	}
	d.ms.InvalidateNoticeHold()
	d.sync()
	if upsertedIDs(w.fleet)["n"] {
		t.Fatal("an ack Fleet refused (422) must keep the hold")
	}
}
