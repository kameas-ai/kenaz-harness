package fleet

// approvals_test.go — ml-producer-01MLPRD01 WP06: the pending-approvals hub
// decode (strict known keys, tolerant extras, unknown kinds kept), the
// approve allowlist (a foreign path sends nothing), verbatim bodies, the
// 404 "older Fleet" sentinel and the stale-item errors.

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// hubContractExample is the GET /api/v1/me/pending-approvals example from
// kenaz-fleet docs/contract-pending-approvals.md, with the elided hash and
// body filled in.
const hubContractExample = `{
  "items": [
    {
      "id": "legal:terms:v1.1",
      "kind": "legal_acceptance",
      "title": "Terms of Use",
      "summary": "Review and accept the current Terms of Use (v1.1).",
      "document_url": "https://kenaz.kameas.ai/terms.html",
      "document_sha256": "0f96e53b0f96e53b0f96e53b0f96e53b0f96e53b0f96e53b0f96e53b0f96e53b",
      "version": "v1.1",
      "blocking": "Continued use of Fleet",
      "required": true,
      "approve": {
        "method": "POST",
        "path": "/api/v1/me/legal-acceptances",
        "body": {"acceptances": [{"kind": "terms", "version": "v1.1"}]}
      }
    },
    {
      "id": "ml_notice:org-1:2",
      "kind": "ml_notice",
      "title": "Hosted inference notice",
      "summary": "Read how your activity is uploaded for hosted inference.",
      "body_text": "Acme has turned on hosted inference.\n\nChanged since you last approved: hr/** removed.",
      "version": "2",
      "blocking": "Hosted inference uploads for Acme",
      "required": true,
      "approve": {
        "method": "POST",
        "path": "/api/v1/me/ml/notice-ack",
        "body": {"notice_version": 2, "text_revision": 2}
      }
    }
  ],
  "count": 2
}`

func hubItem(fields string) string {
	return `{"items":[{` + fields + `}],"count":1}`
}

const minimalItemFields = `"id":"x:1","kind":"future_kind","title":"T","summary":"S","version":"1",` +
	`"blocking":"Nothing is paused","required":false,"approve":{"method":"POST","path":"/api/v1/me/ml/exclusions-seen","body":{"exclusions_version":1}}`

