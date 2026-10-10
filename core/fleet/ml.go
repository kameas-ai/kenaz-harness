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
//     error — the typed exclusions included (WP05: required, validated
//     against the contract limits, an unknown key inside them refused
//     unless its value is empty).
//     Unknown EXTRA top-level keys are tolerated: Fleet adds response fields
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
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
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
	// Exclusions are the org's typed exclusions (contract "Typed
	// exclusions"): matched ON DEVICE, before hashing, by the producer
	// (core/mlproducer/exclusions.go). Never nil slices after a decode.
	Exclusions MLExclusions `json:"exclusions"`
	// ExclusionsVersion is bumped by Fleet on every exclusions change.
	ExclusionsVersion int `json:"exclusions_version"`
	// LegacyExclusionNotes are the org's previous FREE-TEXT exclusions:
	// display only, never patterns, never matched (contract: "Producers
	// ignore them").
	LegacyExclusionNotes []string `json:"legacy_exclusion_notes"`
	// NoticeTextRevision is the revision of the notice TEXT Fleet currently
	// requires (kenaz-fleet PR #225; env-wide, e.g. dev=2, stage/prod=1).
	// OPTIONAL on the wire: an older Fleet omits it and it decodes as 0
	// ("no text revisions"). A harness that renders an older revision
	// locally must not acknowledge a newer one.
	NoticeTextRevision int `json:"notice_text_revision"`
	// AckedTextRevision is the text revision this member last acknowledged
	// (0 = none, or an older Fleet that does not send it).
	AckedTextRevision int `json:"acked_text_revision"`
}

// MLExclusions is /me/ml's `exclusions` object.
type MLExclusions struct {
	// Paths are file-path globs.
	Paths []string `json:"paths"`
	// Commands are terminal command prefixes.
	Commands []string `json:"commands"`
	// ExcludeBrowser drops browser page events. The harness ships none, so
	// the producer treats it as a no-op (it is decoded and displayed).
	ExcludeBrowser bool `json:"exclude_browser"`
}

// Exclusion list limits (contract "Typed exclusions": server-validated on
// PUT; re-checked here so an out-of-contract body fails closed).
const (
	MLExclusionsMaxEntries  = 50
	MLExclusionsMaxEntryLen = 256
)

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
	Exclusions                json.RawMessage `json:"exclusions"`
	ExclusionsVersion         *int            `json:"exclusions_version"`
	LegacyExclusionNotes      json.RawMessage `json:"legacy_exclusion_notes"`
	// Optional (PR #225): absent decodes as 0.
	NoticeTextRevision *int `json:"notice_text_revision"`
	AckedTextRevision  *int `json:"acked_text_revision"`
}

// mlExclusionsWire is the exclusions object, one pointer per key.
type mlExclusionsWire struct {
	Paths          *[]string `json:"paths"`
	Commands       *[]string `json:"commands"`
	ExcludeBrowser *bool     `json:"exclude_browser"`
}

// decodeMLExclusions is strict on purpose, stricter than the outer object:
// every known key required, null refused. An UNKNOWN key is a new kind of
// exclusion this build cannot honour. Rule agreed with Fleet (2026-10-09):
// Fleet adds new exclusion types only as lists or bools that default to
// empty, so an unknown key whose value is EMPTY (`[]` or `false`) excludes
// nothing and is accepted and ignored; an unknown key with any NON-empty
// value (a non-empty list, `true`, a string, an object, a number, null) is
// a decode error — silently ignoring a customer's exclusion would breach
// the DPA (SA v1.1 §4), so the read fails and the gate stays closed until
// the harness learns the new type.
func decodeMLExclusions(raw json.RawMessage) (MLExclusions, error) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || t[0] != '{' {
		return MLExclusions{}, fmt.Errorf("%w: exclusions is not an object", ErrMLDecode)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(t, &fields); err != nil {
		return MLExclusions{}, fmt.Errorf("%w: exclusions: %v", ErrMLDecode, err)
	}
	for k, v := range fields {
		switch k {
		case "paths", "commands", "exclude_browser":
			continue
		}
		if !emptyExclusionValue(v) {
			return MLExclusions{}, fmt.Errorf("%w: exclusions has an unknown non-empty key %q this build cannot honour", ErrMLDecode, k)
		}
		delete(fields, k)
	}
	known, err := json.Marshal(fields)
	if err != nil {
		return MLExclusions{}, fmt.Errorf("%w: exclusions: %v", ErrMLDecode, err)
	}
	dec := json.NewDecoder(bytes.NewReader(known))
	dec.DisallowUnknownFields()
	var w mlExclusionsWire
	if err := dec.Decode(&w); err != nil {
		return MLExclusions{}, fmt.Errorf("%w: exclusions: %v", ErrMLDecode, err)
	}
	switch {
	case w.Paths == nil || *w.Paths == nil:
		return MLExclusions{}, fmt.Errorf("%w: exclusions.paths missing or null", ErrMLDecode)
	case w.Commands == nil || *w.Commands == nil:
		return MLExclusions{}, fmt.Errorf("%w: exclusions.commands missing or null", ErrMLDecode)
	case w.ExcludeBrowser == nil:
		return MLExclusions{}, fmt.Errorf("%w: exclusions.exclude_browser missing", ErrMLDecode)
	}
	if err := checkExclusionEntries("paths", *w.Paths); err != nil {
		return MLExclusions{}, err
	}
	if err := checkExclusionEntries("commands", *w.Commands); err != nil {
		return MLExclusions{}, err
	}
	return MLExclusions{
		Paths:          append([]string{}, *w.Paths...),
		Commands:       append([]string{}, *w.Commands...),
		ExcludeBrowser: *w.ExcludeBrowser,
	}, nil
}

