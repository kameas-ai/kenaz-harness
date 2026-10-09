package settings

// Cloud ML consent panel (ml-producer-01MLPRD01 WP01, spec §5; kenaz-fleet
// docs/contract-harness-ml.md). Settings → Sync → "Cloud ML" reads
// GET /api/v1/me/ml through core/fleet/ml.go and shows: org offload,
// policy, the member's workflow_events opt-in, notice status, effective,
// retention, the required notice text (rendered here so it is pinned by a
// golden test), and the producer's shipping status.
//
// Shipping status is filled by the ML shipper (WP03) through
// SetMLShippingStatusProvider; until a provider is installed the view's
// Shipping is nil and the panel's status block stays empty — it never shows
// invented zeros.
//
// Fail-closed: any error reading /me/ml leaves every consent bool false and
// sets FleetError. Effective in this view is MeML.IsEffective() (effective
// AND a current ack), never the raw flag alone.

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

// mlNoticeTemplate is the required notice text from kenaz-fleet
// docs/contract-harness-ml.md "Required notice and acknowledgement",
// verbatim (the blockquote's line wraps joined with single spaces).
// {Org} / {org} = org display name, {retention} = retention_days.
const mlNoticeTemplate = "{Org} has turned on hosted inference. Kenaz will upload your activity on this device to " +
	"Fleet: files you open and edit (paths, not contents), terminal commands, window titles, " +
	"browser URLs, and summaries of your coding tasks (repo, branch, test and commit counts). " +
	"It is kept for {retention} days. Your organization controls this setting. If it is turned " +
	"off, uploads stop and your uploaded data is deleted within 72 hours."

// mlNoticeDeleteSentence is the sentence the retain_on_withdrawal variant
// replaces, and mlNoticeRetainSentence its replacement (contract, verbatim).
const (
	mlNoticeDeleteSentence = "If it is turned off, uploads stop and your uploaded data is deleted within 72 hours."
	mlNoticeRetainSentence = "If it is turned off, uploads stop. Your organization has instructed that data already " +
		"uploaded is kept until the {retention} days have passed."
)

// mlOrgFallback fills {org} when the cached identity has no org name.
const mlOrgFallback = "Your organization"

// RenderMLNotice fills the contract notice for display. retainOnWithdrawal
// selects the contract's retention variant of the last sentence.
func RenderMLNotice(orgName string, retentionDays int, retainOnWithdrawal bool) string {
	org := strings.TrimSpace(orgName)
	if org == "" {
		org = mlOrgFallback
	}
	text := mlNoticeTemplate
	if retainOnWithdrawal {
		text = strings.Replace(text, mlNoticeDeleteSentence, mlNoticeRetainSentence, 1)
	}
	return strings.NewReplacer(
		"{Org}", org,
		"{org}", org,
		"{retention}", strconv.Itoa(retentionDays),
	).Replace(text)
}

// MLShippingStatusView is the producer's shipping health (spec §5 "shipping
// status"). Filled by the WP03 shipper via SetMLShippingStatusProvider.
type MLShippingStatusView struct {
	// LastBatchAt is the RFC 3339 time of the last 2xx batch ("" = none yet).
	LastBatchAt string `json:"lastBatchAt"`
	Accepted    int64  `json:"accepted"`
	Duplicates  int64  `json:"duplicates"`
	Rejected    int64  `json:"rejected"`
	// StopReason is the last stop code ("" = running or never stopped), e.g.
	// ml_not_effective, unsupported_schema_version, signed_out, org_paused.
	StopReason string `json:"stopReason"`
}

// MLShippingStatusProvider is the seam the ML shipper (WP03) implements.
// MLShippingStatus must be cheap and non-blocking: it runs on every panel read.
type MLShippingStatusProvider interface {
	MLShippingStatus() MLShippingStatusView
}

// MLShippingStatusFunc adapts a plain func to MLShippingStatusProvider.
type MLShippingStatusFunc func() MLShippingStatusView

// MLShippingStatus implements MLShippingStatusProvider.
func (f MLShippingStatusFunc) MLShippingStatus() MLShippingStatusView { return f() }

type mlShippingHolder struct{ p MLShippingStatusProvider }

// SetMLShippingStatusProvider installs the shipper's status source (nil
// uninstalls). Owner of the production call: ml-producer-01MLPRD01 WP03
// (core/rpc/mlproducer_wiring.go).
func (a *API) SetMLShippingStatusProvider(p MLShippingStatusProvider) {
	if a == nil {
		return
	}
	if p == nil {
		a.mlShipping.Store(nil)
		return
	}
	a.mlShipping.Store(&mlShippingHolder{p: p})
}

func (a *API) mlShippingStatus() *MLShippingStatusView {
	h := a.mlShipping.Load()
	if h == nil || h.p == nil {
		return nil
	}
	s := h.p.MLShippingStatus()
	return &s
}