func TestDecodePendingApprovals_Table(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr bool
		check   func(t *testing.T, p PendingApprovals)
	}{
		{name: "contract example", body: hubContractExample, check: func(t *testing.T, p PendingApprovals) {
			if len(p.Items) != 2 || p.Count != 2 {
				t.Fatalf("items = %d count = %d", len(p.Items), p.Count)
			}
			legal := p.Items[0]
			if legal.Kind != ApprovalKindLegalAcceptance || legal.DocumentURL != "https://kenaz.kameas.ai/terms.html" ||
				legal.Version != "v1.1" || legal.Blocking != "Continued use of Fleet" || !legal.Required ||
				legal.Approve.Method != "POST" || legal.Approve.Path != ApprovePathLegalAcceptances {
				t.Errorf("legal = %+v", legal)
			}
			n, ok := p.Find("ml_notice:org-1:2")
			if !ok || n.Kind != ApprovalKindMLNotice || !strings.Contains(n.BodyText, "Changed since you last approved") {
				t.Errorf("notice = %+v", n)
			}
			if string(n.Approve.Body) != `{"notice_version": 2, "text_revision": 2}` {
				t.Errorf("approve body not kept verbatim: %s", n.Approve.Body)
			}
		}},
		{name: "unknown kind kept", body: hubItem(minimalItemFields), check: func(t *testing.T, p PendingApprovals) {
			if len(p.Items) != 1 || p.Items[0].Kind != "future_kind" || p.Items[0].BodyText != "" || p.Items[0].DocumentURL != "" {
				t.Errorf("%+v", p)
			}
		}},
		{name: "unknown top-level and item keys tolerated",
			body: `{"future":1,"items":[{"link":"/settings#x","extra":{"a":1},` + minimalItemFields + `}],"count":1}`,
			check: func(t *testing.T, p PendingApprovals) {
				if len(p.Items) != 1 || p.Items[0].ID != "x:1" {
					t.Errorf("%+v", p)
				}
			}},
		{name: "suspended org: empty list", body: `{"items":[],"count":0}`, check: func(t *testing.T, p PendingApprovals) {
			if len(p.Items) != 0 {
				t.Errorf("%+v", p)
			}
		}},
		{name: "foreign approve path still decodes (refused only on approve)",
			body: hubItem(strings.Replace(minimalItemFields, "/api/v1/me/ml/exclusions-seen", "/api/v1/admin/nuke", 1)),
			check: func(t *testing.T, p PendingApprovals) {
				if p.Items[0].Approve.Path != "/api/v1/admin/nuke" {
					t.Errorf("%+v", p)
				}
			}},
		{name: "items missing", body: `{"count":0}`, wantErr: true},
		{name: "items null", body: `{"items":null,"count":0}`, wantErr: true},
		{name: "count missing", body: `{"items":[]}`, wantErr: true},
		{name: "blocking as bool refused (it is a string)", body: hubItem(strings.Replace(minimalItemFields, `"blocking":"Nothing is paused"`, `"blocking":true`, 1)), wantErr: true},
		{name: "required as string refused", body: hubItem(strings.Replace(minimalItemFields, `"required":false`, `"required":"no"`, 1)), wantErr: true},
		{name: "id missing", body: hubItem(strings.Replace(minimalItemFields, `"id":"x:1",`, ``, 1)), wantErr: true},
		{name: "version missing", body: hubItem(strings.Replace(minimalItemFields, `"version":"1",`, ``, 1)), wantErr: true},
		{name: "approve missing", body: `{"items":[{"id":"a","kind":"k","title":"t","summary":"s","version":"1","blocking":"b","required":true}],"count":1}`, wantErr: true},
		{name: "approve.body not an object", body: hubItem(strings.Replace(minimalItemFields, `"body":{"exclusions_version":1}`, `"body":"x"`, 1)), wantErr: true},
		{name: "approve.path missing", body: hubItem(strings.Replace(minimalItemFields, `"path":"/api/v1/me/ml/exclusions-seen",`, ``, 1)), wantErr: true},
		{name: "document_url without hash", body: hubItem(minimalItemFields + `,"document_url":"https://kenaz.kameas.ai/terms.html"`), wantErr: true},
		{name: "document_url not http(s)", body: hubItem(minimalItemFields + `,"document_url":"javascript:alert(1)","document_sha256":"` + strings.Repeat("a", 64) + `"`), wantErr: true},
		{name: "duplicate ids", body: `{"items":[{` + minimalItemFields + `},{` + minimalItemFields + `}],"count":2}`, wantErr: true},
		{name: "trailing data", body: `{"items":[],"count":0}{}`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := DecodePendingApprovals([]byte(tc.body))
			if tc.wantErr {
				if err == nil || !errors.Is(err, ErrPendingApprovalsDecode) {
					t.Fatalf("err = %v, want ErrPendingApprovalsDecode", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			tc.check(t, p)
		})
	}
}

func TestApproveActionAllowed_ExactMatch(t *testing.T) {
	allowed := [][2]string{
		{"POST", "/api/v1/me/legal-acceptances"},
		{"POST", "/api/v1/me/ml/notice-ack"},
		{"POST", "/api/v1/me/ml/exclusions-seen"},
	}
	for _, a := range allowed {
		if !ApproveActionAllowed(a[0], a[1]) {
			t.Errorf("%v refused", a)
		}
	}
	refused := [][2]string{
		{"post", "/api/v1/me/ml/notice-ack"},
		{"PUT", "/api/v1/me/ml/notice-ack"},
		{"DELETE", "/api/v1/me/legal-acceptances"},
		{"POST", "/api/v1/me/ml/notice-ack/"},
		{"POST", "/api/v1/me/ml/notice-ack?x=1"},
		{"POST", "/api/v1/me/ml/../ml/notice-ack"},
		{"POST", "https://evil.example/api/v1/me/ml/notice-ack"},
		{"POST", "/api/v1/me/ml/opt-in-prompt-dismissed"},
		{"POST", "/api/v1/me/telemetry-opt-ins"},
		{"POST", ""},
	}
	for _, r := range refused {
		if ApproveActionAllowed(r[0], r[1]) {
			t.Errorf("%v allowed", r)
		}
	}
}

// fakeHubFleet serves the hub and records every request it receives.
type fakeHubFleet struct {
	mu         sync.Mutex
	listStatus int
	listBody   string
	postStatus int
	postBody   string
	requests   []string // "METHOD path body"
}

func (f *fakeHubFleet) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path+" "+string(b))
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet && r.URL.Path == "/api/v1/me/pending-approvals" {
		w.WriteHeader(f.listStatus)
		_, _ = io.WriteString(w, f.listBody)
		return
	}
	w.WriteHeader(f.postStatus)
	_, _ = io.WriteString(w, f.postBody)
}

