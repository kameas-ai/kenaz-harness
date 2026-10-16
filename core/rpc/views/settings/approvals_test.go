package settings

// approvals_test.go — ml-producer-01MLPRD01 WP06: the pending-approvals hub
// view and approve path, and the Cloud ML panel's hub-rendered notice with
// its pre-hub fallback. Real fleet.Client against the fake Fleet in
// ml_test.go.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

const hubSHA = "0f96e53b0f96e53b0f96e53b0f96e53b0f96e53b0f96e53b0f96e53b0f96e53b"

// hubNoticeText is a server-rendered notice with the per-member diff.
const hubNoticeText = "Acme Corp has turned on hosted inference. Kenaz will upload your activity (rev 2 wording).\n\n" +
	"Changed since you last approved:\n- no longer excluded: hr/**"

func hubLegal() map[string]any {
	return map[string]any{
		"id": "legal:terms:v1.1", "kind": "legal_acceptance", "title": "Terms of Use",
		"summary":      "Review and accept the current Terms of Use (v1.1).",
		"document_url": "https://kenaz.kameas.ai/terms.html", "document_sha256": hubSHA,
		"version": "v1.1", "blocking": "Continued use of Fleet", "required": true,
		"approve": map[string]any{"method": "POST", "path": "/api/v1/me/legal-acceptances",
			"body": map[string]any{"acceptances": []any{map[string]any{"kind": "terms", "version": "v1.1"}}}},
	}
}

func hubNotice() map[string]any {
	return map[string]any{
		"id": "ml_notice:o1:4", "kind": "ml_notice", "title": "Hosted inference notice",
		"summary": "Read how your activity is uploaded.", "body_text": hubNoticeText,
		"version": "4", "blocking": "Hosted inference uploads for Acme Corp", "required": true,
		"approve": map[string]any{"method": "POST", "path": "/api/v1/me/ml/notice-ack",
			"body": map[string]any{"notice_version": 4, "text_revision": 2}},
	}
}

func hubExclusionsChange() map[string]any {
	return map[string]any{
		"id": "ml_exclusions_change:o1:5", "kind": "ml_exclusions_change", "title": "Exclusions changed",
		"summary": "Your organization now excludes more.", "body_text": "Added: secrets/**",
		"version": "5", "blocking": "Nothing is paused", "required": false,
		"approve": map[string]any{"method": "POST", "path": "/api/v1/me/ml/exclusions-seen",
			"body": map[string]any{"exclusions_version": 5}},
	}
}

func hubForeign() map[string]any {
	return map[string]any{
		"id": "future:1", "kind": "future_kind", "title": "Something new", "summary": "A new kind.",
		"version": "1", "blocking": "Nothing is paused", "required": false,
		"approve": map[string]any{"method": "POST", "path": "/api/v1/orgs/o1/delete", "body": map[string]any{}},
	}
}

func hubBody(items ...map[string]any) string {
	if items == nil {
		items = []map[string]any{}
	}
	b, _ := json.Marshal(map[string]any{"items": items, "count": len(items)})
	return string(b)
}

func (f *mlFakeFleet) hubSnapshot() (gets int, posts []string, acks []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hubGets, append([]string(nil), f.hubPosts...), append([]string(nil), f.ackRaw...)
}

func TestFleetPendingApprovals_NotWired(t *testing.T) {
	v, err := (&API{}).FleetPendingApprovals(context.Background())
	if err != nil || v.SignedIn || v.Available || v.Items == nil || len(v.Items) != 0 {
		t.Errorf("unwired = %+v, %v", v, err)
	}
	if _, err := (&API{}).FleetApproveItem(context.Background(), "x"); !errors.Is(err, ErrApprovalsNotWired) {
		t.Errorf("approve unwired err = %v", err)
	}
}

func TestFleetPendingApprovals_OlderFleet404(t *testing.T) {
	r := newMLRig(t, true)
	v, err := r.api.FleetPendingApprovals(context.Background())
	if err != nil || !v.SignedIn || v.Available || len(v.Items) != 0 || v.FleetError != "" {
		t.Errorf("404 view = %+v, %v", v, err)
	}
	raw, _ := json.Marshal(v)
	if !strings.Contains(string(raw), `"items":[]`) {
		t.Errorf("wire items not []: %s", raw)
	}
}