// MLStatusView is the wire shape of the Cloud ML settings panel.
type MLStatusView struct {
	// SignedIn: a live Fleet session exists. The panel hides when false.
	SignedIn bool `json:"signedIn"`
	// Entitled mirrors the hosted_inference capability. The panel hides
	// when false, and /me/ml is not read.
	Entitled bool `json:"entitled"`
	// OrgPaused: a staff org pause (forces effective=false server side).
	OrgPaused      bool   `json:"orgPaused"`
	PausedCategory string `json:"pausedCategory,omitempty"`
	// Loaded is true when /me/ml was read and decoded on this call.
	Loaded bool `json:"loaded"`

	OrgOffloadEnabled         bool   `json:"orgOffloadEnabled"`
	OrgPolicy                 string `json:"orgPolicy"`
	UserWorkflowEventsOptedIn bool   `json:"userWorkflowEventsOptedIn"`
	NoticeAckRequired         bool   `json:"noticeAckRequired"`
	// Effective is MeML.IsEffective(): effective AND a current ack.
	Effective     bool `json:"effective"`
	NoticeVersion int  `json:"noticeVersion"`
	// NoticeAckedAt is RFC 3339, "" while not acknowledged.
	NoticeAckedAt      string `json:"noticeAckedAt"`
	RetentionDays      int    `json:"retentionDays"`
	RetainOnWithdrawal bool   `json:"retainOnWithdrawal"`

	// OrgName is the org display name used for {org} in the notice.
	OrgName string `json:"orgName"`
	// NoticeText is the contract notice, rendered for this org, retention
	// and retain_on_withdrawal ("" until /me/ml is loaded).
	NoticeText string `json:"noticeText"`
	// NoticeChanged is set on the answer to an ack that Fleet refused with
	// 409 policy_changed: the state was re-read and the (new) notice must
	// be shown again.
	NoticeChanged bool `json:"noticeChanged,omitempty"`

	// FleetError is the last /me/ml read error ("" on success).
	FleetError string `json:"fleetError,omitempty"`

	// Shipping is the producer's status; nil until the shipper is wired.
	Shipping *MLShippingStatusView `json:"shipping,omitempty"`
}

// ErrMLNotWired is returned by the ML write paths when Fleet is not wired
// or the user is signed out.
var ErrMLNotWired = errors.New("settings: cloud ML is not available (fleet not wired or signed out)")

// mlFill copies a decoded MeML onto the view.
func mlFill(v *MLStatusView, m fleet.MeML) {
	v.Loaded = true
	v.OrgOffloadEnabled = m.OrgOffloadEnabled
	v.OrgPolicy = m.OrgPolicy
	v.UserWorkflowEventsOptedIn = m.UserWorkflowEventsOptedIn
	v.NoticeAckRequired = m.NoticeAckRequired
	v.Effective = m.IsEffective()
	v.NoticeVersion = m.NoticeVersion
	v.NoticeAckedAt = ""
	if m.NoticeAckedAt != nil {
		v.NoticeAckedAt = m.NoticeAckedAt.UTC().Format(time.RFC3339)
	}
	v.RetentionDays = m.RetentionDays
	v.RetainOnWithdrawal = m.RetainOnWithdrawal
	v.NoticeText = RenderMLNotice(v.OrgName, m.RetentionDays, m.RetainOnWithdrawal)
}

// mlBase builds the view's session/capability part and reports whether a
// /me/ml read may be made (signed in AND entitled).
func (a *API) mlBase(ctx context.Context) (MLStatusView, *fleet.Client, bool) {
	v := MLStatusView{Shipping: a.mlShippingStatus()}
	if fleet.Disabled() {
		return v, nil, false
	}
	c := a.fleetClient()
	if c == nil || c.IsNop() {
		return v, nil, false
	}
	if ok, err := c.SignedIn(ctx); err != nil || !ok {
		return v, nil, false
	}
	v.SignedIn = true
	if p := a.fleetPoller(); p != nil {
		caps := p.Current()
		v.Entitled = caps.Has(fleet.CapHostedInference)
		v.OrgPaused = caps.Paused
		if caps.Paused {
			v.PausedCategory = fleet.NormalizePausedCategory(caps.PausedCategory)
		}
	}
	if dir := a.fleetDataDir(); dir != "" {
		if id, err := fleet.LoadIdentity(dir); err == nil {
			v.OrgName = id.OrgName
		}
	}
	return v, c, v.Entitled
}

// FleetMLStatus implements SettingsAPI: a fresh /me/ml read (never cached —
// contract: re-read, do not cache across sessions).
func (a *API) FleetMLStatus(ctx context.Context) (MLStatusView, error) {
	v, c, ok := a.mlBase(ctx)
	if !ok {
		return v, nil
	}
	m, err := c.GetMeML(ctx)
	if err != nil {
		v.FleetError = err.Error()
		return v, nil
	}
	mlFill(&v, m)
	return v, nil
}

// FleetMLAckNotice implements SettingsAPI: acknowledges the notice version
// the panel showed. A 409 policy_changed is not an error to the caller: the
// state is re-read and returned with NoticeChanged set, so the panel shows
// the new notice again.
func (a *API) FleetMLAckNotice(ctx context.Context, noticeVersion int) (MLStatusView, error) {
	v, c, ok := a.mlBase(ctx)
	if !ok {
		return v, ErrMLNotWired
	}
	m, err := c.AckMLNotice(ctx, noticeVersion)
	if errors.Is(err, fleet.ErrMLPolicyChanged) {
		fresh, ferr := a.FleetMLStatus(ctx)
		fresh.NoticeChanged = true
		return fresh, ferr
	}
	if err != nil {
		return v, err
	}
	mlFill(&v, m)
	return v, nil
}

// FleetSetWorkflowEventsOptIn implements SettingsAPI: writes the member's
// own workflow_events choice, then re-reads /me/ml.
func (a *API) FleetSetWorkflowEventsOptIn(ctx context.Context, optedIn bool) (MLStatusView, error) {
	_, c, ok := a.mlBase(ctx)
	if !ok {
		return MLStatusView{}, ErrMLNotWired
	}
	if err := c.SetWorkflowEventsOptIn(ctx, optedIn); err != nil {
		return MLStatusView{}, err
	}
	return a.FleetMLStatus(ctx)
}
