package fleet

// ml_test.go — ml-producer-01MLPRD01 WP01: strict /me/ml decode, the
// notice-ack 409 path, and "an error never reads as effective".

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// contractExample is the GET /api/v1/me/ml example body from
// kenaz-fleet docs/contract-harness-ml.md, verbatim.
const contractExample = `{
  "org_offload_enabled": false,
  "org_policy": "member_choice",
  "user_workflow_events_opted_in": false,
  "notice_ack_required": false,
  "effective": false,
  "notice_version": 1,
  "notice_acked_at": null,
  "retention_days": 90,
  "retain_on_withdrawal": false
}`

const effectiveBody = `{"org_offload_enabled":true,"org_policy":"on","user_workflow_events_opted_in":false,
"notice_ack_required":false,"effective":true,"notice_version":3,"notice_acked_at":"2026-10-09T12:00:00Z",
"retention_days":30,"retain_on_withdrawal":true}`

func TestDecodeMeML_Table(t *testing.T) {
	replace := func(old, new string) string { return strings.Replace(contractExample, old, new, 1) }
	cases := []struct {
		name    string
		body    string
		wantErr bool
		check   func(t *testing.T, m MeML)
	}{
		{name: "contract example", body: contractExample, check: func(t *testing.T, m MeML) {
			if m.OrgOffloadEnabled || m.Effective || m.NoticeAckedAt != nil || m.IsEffective() {
				t.Errorf("default-off example decoded as %+v", m)
			}
			if m.OrgPolicy != MLPolicyMemberChoice || m.RetentionDays != 90 || m.NoticeVersion != 1 {
				t.Errorf("fields: %+v", m)
			}
		}},
		{name: "effective with ack", body: effectiveBody, check: func(t *testing.T, m MeML) {
			if !m.IsEffective() {
				t.Fatalf("IsEffective=false for %+v", m)
			}
			want := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
			if !m.NoticeAckedAt.Equal(want) || !m.RetainOnWithdrawal || m.RetentionDays != 30 || m.NoticeVersion != 3 {
				t.Errorf("fields: %+v", m)
			}
		}},
		{name: "notice required", body: replace(`"notice_ack_required": false`, `"notice_ack_required": true`), check: func(t *testing.T, m MeML) {
			if !m.NoticeAckRequired || m.IsEffective() {
				t.Errorf("%+v", m)
			}
		}},
		{name: "unknown extra key tolerated (additive Fleet change)",
			body: strings.Replace(effectiveBody, "{", `{"some_future_field": {"x": 1},`, 1),
			check: func(t *testing.T, m MeML) {
				if !m.IsEffective() || m.NoticeVersion != 3 || m.OrgPolicy != MLPolicyOn {
					t.Errorf("extra key changed the decode: %+v", m)
				}
			}},
		{name: "missing key refused", body: replace(`"retain_on_withdrawal": false`, `"retain_on_withdrawal_x": false`), wantErr: true},
		{name: "missing notice_acked_at refused", body: replace(`"notice_acked_at": null,`, ``), wantErr: true},
		{name: "unknown policy refused", body: replace(`"member_choice"`, `"maybe"`), wantErr: true},
		{name: "retention below 7 refused", body: replace(`"retention_days": 90`, `"retention_days": 3`), wantErr: true},
		{name: "retention above 90 refused", body: replace(`"retention_days": 90`, `"retention_days": 91`), wantErr: true},
		{name: "notice_version 0 refused", body: replace(`"notice_version": 1`, `"notice_version": 0`), wantErr: true},
		{name: "bad timestamp refused", body: replace(`"notice_acked_at": null`, `"notice_acked_at": "yesterday"`), wantErr: true},
		{name: "wrong type refused", body: replace(`"effective": false`, `"effective": "true"`), wantErr: true},
		{name: "effective without ack refused", body: replace(`"effective": false`, `"effective": true`), wantErr: true},
		{name: "effective while ack required refused", body: strings.Replace(effectiveBody,
			`"notice_ack_required":false`, `"notice_ack_required":true`, 1), wantErr: true},
		{name: "trailing data refused", body: contractExample + `{}`, wantErr: true},
		{name: "not an object", body: `[]`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := DecodeMeML([]byte(tc.body))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("decoded %+v, want error", m)
				}
				if !errors.Is(err, ErrMLDecode) {
					t.Errorf("err %v does not match ErrMLDecode", err)
				}
				if m.IsEffective() {
					t.Error("a refused body read as effective")
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeMeML: %v", err)
			}
			tc.check(t, m)
		})
	}
}

// fakeMLFleet serves /me/ml and /me/ml/notice-ack.
type fakeMLFleet struct {
	mu        sync.Mutex
	getStatus int
	getBody   string
	ackStatus int
	ackBody   string
	acks      []string
	gets      int
}

func (f *fakeMLFleet) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/me/ml":
		f.gets++
		w.WriteHeader(f.getStatus)
		_, _ = io.WriteString(w, f.getBody)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/me/ml/notice-ack":
		b, _ := io.ReadAll(r.Body)
		f.acks = append(f.acks, string(b))
		w.WriteHeader(f.ackStatus)
		_, _ = io.WriteString(w, f.ackBody)
	default:
		http.NotFound(w, r)
	}
}