func TestFleetPendingApprovals_ListAndKinds(t *testing.T) {
	r := newMLRig(t, true)
	r.fleet.set(func(f *mlFakeFleet) { f.hubStatus, f.hubBody = 200, hubBody(hubLegal(), hubNotice(), hubForeign()) })
	v, err := r.api.FleetPendingApprovals(context.Background())
	if err != nil || !v.Available || len(v.Items) != 3 || v.RequiredCount != 2 {
		t.Fatalf("view = %+v, %v", v, err)
	}
	legal, notice, foreign := v.Items[0], v.Items[1], v.Items[2]
	if legal.DocumentURL != "https://kenaz.kameas.ai/terms.html" || legal.DocumentSHA256 != hubSHA ||
		legal.Version != "v1.1" || !legal.ApproveAllowed || legal.Blocking != "Continued use of Fleet" {
		t.Errorf("legal = %+v", legal)
	}
	if notice.BodyText != hubNoticeText || !notice.ApproveAllowed {
		t.Errorf("notice = %+v", notice)
	}
	// Unknown kind kept for generic rendering; its foreign action is not approvable here.
	if foreign.Kind != "future_kind" || foreign.ApproveAllowed {
		t.Errorf("foreign = %+v", foreign)
	}
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), "/api/v1/") {
		t.Errorf("an approve path leaked to the frontend view: %s", raw)
	}
}

func TestFleetPendingApprovals_DeferrableUntilReachesTheView(t *testing.T) {
	r := newMLRig(t, true)
	deferred := hubLegal()
	deferred["deferrable_until"] = "2026-11-21T08:00:00Z"
	r.fleet.set(func(f *mlFakeFleet) { f.hubStatus, f.hubBody = 200, hubBody(deferred, hubNotice()) })
	v, err := r.api.FleetPendingApprovals(context.Background())
	if err != nil || len(v.Items) != 2 {
		t.Fatalf("view = %+v, %v", v, err)
	}
	if got := v.Items[0].DeferrableUntil; got != "2026-11-21T08:00:00Z" {
		t.Errorf("legal deferrableUntil = %q", got)
	}
	// Still required: deferral changes when it blocks, not whether.
	if !v.Items[0].Required || v.RequiredCount != 2 {
		t.Errorf("deferred item must stay required: %+v", v)
	}
	if got := v.Items[1].DeferrableUntil; got != "" {
		t.Errorf("notice deferrableUntil = %q, want \"\"", got)
	}
}

func TestFleetPendingApprovals_ExclusionsChangeOnlyWhenNothingRequired(t *testing.T) {
	r := newMLRig(t, true)
	r.fleet.set(func(f *mlFakeFleet) { f.hubStatus, f.hubBody = 200, hubBody(hubLegal(), hubExclusionsChange()) })
	v, _ := r.api.FleetPendingApprovals(context.Background())
	if len(v.Items) != 1 || v.Items[0].Kind != fleet.ApprovalKindLegalAcceptance {
		t.Errorf("with a required item pending, items = %+v", v.Items)
	}
	// It cannot be approved while hidden either.
	if got, err := r.api.FleetApproveItem(context.Background(), "ml_exclusions_change:o1:5"); err != nil || !got.Changed {
		t.Errorf("approve hidden = %+v, %v", got, err)
	}
	if _, posts, _ := r.fleet.hubSnapshot(); len(posts) != 0 {
		t.Errorf("hidden item approved: %v", posts)
	}

	r.fleet.set(func(f *mlFakeFleet) { f.hubBody = hubBody(hubExclusionsChange()) })
	v, _ = r.api.FleetPendingApprovals(context.Background())
	if len(v.Items) != 1 || v.Items[0].Kind != fleet.ApprovalKindMLExclusionsChange || v.RequiredCount != 0 {
		t.Errorf("with nothing required, items = %+v", v.Items)
	}
	// Dismiss → exclusions-seen with the body verbatim.
	if _, err := r.api.FleetApproveItem(context.Background(), "ml_exclusions_change:o1:5"); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if _, posts, _ := r.fleet.hubSnapshot(); len(posts) != 1 || posts[0] != `/api/v1/me/ml/exclusions-seen {"exclusions_version":5}` {
		t.Errorf("posts = %q", posts)
	}
}

