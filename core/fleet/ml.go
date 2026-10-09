package fleet

// ml.go — the harness side of Fleet's hosted-inference consent surface
// (ml-producer-01MLPRD01 WP01; kenaz-fleet docs/contract-harness-ml.md,
// "GET /api/v1/me/ml", "The effective rule", "Required notice and
// acknowledgement").
//
// The ML producer (core/mlproducer, fleet-free) ships agent.* records only
// while BOTH the hosted_inference capability is on AND /me/ml says
// effective with a non-null notice_acked_at. This file is the only place
// those consent bits are read off the wire, so it fails CLOSED:
//
//   - every non-2xx (staff 403, user_not_provisioned, 5xx, org pause) is an
//     error, never a zero-value MeML that a careless caller might read;
//   - the body is decoded strictly on the contract's keys: a missing key, an
//     out-of-contract policy / retention / version, a malformed timestamp, or
//     a self-contradicting object (effective without an ack) is a decode
//     error. Unknown EXTRA keys are tolerated: Fleet adds response fields
//     additively as routine, and refusing them would silently stop the ML
//     lane on every installed harness until users upgrade.
//
// Callers must still treat an error as "not effective": IsEffective folds
// that in for them.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// TelemetryClassWorkflowEvents is the opt-in class a member flips under
// org policy member_choice (contract "workflow_events opt-in class").
const TelemetryClassWorkflowEvents = "workflow_events"

// ML org policies (contract "GET /api/v1/me/ml": org_policy).
const (
	MLPolicyOn           = "on"
	MLPolicyOff          = "off"
	MLPolicyMemberChoice = "member_choice"
)

// Fleet error codes on the /me/ml routes.
const (
	MLCodePolicyChanged     = "policy_changed"
	MLCodeStaffNotPermitted = "staff_not_permitted"
)

// ErrMLPolicyChanged is matched (errors.Is) by the *MLError a stale
// notice-ack gets: 409 policy_changed. The caller re-reads /me/ml and shows
// the (new) notice again.
var ErrMLPolicyChanged = errors.New("fleet: ml notice changed since it was shown (409 policy_changed)")

// ErrMLStaffNotPermitted is matched by a 403 staff_not_permitted: Kameas
// staff accounts never have an ML consent state.
var ErrMLStaffNotPermitted = errors.New("fleet: ml consent routes refuse staff accounts (403 staff_not_permitted)")

// ErrMLDecode wraps every strict-decode refusal of a /me/ml body.
var ErrMLDecode = errors.New("fleet: ml consent response did not match the contract")

// MLError is a non-2xx answer from a /me/ml route.
type MLError struct {
	Status  int
	Code    string
	Message string
}

func (e *MLError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("fleet: ml consent: HTTP %d", e.Status)
	}
	return fmt.Sprintf("fleet: ml consent: HTTP %d %s", e.Status, e.Code)
}

// Is lets errors.Is match the typed sentinels.
func (e *MLError) Is(target error) bool {
	switch target {
	case ErrMLPolicyChanged:
		return e.Status == http.StatusConflict && e.Code == MLCodePolicyChanged
	case ErrMLStaffNotPermitted:
		return e.Status == http.StatusForbidden && e.Code == MLCodeStaffNotPermitted
	}
	return false
}

// MeML is GET /api/v1/me/ml — every contract field, nothing else.
type MeML struct {
	OrgOffloadEnabled         bool   `json:"org_offload_enabled"`
	OrgPolicy                 string `json:"org_policy"`
	UserWorkflowEventsOptedIn bool   `json:"user_workflow_events_opted_in"`
	NoticeAckRequired         bool   `json:"notice_ack_required"`
	Effective                 bool   `json:"effective"`
	NoticeVersion             int    `json:"notice_version"`
	// NoticeAckedAt is when this member acknowledged THIS NoticeVersion;
	// nil until they do. The producer must not transmit while it is nil.
	NoticeAckedAt      *time.Time `json:"notice_acked_at"`
	RetentionDays      int        `json:"retention_days"`
	RetainOnWithdrawal bool       `json:"retain_on_withdrawal"`
}

// IsEffective is the one predicate a sender may branch on: effective AND a
// non-null ack (the contract says read both). A nil MeML is not effective.
func (m *MeML) IsEffective() bool {
	return m != nil && m.Effective && m.NoticeAckedAt != nil && !m.NoticeAckRequired
}

// mlWire has one pointer per key so a MISSING key is distinguishable from a
// zero value. notice_acked_at is json.RawMessage so "null" (present, not
// acked) is distinguishable from absent.
type mlWire struct {
	OrgOffloadEnabled         *bool           `json:"org_offload_enabled"`
	OrgPolicy                 *string         `json:"org_policy"`
	UserWorkflowEventsOptedIn *bool           `json:"user_workflow_events_opted_in"`
	NoticeAckRequired         *bool           `json:"notice_ack_required"`
	Effective                 *bool           `json:"effective"`
	NoticeVersion             *int            `json:"notice_version"`
	NoticeAckedAt             json.RawMessage `json:"notice_acked_at"`
	RetentionDays             *int            `json:"retention_days"`
	RetainOnWithdrawal        *bool           `json:"retain_on_withdrawal"`
}