func newMLTestClient(t *testing.T, f *fakeMLFleet) *Client {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	if err := SaveTokens(TokenSet{AccessToken: "at-ml", RefreshToken: "rt-ml", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	t.Cleanup(func() { _ = ClearTokens() })
	return newTestClient(t, srv)
}

func TestGetMeML_ErrorsNeverReadAsEffective(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		is     error
	}{
		{"staff 403", 403, `{"code":"staff_not_permitted","message":"staff"}`, ErrMLStaffNotPermitted},
		{"user not provisioned", 403, `{"code":"user_not_provisioned","message":"x"}`, nil},
		// Even a 4xx whose body happens to be an effective object is an error.
		{"404 with effective body", 404, effectiveBody, nil},
		{"strict decode failure on 200", 200, `{"effective":true}`, ErrMLDecode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newMLTestClient(t, &fakeMLFleet{getStatus: tc.status, getBody: tc.body})
			m, err := c.GetMeML(t.Context())
			if err == nil {
				t.Fatalf("GetMeML = %+v, want error", m)
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Errorf("err = %v, want errors.Is %v", err, tc.is)
			}
			if m.IsEffective() || m.Effective {
				t.Errorf("error path returned an effective MeML: %+v", m)
			}
		})
	}
}

func TestGetMeML_OK(t *testing.T) {
	c := newMLTestClient(t, &fakeMLFleet{getStatus: 200, getBody: effectiveBody})
	m, err := c.GetMeML(t.Context())
	if err != nil {
		t.Fatalf("GetMeML: %v", err)
	}
	if !m.IsEffective() {
		t.Errorf("want effective, got %+v", m)
	}
}

func TestAckMLNotice_PostsVersionAndReturnsFreshState(t *testing.T) {
	f := &fakeMLFleet{ackStatus: 200, ackBody: effectiveBody}
	c := newMLTestClient(t, f)
	m, err := c.AckMLNotice(t.Context(), 3)
	if err != nil {
		t.Fatalf("AckMLNotice: %v", err)
	}
	if !m.IsEffective() {
		t.Errorf("fresh state not effective: %+v", m)
	}
	if len(f.acks) != 1 {
		t.Fatalf("acks = %d, want 1", len(f.acks))
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(f.acks[0]), &body); err != nil {
		t.Fatalf("ack body %q: %v", f.acks[0], err)
	}
	if v, ok := body["notice_version"].(float64); !ok || v != 3 || len(body) != 1 {
		t.Errorf("ack body = %v, want exactly {notice_version: 3}", body)
	}
}

func TestAckMLNotice_409PolicyChangedIsTyped(t *testing.T) {
	f := &fakeMLFleet{ackStatus: 409, ackBody: `{"code":"policy_changed","message":"the org notice changed since it was fetched; re-read /api/v1/me/ml"}`}
	c := newMLTestClient(t, f)
	m, err := c.AckMLNotice(t.Context(), 1)
	if !errors.Is(err, ErrMLPolicyChanged) {
		t.Fatalf("err = %v, want ErrMLPolicyChanged", err)
	}
	var me *MLError
	if !errors.As(err, &me) || me.Status != 409 || me.Code != MLCodePolicyChanged {
		t.Errorf("err = %#v", err)
	}
	if m.IsEffective() {
		t.Error("409 returned an effective state")
	}
	// A 409 with some other code is NOT policy_changed.
	f.ackBody = `{"code":"something_else"}`
	if _, err := c.AckMLNotice(t.Context(), 1); err == nil || errors.Is(err, ErrMLPolicyChanged) {
		t.Errorf("409 something_else: err = %v", err)
	}
}

func TestAckMLNotice_RefusesVersionZeroWithoutRequest(t *testing.T) {
	f := &fakeMLFleet{ackStatus: 200, ackBody: effectiveBody}
	c := newMLTestClient(t, f)
	if _, err := c.AckMLNotice(t.Context(), 0); err == nil {
		t.Fatal("version 0 accepted")
	}
	if len(f.acks) != 0 {
		t.Errorf("a request was sent for version 0")
	}
}

func TestML_NopClient(t *testing.T) {
	nop := NewNopClient()
	if _, err := nop.GetMeML(t.Context()); !errors.Is(err, ErrFleetDisabled) {
		t.Errorf("GetMeML: %v", err)
	}
	if _, err := nop.AckMLNotice(t.Context(), 1); !errors.Is(err, ErrFleetDisabled) {
		t.Errorf("AckMLNotice: %v", err)
	}
	if err := nop.SetWorkflowEventsOptIn(t.Context(), true); !errors.Is(err, ErrFleetDisabled) {
		t.Errorf("SetWorkflowEventsOptIn: %v", err)
	}
}

func TestSetWorkflowEventsOptIn_PutsTheClass(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/api/v1/me/telemetry-opt-ins" {
			b, _ := io.ReadAll(r.Body)
			got = string(b)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	if err := SaveTokens(TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ClearTokens() })
	c := newTestClient(t, srv)
	if err := c.SetWorkflowEventsOptIn(t.Context(), true); err != nil {
		t.Fatalf("SetWorkflowEventsOptIn: %v", err)
	}
	want := `{"updates":[{"class":"workflow_events","opted_in":true}]}`
	if got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestHostedInferenceCapabilityAndClass(t *testing.T) {
	if string(CapHostedInference) != "hosted_inference" {
		t.Errorf("CapHostedInference = %q", CapHostedInference)
	}
	found := false
	for _, c := range AllCapabilities() {
		found = found || c == CapHostedInference
	}
	if !found {
		t.Error("AllCapabilities() lacks CapHostedInference")
	}
	found = false
	for _, c := range KnownTelemetryClasses {
		found = found || c == "workflow_events"
	}
	if !found {
		t.Error("KnownTelemetryClasses lacks workflow_events")
	}
}
