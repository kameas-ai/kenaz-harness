package fleet

// ml_test.go — ml-producer-01MLPRD01 WP01: strict /me/ml decode, the
// notice-ack 409 path, and "an error never reads as effective".

import (
	"encoding/json"
	"errors"
	"fmt"
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
  "retain_on_withdrawal": false,
  "exclusions": { "paths": [], "commands": [], "exclude_browser": false },
  "exclusions_version": 1,
  "legacy_exclusion_notes": []
}`

const effectiveBody = `{"org_offload_enabled":true,"org_policy":"on","user_workflow_events_opted_in":false,
"notice_ack_required":false,"effective":true,"notice_version":3,"notice_acked_at":"2026-10-09T12:00:00Z",
"retention_days":30,"retain_on_withdrawal":true,
"exclusions":{"paths":["hr/**","**/secrets/*"],"commands":["ssh","git push"],"exclude_browser":true},
"exclusions_version":4,"legacy_exclusion_notes":["no HR folders please"]}`

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
		{name: "typed exclusions decoded", body: effectiveBody, check: func(t *testing.T, m MeML) {
			e := m.Exclusions
			if len(e.Paths) != 2 || e.Paths[0] != "hr/**" || e.Paths[1] != "**/secrets/*" ||
				len(e.Commands) != 2 || e.Commands[1] != "git push" || !e.ExcludeBrowser {
				t.Errorf("exclusions: %+v", e)
			}
			if m.ExclusionsVersion != 4 || len(m.LegacyExclusionNotes) != 1 || m.LegacyExclusionNotes[0] != "no HR folders please" {
				t.Errorf("version/notes: %d %q", m.ExclusionsVersion, m.LegacyExclusionNotes)
			}
		}},
		{name: "empty exclusions are non-nil empty lists", body: contractExample, check: func(t *testing.T, m MeML) {
			if m.Exclusions.Paths == nil || m.Exclusions.Commands == nil || m.LegacyExclusionNotes == nil {
				t.Errorf("nil list after decode: %+v %#v", m.Exclusions, m.LegacyExclusionNotes)
			}
			if m.ExclusionsVersion != 1 {
				t.Errorf("exclusions_version = %d", m.ExclusionsVersion)
			}
		}},
		// WP05: the exclusion keys are required (contract: "Always
		// present"); a malformed value is a decode error so the gate
		// fails closed.
		{name: "missing exclusions refused", body: replace(`"exclusions": { "paths": [], "commands": [], "exclude_browser": false },`, ``), wantErr: true},
		{name: "missing exclusions_version refused", body: replace(`"exclusions_version": 1,`, ``), wantErr: true},
		{name: "missing legacy_exclusion_notes refused", body: replace(`,
  "legacy_exclusion_notes": []`, ``), wantErr: true},
		{name: "null exclusions refused", body: replace(`{ "paths": [], "commands": [], "exclude_browser": false }`, `null`), wantErr: true},
		{name: "exclusions not an object refused", body: replace(`{ "paths": [], "commands": [], "exclude_browser": false }`, `["hr/**"]`), wantErr: true},
		{name: "null paths refused", body: replace(`"paths": []`, `"paths": null`), wantErr: true},
		{name: "missing commands refused", body: replace(`"commands": [], `, ``), wantErr: true},
		{name: "missing exclude_browser refused", body: replace(`, "exclude_browser": false`, ``), wantErr: true},
		{name: "paths not an array refused", body: replace(`"paths": []`, `"paths": "hr/**"`), wantErr: true},
		{name: "non-string path entry refused", body: replace(`"paths": []`, `"paths": [7]`), wantErr: true},
		{name: "non-string command entry refused", body: replace(`"commands": []`, `"commands": [{"p":"ssh"}]`), wantErr: true},
		{name: "exclude_browser wrong type refused", body: replace(`"exclude_browser": false`, `"exclude_browser": "yes"`), wantErr: true},
		// Unknown keys inside exclusions (rule agreed with Fleet): an EMPTY
		// value excludes nothing and is ignored; anything non-empty is an
		// exclusion this build cannot honour, so the read fails closed.
		{name: "unknown empty list inside exclusions accepted",
			body: replace(`"exclude_browser": false }`, `"exclude_browser": false, "urls": [ ] }`),
			check: func(t *testing.T, m MeML) {
				if len(m.Exclusions.Paths) != 0 || len(m.Exclusions.Commands) != 0 || m.Exclusions.ExcludeBrowser {
					t.Errorf("exclusions = %+v", m.Exclusions)
				}
			}},
		{name: "unknown false inside exclusions accepted",
			body: replace(`"exclude_browser": false }`, `"exclude_browser": false, "exclude_clipboard": false }`),
			check: func(t *testing.T, m MeML) {
				if m.ExclusionsVersion != 1 {
					t.Errorf("decode changed: %+v", m)
				}
			}},
		{name: "unknown non-empty list inside exclusions refused",
			body: replace(`"exclude_browser": false }`, `"exclude_browser": false, "urls": ["x"] }`), wantErr: true},
		{name: "unknown true inside exclusions refused",
			body: replace(`"exclude_browser": false }`, `"exclude_browser": false, "exclude_clipboard": true }`), wantErr: true},
		{name: "unknown string inside exclusions refused",
			body: replace(`"exclude_browser": false }`, `"exclude_browser": false, "urls": "x" }`), wantErr: true},
		{name: "unknown empty string inside exclusions refused (only [] and false are empty)",
			body: replace(`"exclude_browser": false }`, `"exclude_browser": false, "urls": "" }`), wantErr: true},
		{name: "unknown object inside exclusions refused",
			body: replace(`"exclude_browser": false }`, `"exclude_browser": false, "rules": {} }`), wantErr: true},
		{name: "unknown null inside exclusions refused",
			body: replace(`"exclude_browser": false }`, `"exclude_browser": false, "urls": null }`), wantErr: true},
		{name: "51 paths refused", body: replace(`"paths": []`, `"paths": [`+quotedN("p", 51)+`]`), wantErr: true},
		{name: "50 paths accepted", body: replace(`"paths": []`, `"paths": [`+quotedN("p", 50)+`]`), check: func(t *testing.T, m MeML) {
			if len(m.Exclusions.Paths) != 50 {
				t.Errorf("paths = %d", len(m.Exclusions.Paths))
			}
		}},
		{name: "51 commands refused", body: replace(`"commands": []`, `"commands": [`+quotedN("c", 51)+`]`), wantErr: true},
		{name: "257-char entry refused", body: replace(`"commands": []`, `"commands": ["`+strings.Repeat("a", 257)+`"]`), wantErr: true},
		{name: "256-char entry accepted", body: replace(`"commands": []`, `"commands": ["`+strings.Repeat("é", 256)+`"]`), check: func(t *testing.T, m MeML) {
			if len(m.Exclusions.Commands) != 1 {
				t.Errorf("commands = %v", m.Exclusions.Commands)
			}
		}},
		{name: "empty entry refused", body: replace(`"paths": []`, `"paths": ["  "]`), wantErr: true},
		{name: "control character refused", body: replace(`"paths": []`, `"paths": ["a\u0007b"]`), wantErr: true},
		{name: "exclusions_version 0 refused", body: replace(`"exclusions_version": 1`, `"exclusions_version": 0`), wantErr: true},
		{name: "exclusions_version wrong type refused", body: replace(`"exclusions_version": 1`, `"exclusions_version": "1"`), wantErr: true},
		{name: "null legacy notes refused", body: replace(`"legacy_exclusion_notes": []`, `"legacy_exclusion_notes": null`), wantErr: true},
		{name: "legacy notes not strings refused", body: replace(`"legacy_exclusion_notes": []`, `"legacy_exclusion_notes": [1]`), wantErr: true},
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
	m, err := c.AckMLNotice(t.Context(), 3, 0)
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
	m, err := c.AckMLNotice(t.Context(), 1, 0)
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
	if _, err := c.AckMLNotice(t.Context(), 1, 0); err == nil || errors.Is(err, ErrMLPolicyChanged) {
		t.Errorf("409 something_else: err = %v", err)
	}
}

func TestAckMLNotice_RefusesVersionZeroWithoutRequest(t *testing.T) {
	f := &fakeMLFleet{ackStatus: 200, ackBody: effectiveBody}
	c := newMLTestClient(t, f)
	if _, err := c.AckMLNotice(t.Context(), 0, 0); err == nil {
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
	if _, err := nop.AckMLNotice(t.Context(), 1, 0); !errors.Is(err, ErrFleetDisabled) {
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

// quotedN renders n distinct quoted JSON strings, comma-separated.
func quotedN(prefix string, n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf("%q", fmt.Sprintf("%s%d/**", prefix, i))
	}
	return strings.Join(parts, ",")
}

// Fleet PR #225: notice_text_revision / acked_text_revision are OPTIONAL
// (an older Fleet omits them → 0) and must be ≥ 0.
func TestDecodeMeML_TextRevisions(t *testing.T) {
	with := strings.Replace(contractExample, "{", `{"notice_text_revision": 2, "acked_text_revision": 1,`, 1)
	m, err := DecodeMeML([]byte(with))
	if err != nil {
		t.Fatalf("decode with revisions: %v", err)
	}
	if m.NoticeTextRevision != 2 || m.AckedTextRevision != 1 {
		t.Errorf("revisions = %d/%d, want 2/1", m.NoticeTextRevision, m.AckedTextRevision)
	}
	m, err = DecodeMeML([]byte(contractExample))
	if err != nil {
		t.Fatalf("decode without revisions: %v", err)
	}
	if m.NoticeTextRevision != 0 || m.AckedTextRevision != 0 {
		t.Errorf("absent revisions decoded as %d/%d, want 0/0", m.NoticeTextRevision, m.AckedTextRevision)
	}
	for _, bad := range []string{`"notice_text_revision": -1,`, `"acked_text_revision": -1,`, `"notice_text_revision": "2",`} {
		body := strings.Replace(contractExample, "{", "{"+bad, 1)
		if _, err := DecodeMeML([]byte(body)); !errors.Is(err, ErrMLDecode) {
			t.Errorf("%s: err = %v, want ErrMLDecode", bad, err)
		}
	}
}

func TestAckMLNotice_TextRevisionInBodyOnlyWhenPositive(t *testing.T) {
	cases := []struct {
		rev  int
		want map[string]float64
	}{
		{0, map[string]float64{"notice_version": 3}},
		{1, map[string]float64{"notice_version": 3, "text_revision": 1}},
	}
	for _, tc := range cases {
		f := &fakeMLFleet{ackStatus: 200, ackBody: effectiveBody}
		c := newMLTestClient(t, f)
		if _, err := c.AckMLNotice(t.Context(), 3, tc.rev); err != nil {
			t.Fatalf("rev %d: AckMLNotice: %v", tc.rev, err)
		}
		f.mu.Lock()
		acks := append([]string(nil), f.acks...)
		f.mu.Unlock()
		if len(acks) != 1 {
			t.Fatalf("rev %d: acks = %d, want 1", tc.rev, len(acks))
		}
		var body map[string]float64
		if err := json.Unmarshal([]byte(acks[0]), &body); err != nil {
			t.Fatalf("ack body %q: %v", acks[0], err)
		}
		if len(body) != len(tc.want) {
			t.Errorf("rev %d: body = %v, want %v", tc.rev, body, tc.want)
		}
		for k, v := range tc.want {
			if body[k] != v {
				t.Errorf("rev %d: body = %v, want %v", tc.rev, body, tc.want)
			}
		}
	}
}

func TestAckMLNotice_RefusesNegativeTextRevisionWithoutRequest(t *testing.T) {
	f := &fakeMLFleet{ackStatus: 200, ackBody: effectiveBody}
	c := newMLTestClient(t, f)
	if _, err := c.AckMLNotice(t.Context(), 1, -1); err == nil {
		t.Fatal("text_revision -1 accepted")
	}
	if len(f.acks) != 0 {
		t.Error("a request was sent for text_revision -1")
	}
}