// DecodeMeML decodes a /me/ml (or notice-ack) response body: every contract
// key required and validated; unknown extra keys ignored (additive Fleet
// changes must not break consent reads).
func DecodeMeML(raw []byte) (MeML, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var w mlWire
	if err := dec.Decode(&w); err != nil {
		return MeML{}, fmt.Errorf("%w: %v", ErrMLDecode, err)
	}
	if dec.More() {
		return MeML{}, fmt.Errorf("%w: trailing data after the object", ErrMLDecode)
	}
	missing := func(k string) error { return fmt.Errorf("%w: missing key %q", ErrMLDecode, k) }
	switch {
	case w.OrgOffloadEnabled == nil:
		return MeML{}, missing("org_offload_enabled")
	case w.OrgPolicy == nil:
		return MeML{}, missing("org_policy")
	case w.UserWorkflowEventsOptedIn == nil:
		return MeML{}, missing("user_workflow_events_opted_in")
	case w.NoticeAckRequired == nil:
		return MeML{}, missing("notice_ack_required")
	case w.Effective == nil:
		return MeML{}, missing("effective")
	case w.NoticeVersion == nil:
		return MeML{}, missing("notice_version")
	case w.NoticeAckedAt == nil:
		return MeML{}, missing("notice_acked_at")
	case w.RetentionDays == nil:
		return MeML{}, missing("retention_days")
	case w.RetainOnWithdrawal == nil:
		return MeML{}, missing("retain_on_withdrawal")
	}
	m := MeML{
		OrgOffloadEnabled:         *w.OrgOffloadEnabled,
		OrgPolicy:                 *w.OrgPolicy,
		UserWorkflowEventsOptedIn: *w.UserWorkflowEventsOptedIn,
		NoticeAckRequired:         *w.NoticeAckRequired,
		Effective:                 *w.Effective,
		NoticeVersion:             *w.NoticeVersion,
		RetentionDays:             *w.RetentionDays,
		RetainOnWithdrawal:        *w.RetainOnWithdrawal,
	}
	switch m.OrgPolicy {
	case MLPolicyOn, MLPolicyOff, MLPolicyMemberChoice:
	default:
		return MeML{}, fmt.Errorf("%w: unknown org_policy %q", ErrMLDecode, m.OrgPolicy)
	}
	if m.RetentionDays < 7 || m.RetentionDays > 90 {
		return MeML{}, fmt.Errorf("%w: retention_days %d outside 7..90", ErrMLDecode, m.RetentionDays)
	}
	if m.NoticeVersion < 1 {
		return MeML{}, fmt.Errorf("%w: notice_version %d < 1", ErrMLDecode, m.NoticeVersion)
	}
	if string(bytes.TrimSpace(w.NoticeAckedAt)) != "null" {
		var s string
		if err := json.Unmarshal(w.NoticeAckedAt, &s); err != nil {
			return MeML{}, fmt.Errorf("%w: notice_acked_at: %v", ErrMLDecode, err)
		}
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return MeML{}, fmt.Errorf("%w: notice_acked_at: %v", ErrMLDecode, err)
		}
		m.NoticeAckedAt = &t
	}
	// Self-consistency: effective = ships AND acked. An object claiming
	// effective without an ack, or while an ack is still required, is not
	// something a sender may trust.
	if m.Effective && (m.NoticeAckedAt == nil || m.NoticeAckRequired) {
		return MeML{}, fmt.Errorf("%w: effective=true without a current notice ack", ErrMLDecode)
	}
	return m, nil
}

// mlErrEnvelope is Fleet's {code,message,details} error shape.
type mlErrEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// readMLResponse turns a /me/ml-shaped response into MeML or a typed error.
func readMLResponse(resp *http.Response) (MeML, error) {
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return MeML{}, fmt.Errorf("fleet: ml consent: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		e := &MLError{Status: resp.StatusCode}
		var env mlErrEnvelope
		if json.Unmarshal(raw, &env) == nil {
			e.Code, e.Message = env.Code, env.Message
		}
		return MeML{}, e
	}
	return DecodeMeML(raw)
}

// GetMeML reads GET /api/v1/me/ml. Any error means "not effective".
func (c *Client) GetMeML(ctx context.Context) (MeML, error) {
	if c == nil || c.isNop {
		return MeML{}, ErrFleetDisabled
	}
	resp, err := c.Get(ctx, "/api/v1/me/ml")
	if err != nil {
		return MeML{}, fmt.Errorf("fleet: get /me/ml: %w", err)
	}
	return readMLResponse(resp)
}

// AckMLNotice posts POST /api/v1/me/ml/notice-ack {notice_version} — the
// version of the notice that was actually shown. A stale version answers
// 409 policy_changed (errors.Is(err, ErrMLPolicyChanged)): re-read and show
// the notice again. On success Fleet returns the fresh /me/ml object.
func (c *Client) AckMLNotice(ctx context.Context, noticeVersion int) (MeML, error) {
	if c == nil || c.isNop {
		return MeML{}, ErrFleetDisabled
	}
	if noticeVersion < 1 {
		return MeML{}, fmt.Errorf("fleet: ack ml notice: notice_version %d < 1", noticeVersion)
	}
	resp, err := c.PostJSON(ctx, "/api/v1/me/ml/notice-ack", map[string]int{"notice_version": noticeVersion})
	if err != nil {
		return MeML{}, fmt.Errorf("fleet: ack ml notice: %w", err)
	}
	return readMLResponse(resp)
}

// SetWorkflowEventsOptIn writes the member's own workflow_events choice via
// PUT /api/v1/me/telemetry-opt-ins (contract "workflow_events opt-in class").
func (c *Client) SetWorkflowEventsOptIn(ctx context.Context, optedIn bool) error {
	return c.PutTelemetryOptIns(ctx, []TelemetryOptInItem{{Class: TelemetryClassWorkflowEvents, OptedIn: optedIn}})
}