func TestFleetPendingApprovals_SuspendedOrgEmpty(t *testing.T) {
	r := newMLRig(t, true)
	r.fleet.set(func(f *mlFakeFleet) { f.hubStatus, f.hubBody = 200, hubBody() })
	v, err := r.api.FleetPendingApprovals(context.Background())
	if err != nil || !v.Available || len(v.Items) != 0 || v.RequiredCount != 0 {
		t.Errorf("suspended = %+v, %v", v, err)
	}
}

func TestFleetApproveItem_MLNoticeSendsVerbatimBody(t *testing.T) {
	r := newMLRig(t, true)
	r.fleet.set(func(f *mlFakeFleet) {
		f.hubStatus, f.hubBody = 200, hubBody(hubNotice())
		f.ackBody = mlBody(true, "on", false, false, true, 4, "2026-10-09T13:00:00Z", 90, false)
	})
	if _, err := r.api.FleetApproveItem(context.Background(), "ml_notice:o1:4"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, _, acks := r.fleet.hubSnapshot(); len(acks) != 1 || acks[0] != `{"notice_version":4,"text_revision":2}` {
		t.Errorf("notice-ack bodies = %q", acks)
	}
}

// kenaz-fleet #235: the rev-2 ml_notice approve body carries
// ai_usage_epoch. The hub path forwards Fleet's body byte for byte (key
// order and all) — the harness never rebuilds it, so the epoch it echoes
// is exactly the one Fleet put there.
func TestFleetApproveItem_MLNoticeForwardsHubBodyByteForByte(t *testing.T) {
	const approveBody = `{"notice_version":4,"text_revision":2,"ai_usage_epoch":9}`
	item, _ := json.Marshal(map[string]any{
		"id": "ml_notice:o1:4", "kind": "ml_notice", "title": "Your organization has enabled workflow data offload",
		"summary": "Read how your activity is uploaded.", "body_text": "What's new\n\nAI usage is now included.",
		"version": "4", "blocking": "Offload for Acme Corp", "required": true,
		"approve": map[string]any{"method": "POST", "path": "/api/v1/me/ml/notice-ack", "body": json.RawMessage(approveBody)},
	})
	r := newMLRig(t, true)
	r.fleet.set(func(f *mlFakeFleet) {
		f.hubStatus, f.hubBody = 200, `{"items":[`+string(item)+`],"count":1}`
		f.ackBody = withAIUsage(mlBody(true, "on", false, false, true, 4, "2026-10-09T13:00:00Z", 90, false), true, 9)
	})
	if _, err := r.api.FleetApproveItem(context.Background(), "ml_notice:o1:4"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, _, acks := r.fleet.hubSnapshot(); len(acks) != 1 || acks[0] != approveBody {
		t.Errorf("notice-ack bodies = %q, want exactly %q", acks, approveBody)
	}
}

func TestFleetApproveItem_LegalSendsVerbatimBody(t *testing.T) {
	r := newMLRig(t, true)
	r.fleet.set(func(f *mlFakeFleet) { f.hubStatus, f.hubBody = 200, hubBody(hubLegal()) })
	if _, err := r.api.FleetApproveItem(context.Background(), "legal:terms:v1.1"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	want := `/api/v1/me/legal-acceptances {"acceptances":[{"kind":"terms","version":"v1.1"}]}`
	if _, posts, _ := r.fleet.hubSnapshot(); len(posts) != 1 || posts[0] != want {
		t.Errorf("posts = %q, want %q", posts, want)
	}
}

func TestFleetApproveItem_ForeignPathRefusedNothingSent(t *testing.T) {
	r := newMLRig(t, true)
	r.fleet.set(func(f *mlFakeFleet) { f.hubStatus, f.hubBody = 200, hubBody(hubForeign()) })
	_, err := r.api.FleetApproveItem(context.Background(), "future:1")
	if !errors.Is(err, fleet.ErrApproveActionNotAllowed) {
		t.Fatalf("err = %v, want ErrApproveActionNotAllowed", err)
	}
	if _, posts, acks := r.fleet.hubSnapshot(); len(posts) != 0 || len(acks) != 0 {
		t.Errorf("a refused action reached Fleet: posts=%q acks=%q", posts, acks)
	}
}

func TestFleetApproveItem_StaleAndUnknownComeBackChanged(t *testing.T) {
	r := newMLRig(t, true)
	r.fleet.set(func(f *mlFakeFleet) {
		f.hubStatus, f.hubBody = 200, hubBody(hubLegal())
		f.hubPostSt, f.hubPostRes = 400, `{"code":"legal_acceptance_outdated"}`
	})
	v, err := r.api.FleetApproveItem(context.Background(), "legal:terms:v1.1")
	if err != nil || !v.Changed || len(v.Items) != 1 {
		t.Errorf("stale = %+v, %v", v, err)
	}
	v, err = r.api.FleetApproveItem(context.Background(), "no-such-id")
	if err != nil || !v.Changed {
		t.Errorf("unknown id = %+v, %v", v, err)
	}
}

// ── Cloud ML panel: hub notice vs fallback ─────────────────────────────

func TestFleetMLStatus_HubNoticeReplacesLocalTemplate(t *testing.T) {
	r := newMLRig(t, true)
	r.fleet.set(func(f *mlFakeFleet) {
		// A rev-2 Fleet: without the hub this would be the dashboard state.
		f.state = withTextRevisions(mlBody(true, "on", false, true, false, 4, "", 90, false), 2, 1)
		f.hubStatus, f.hubBody = 200, hubBody(hubLegal(), hubNotice())
	})
	v, err := r.api.FleetMLStatus(context.Background())
	if err != nil {
		t.Fatalf("FleetMLStatus: %v", err)
	}
	if !v.NoticeFromHub || v.NoticeItemID != "ml_notice:o1:4" || v.NoticeText != hubNoticeText ||
		v.NoticeNeedsDashboard || v.NoticeDashboardURL != "" {
		t.Errorf("view = %+v, want the hub notice verbatim", v)
	}
	// Fleet's rev-2 ml_notice keeps its heading in the item title.
	if v.NoticeTitle != "Hosted inference notice" || v.Notice != nil {
		t.Errorf("noticeTitle = %q, notice = %+v; want the hub title and no local notice", v.NoticeTitle, v.Notice)
	}
}

func TestFleetMLStatus_FallbackWhenHubUnavailable(t *testing.T) {
	t.Run("404 rev 1: local template", func(t *testing.T) {
		r := newMLRig(t, true)
		r.fleet.set(func(f *mlFakeFleet) {
			f.state = withTextRevisions(mlBody(true, "on", false, true, false, 4, "", 90, false), 1, 0)
		})
		v, _ := r.api.FleetMLStatus(context.Background())
		if v.NoticeFromHub || v.NoticeItemID != "" || v.NoticeText != RenderMLNotice("Acme Corp", 90, "on").PlainText() {
			t.Errorf("view = %+v", v)
		}
	})
	t.Run("404 rev 2: dashboard link", func(t *testing.T) {
		r := newMLRig(t, true)
		r.fleet.set(func(f *mlFakeFleet) {
			f.state = withTextRevisions(mlBody(true, "on", false, true, false, 4, "", 90, false), 2, 1)
		})
		v, _ := r.api.FleetMLStatus(context.Background())
		if v.NoticeFromHub || !v.NoticeNeedsDashboard || v.NoticeText != "" || v.NoticeDashboardURL == "" {
			t.Errorf("view = %+v", v)
		}
	})
	t.Run("hub served, no ml_notice item: local template", func(t *testing.T) {
		r := newMLRig(t, true)
		r.fleet.set(func(f *mlFakeFleet) {
			f.state = mlBody(true, "on", false, true, false, 4, "", 90, false)
			f.hubStatus, f.hubBody = 200, hubBody(hubLegal())
		})
		v, _ := r.api.FleetMLStatus(context.Background())
		if v.NoticeFromHub || v.NoticeText != RenderMLNotice("Acme Corp", 90, "on").PlainText() {
			t.Errorf("view = %+v", v)
		}
	})
	t.Run("no ack required: hub not read", func(t *testing.T) {
		r := newMLRig(t, true)
		r.fleet.set(func(f *mlFakeFleet) {
			f.state = mlBody(true, "on", false, false, true, 4, "2026-10-09T13:00:00Z", 90, false)
			f.hubStatus, f.hubBody = 200, hubBody(hubNotice())
		})
		v, _ := r.api.FleetMLStatus(context.Background())
		if gets, _, _ := r.fleet.hubSnapshot(); gets != 0 || v.NoticeFromHub {
			t.Errorf("hub gets = %d, view = %+v", gets, v)
		}
	})
}

// The gate is unchanged (Fleet ruling): approving the hub ml_notice posts
// the ack, and /me/ml's effective — the only thing the producer gate reads —
// is what reopens it.
func TestFleetApproveItem_MLNoticeAckThenMeMLEffective(t *testing.T) {
	r := newMLRig(t, true)
	r.fleet.set(func(f *mlFakeFleet) {
		f.state = mlBody(true, "on", false, true, false, 4, "", 90, false)
		f.hubStatus, f.hubBody = 200, hubBody(hubNotice())
	})
	if v, _ := r.api.FleetMLStatus(context.Background()); v.Effective || !v.NoticeFromHub {
		t.Fatalf("before = %+v", v)
	}
	if _, err := r.api.FleetApproveItem(context.Background(), "ml_notice:o1:4"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	// Fleet records the ack: /me/ml turns effective and the hub item goes.
	r.fleet.set(func(f *mlFakeFleet) {
		f.state = mlBody(true, "on", false, false, true, 4, "2026-10-09T13:00:00Z", 90, false)
		f.hubBody = hubBody()
	})
	if v, _ := r.api.FleetMLStatus(context.Background()); !v.Effective || v.NoticeAckRequired {
		t.Errorf("after = %+v", v)
	}
}

func hubOptInOffer(version string) map[string]any {
	return map[string]any{
		"id": "ml_opt_in_available:o1", "kind": "ml_opt_in_available", "title": "Turn on offload?",
		"summary": "Your organization lets you choose.", "version": version,
		"blocking": "Nothing is paused", "required": false,
		"approve": map[string]any{"method": "POST", "path": "/api/v1/me/ml/opt-in-prompt-dismissed",
			"body": map[string]any{"notice_version": 3}},
	}
}

// hubFutureInfo is an informational item of a kind this build doesn't know,
// whose action the harness does not send: "Not now" is local only.
func hubFutureInfo(version string) map[string]any {
	return map[string]any{
		"id": "future_info:1", "kind": "future_info", "title": "Something new",
		"summary": "Informational.", "version": version,
		"blocking": "Nothing is paused", "required": false,
		"approve": map[string]any{"method": "POST", "path": "/api/v1/me/future/seen", "body": map[string]any{}},
	}
}

// Legal (kameas-ai-11, final ruling 2026-10-16): the "Offload available"
// prompt's "Not now" is member-private and is sent as the hub's own action
// (POST /me/ml/opt-in-prompt-dismissed {notice_version}). That route is
// bound to the ml_opt_in_available kind, so an ml_notice carrying it is not
// approvable (the notice's "Not now" is never recorded).
func TestFleetApproveItem_OptInPromptNotNowIsSentNoticeCannotUseIt(t *testing.T) {
	r := newMLRig(t, true)
	notice := hubNotice()
	notice["id"], notice["version"] = "ml_notice:o1:9", "9"
	notice["approve"] = map[string]any{"method": "POST", "path": "/api/v1/me/ml/opt-in-prompt-dismissed",
		"body": map[string]any{"notice_version": 9}}
	r.fleet.set(func(f *mlFakeFleet) { f.hubStatus, f.hubBody = 200, hubBody(hubOptInOffer("1"), notice) })
	ctx := context.Background()

	v, err := r.api.FleetPendingApprovals(ctx)
	if err != nil || len(v.Items) != 2 {
		t.Fatalf("view = %+v, %v", v, err)
	}
	for _, it := range v.Items {
		switch it.Kind {
		case "ml_opt_in_available":
			if !it.ApproveAllowed || it.LocalDismiss {
				t.Fatalf("opt-in offer: approveAllowed=%v localDismiss=%v, want true/false", it.ApproveAllowed, it.LocalDismiss)
			}
		case "ml_notice":
			if it.ApproveAllowed {
				t.Fatal("an ml_notice carrying the opt-in dismissal route must not be approvable")
			}
		}
	}
	if _, err := r.api.FleetApproveItem(ctx, "ml_notice:o1:9"); !errors.Is(err, fleet.ErrApproveActionNotAllowed) {
		t.Fatalf("notice via dismissal route err = %v, want ErrApproveActionNotAllowed", err)
	}
	if _, posts, _ := r.fleet.hubSnapshot(); len(posts) != 0 {
		t.Fatalf("the notice reached the dismissal route: %v", posts)
	}
	if _, err := r.api.FleetApproveItem(ctx, "ml_opt_in_available:o1"); err != nil {
		t.Fatalf("opt-in Not now: %v", err)
	}
	if _, posts, _ := r.fleet.hubSnapshot(); len(posts) != 1 || !strings.Contains(posts[0], "opt-in-prompt-dismissed") {
		t.Fatalf("opt-in Not now posts = %v, want one POST to opt-in-prompt-dismissed", posts)
	}
}

// An informational item whose action the harness does not send can be
// hidden on this device ("Not now"). Nothing reaches Fleet; a revised
// version shows again; required items can't be dismissed.
func TestFleetDismissItemLocally_NeverSentAndVersionScoped(t *testing.T) {
	r := newMLRig(t, true)
	r.fleet.set(func(f *mlFakeFleet) { f.hubStatus, f.hubBody = 200, hubBody(hubFutureInfo("1")) })
	ctx := context.Background()

	v, err := r.api.FleetPendingApprovals(ctx)
	if err != nil || len(v.Items) != 1 || v.Items[0].ApproveAllowed || !v.Items[0].LocalDismiss {
		t.Fatalf("view = %+v, %v; want a locally dismissible item", v, err)
	}
	v, err = r.api.FleetDismissItemLocally(ctx, "future_info:1")
	if err != nil || len(v.Items) != 0 {
		t.Fatalf("after Not now = %+v, %v; want the item hidden", v, err)
	}
	if _, posts, _ := r.fleet.hubSnapshot(); len(posts) != 0 {
		t.Fatalf("a local Not now reached Fleet: %v", posts)
	}
	if v, _ := r.api.FleetPendingApprovals(ctx); len(v.Items) != 0 {
		t.Fatalf("dismissed item came back at the same version: %+v", v)
	}
	r.fleet.set(func(f *mlFakeFleet) { f.hubBody = hubBody(hubFutureInfo("2")) })
	if v, _ := r.api.FleetPendingApprovals(ctx); len(v.Items) != 1 {
		t.Fatalf("revised item hidden: %+v", v)
	}
	r.fleet.set(func(f *mlFakeFleet) { f.hubBody = hubBody(hubLegal()) })
	if _, err := r.api.FleetDismissItemLocally(ctx, "legal:terms:v1.1"); !errors.Is(err, ErrNotLocallyDismissible) {
		t.Fatalf("dismiss required err = %v", err)
	}
}

// #427 re-review: a malformed opt-in offer whose approve action points at
// an ack or acceptance route must not turn "Not now" into a recorded ack. It
// is not approvable; "Not now" is local only, and nothing is sent.
func TestFleetApproveItem_MalformedOptInOfferCannotRecordAnAck(t *testing.T) {
	r := newMLRig(t, true)
	bad := hubOptInOffer("1")
	bad["approve"] = map[string]any{"method": "POST", "path": "/api/v1/me/ml/notice-ack",
		"body": map[string]any{"notice_version": 4, "text_revision": 2}}
	r.fleet.set(func(f *mlFakeFleet) { f.hubStatus, f.hubBody = 200, hubBody(bad) })
	ctx := context.Background()

	v, err := r.api.FleetPendingApprovals(ctx)
	if err != nil || len(v.Items) != 1 || v.Items[0].ApproveAllowed || !v.Items[0].LocalDismiss {
		t.Fatalf("malformed offer view = %+v, %v; want not approvable, local dismiss only", v, err)
	}
	if _, err := r.api.FleetApproveItem(ctx, "ml_opt_in_available:o1"); !errors.Is(err, fleet.ErrApproveActionNotAllowed) {
		t.Fatalf("approve malformed offer err = %v, want ErrApproveActionNotAllowed", err)
	}
	if _, _, acks := r.fleet.hubSnapshot(); len(acks) != 0 {
		t.Fatalf("a Not now on a malformed offer recorded a notice ack: %v", acks)
	}
	if _, posts, _ := r.fleet.hubSnapshot(); len(posts) != 0 {
		t.Fatalf("a malformed offer sent: %v", posts)
	}
}