// emptyExclusionValue reports whether an unknown exclusions key carries an
// empty value: exactly `[]` (whitespace allowed) or `false`.
func emptyExclusionValue(v json.RawMessage) bool {
	t := bytes.TrimSpace(v)
	if string(t) == "false" {
		return true
	}
	if len(t) >= 2 && t[0] == '[' && t[len(t)-1] == ']' {
		return len(bytes.TrimSpace(t[1:len(t)-1])) == 0
	}
	return false
}

// checkExclusionEntries enforces the contract limits: ≤50 entries, each
// non-empty after trimming, ≤256 characters, valid UTF-8, no control
// characters.
func checkExclusionEntries(field string, entries []string) error {
	if len(entries) > MLExclusionsMaxEntries {
		return fmt.Errorf("%w: exclusions.%s has %d entries (max %d)", ErrMLDecode, field, len(entries), MLExclusionsMaxEntries)
	}
	for i, e := range entries {
		if strings.TrimSpace(e) == "" {
			return fmt.Errorf("%w: exclusions.%s[%d] is empty", ErrMLDecode, field, i)
		}
		if !utf8.ValidString(e) || utf8.RuneCountInString(e) > MLExclusionsMaxEntryLen {
			return fmt.Errorf("%w: exclusions.%s[%d] is not UTF-8 or longer than %d characters", ErrMLDecode, field, i, MLExclusionsMaxEntryLen)
		}
		for _, r := range e {
			if unicode.IsControl(r) {
				return fmt.Errorf("%w: exclusions.%s[%d] has a control character", ErrMLDecode, field, i)
			}
		}
	}
	return nil
}

// decodeLegacyNotes requires a JSON array of strings (null refused).
func decodeLegacyNotes(raw json.RawMessage) ([]string, error) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || t[0] != '[' {
		return nil, fmt.Errorf("%w: legacy_exclusion_notes is not an array", ErrMLDecode)
	}
	var notes []string
	if err := json.Unmarshal(t, &notes); err != nil {
		return nil, fmt.Errorf("%w: legacy_exclusion_notes: %v", ErrMLDecode, err)
	}
	if notes == nil {
		notes = []string{}
	}
	return notes, nil
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
	// The exclusion keys are REQUIRED, not optional (WP05): the contract
	// says they are "Always present (empty lists, not null, when nothing
	// is excluded)", and Fleet #220 (live on dev + prod) always sends
	// them. Reading an absent key as "no exclusions" would ship a
	// customer's excluded paths whenever a Fleet did not send them; a
	// missing key fails the read and the gate stays closed instead.
	case w.Exclusions == nil:
		return MeML{}, missing("exclusions")
	case w.ExclusionsVersion == nil:
		return MeML{}, missing("exclusions_version")
	case w.LegacyExclusionNotes == nil:
		return MeML{}, missing("legacy_exclusion_notes")
	}
	excl, err := decodeMLExclusions(w.Exclusions)
	if err != nil {
		return MeML{}, err
	}
	notes, err := decodeLegacyNotes(w.LegacyExclusionNotes)
	if err != nil {
		return MeML{}, err
	}
	if *w.ExclusionsVersion < 1 {
		return MeML{}, fmt.Errorf("%w: exclusions_version %d < 1", ErrMLDecode, *w.ExclusionsVersion)
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
		Exclusions:                excl,
		ExclusionsVersion:         *w.ExclusionsVersion,
		LegacyExclusionNotes:      notes,
	}
	if w.NoticeTextRevision != nil {
		if *w.NoticeTextRevision < 0 {
			return MeML{}, fmt.Errorf("%w: notice_text_revision %d < 0", ErrMLDecode, *w.NoticeTextRevision)
		}
		m.NoticeTextRevision = *w.NoticeTextRevision
	}
	if w.AckedTextRevision != nil {
		if *w.AckedTextRevision < 0 {
			return MeML{}, fmt.Errorf("%w: acked_text_revision %d < 0", ErrMLDecode, *w.AckedTextRevision)
		}
		m.AckedTextRevision = *w.AckedTextRevision
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

// AckMLNotice posts POST /api/v1/me/ml/notice-ack {notice_version,
// text_revision} — the version of the notice that was actually shown and
// the revision of the notice TEXT that was shown. text_revision is sent
// only when textRevision > 0, so an older Fleet that does not know it sees
// the original body; a Fleet that requires it (PR #225, dev rev 2) answers
// 409 policy_changed when it is omitted or mismatched. A stale version
// answers 409 policy_changed (errors.Is(err, ErrMLPolicyChanged)): re-read
// and show the notice again. On success Fleet returns the fresh /me/ml
// object.
func (c *Client) AckMLNotice(ctx context.Context, noticeVersion, textRevision int) (MeML, error) {
	if c == nil || c.isNop {
		return MeML{}, ErrFleetDisabled
	}
	if noticeVersion < 1 {
		return MeML{}, fmt.Errorf("fleet: ack ml notice: notice_version %d < 1", noticeVersion)
	}
	if textRevision < 0 {
		return MeML{}, fmt.Errorf("fleet: ack ml notice: text_revision %d < 0", textRevision)
	}
	body := map[string]int{"notice_version": noticeVersion}
	if textRevision > 0 {
		body["text_revision"] = textRevision
	}
	resp, err := c.PostJSON(ctx, "/api/v1/me/ml/notice-ack", body)
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