func (f *fakeHubFleet) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func newHubTestClient(t *testing.T, f *fakeHubFleet) *Client {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	if err := SaveTokens(TokenSet{AccessToken: "at-hub", RefreshToken: "rt-hub", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	t.Cleanup(func() { _ = ClearTokens() })
	return newTestClient(t, srv)
}

func TestGetPendingApprovals_OKAnd404(t *testing.T) {
	f := &fakeHubFleet{listStatus: 200, listBody: hubContractExample}
	c := newHubTestClient(t, f)
	p, err := c.GetPendingApprovals(t.Context())
	if err != nil || len(p.Items) != 2 {
		t.Fatalf("GetPendingApprovals = %+v, %v", p, err)
	}

	f.mu.Lock()
	f.listStatus, f.listBody = 404, `{"code":"not_found"}`
	f.mu.Unlock()
	if _, err := c.GetPendingApprovals(t.Context()); !errors.Is(err, ErrPendingApprovalsUnavailable) {
		t.Errorf("404 err = %v, want ErrPendingApprovalsUnavailable", err)
	}

	f.mu.Lock()
	f.listStatus, f.listBody = 403, `{"code":"staff_not_permitted"}`
	f.mu.Unlock()
	var ae *ApprovalError
	if _, err := c.GetPendingApprovals(t.Context()); !errors.As(err, &ae) || ae.Status != 403 {
		t.Errorf("403 err = %v", err)
	}
}

func TestApprovePendingItem_SendsBodyVerbatimToAllowlistedPath(t *testing.T) {
	f := &fakeHubFleet{listStatus: 200, listBody: hubContractExample, postStatus: 200, postBody: `{}`}
	c := newHubTestClient(t, f)
	p, err := c.GetPendingApprovals(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	n, _ := p.Find("ml_notice:org-1:2")
	if err := c.ApprovePendingItem(t.Context(), n); err != nil {
		t.Fatalf("approve: %v", err)
	}
	reqs := f.snapshot()
	want := `POST /api/v1/me/ml/notice-ack {"notice_version": 2, "text_revision": 2}`
	if len(reqs) != 2 || reqs[1] != want {
		t.Errorf("requests = %q, want the GET then %q", reqs, want)
	}
}

func TestApprovePendingItem_ForeignPathSendsNothing(t *testing.T) {
	f := &fakeHubFleet{postStatus: 200, postBody: `{}`}
	c := newHubTestClient(t, f)
	for _, act := range []ApproveAction{
		{Method: "POST", Path: "/api/v1/admin/orgs/1/delete", Body: []byte(`{}`)},
		{Method: "DELETE", Path: ApprovePathMLNoticeAck, Body: []byte(`{}`)},
		{Method: "POST", Path: "/api/v1/me/ml/opt-in-prompt-dismissed", Body: []byte(`{"notice_version":1}`)},
	} {
		err := c.ApprovePendingItem(t.Context(), PendingApproval{ID: "x", Kind: "future_kind", Approve: act})
		if !errors.Is(err, ErrApproveActionNotAllowed) {
			t.Errorf("%s %s: err = %v, want ErrApproveActionNotAllowed", act.Method, act.Path, err)
		}
	}
	if reqs := f.snapshot(); len(reqs) != 0 {
		t.Errorf("refused actions reached Fleet: %q", reqs)
	}
}

func TestApprovePendingItem_StaleErrors(t *testing.T) {
	cases := []struct {
		status int
		body   string
		stale  bool
	}{
		{409, `{"code":"policy_changed"}`, true},
		{400, `{"code":"legal_acceptance_outdated"}`, true},
		{400, `{"code":"invalid_request"}`, false},
		{422, `{}`, false},
	}
	for _, tc := range cases {
		f := &fakeHubFleet{postStatus: tc.status, postBody: tc.body}
		c := newHubTestClient(t, f)
		err := c.ApprovePendingItem(t.Context(), PendingApproval{
			Kind:    ApprovalKindLegalAcceptance,
			Approve: ApproveAction{Method: "POST", Path: ApprovePathLegalAcceptances, Body: []byte(`{}`)},
		})
		if err == nil || errors.Is(err, ErrApprovalStale) != tc.stale {
			t.Errorf("%d %s: err = %v, stale want %v", tc.status, tc.body, err, tc.stale)
		}
	}
}

func TestPendingApprovals_NopClient(t *testing.T) {
	nop := NewNopClient()
	if _, err := nop.GetPendingApprovals(t.Context()); !errors.Is(err, ErrFleetDisabled) {
		t.Errorf("get: %v", err)
	}
	if err := nop.ApprovePendingItem(t.Context(), PendingApproval{}); !errors.Is(err, ErrFleetDisabled) {
		t.Errorf("approve: %v", err)
	}
}
