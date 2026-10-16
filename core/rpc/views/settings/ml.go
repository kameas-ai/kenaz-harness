package settings

// Cloud ML consent panel (ml-producer-01MLPRD01 WP01, spec §5; kenaz-fleet
// docs/contract-harness-ml.md). Settings → Sync → "Cloud ML" reads
// GET /api/v1/me/ml through core/fleet/ml.go and shows: org offload,
// policy, the member's workflow_events opt-in, notice status, effective,
// retention, the required notice text (rendered here so it is pinned by a
// golden test), and the producer's shipping status.
//
// Notice source (WP06): when Fleet's pending-approvals hub lists an
// ml_notice, its server-rendered body_text replaces the local template and
// the panel approves through the hub (approvals.go). The local template and
// the dashboard routing remain only as the fallback for a Fleet without the
// hub (404).
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
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

// The fallback notice: counsel's approved rev-1 text (counsel doc 08 §3,
// confirmed by Fleet as notice text revision 1), VERBATIM, split into the
// structured fields the panel lays out. It is shown ONLY when Fleet's
// approvals hub is unavailable and the required text revision is <=
// localNoticeTextRevision; with the hub, its body_text wins.
// Placeholders: {Organization name} = the org display name (mlOrgFallback
// when unknown), {retention period} = retention_days. The text covers
// withdrawal retention itself ("How long it is kept"), so there is no
// retain_on_withdrawal variant.
const (
	mlNoticeHeading = "Your organization has enabled workflow data offload"
	mlNoticeIntro   = "{Organization name} has turned on Offload for your account. Here is exactly what that means."

	// mlNoticeMemberChoice is the {If Member Choice:} paragraph
	// (org_policy member_choice), mlNoticeRequired the {If Required:} one
	// (org_policy on).
	mlNoticeMemberChoice = "You decide. Offload is off for you until you turn it on below. You can turn it off " +
		"at any time in Settings → Offload."
	mlNoticeRequired = "Your organization has made Offload required for all members. If you have questions " +
		"about this, contact your organization's administrator."
)

// mlNoticeSections are the bold-labelled paragraphs, in order; Label is
// shown as written (with its colon), Text follows it.
var mlNoticeSections = []MLNoticeSection{
	{Label: "What leaves your machine:", Text: "a record of your workflow activity: file paths, the shell commands " +
		"you run, git branch and repository names, which application or window is active, the domains of sites " +
		"you visit, your device's power state, and timestamps; plus task summaries (start and end times, phase, " +
		"test and commit counts) computed on your machine."},
	{Label: "What never leaves your machine:", Text: "the contents of your files, your source code, your prompts " +
		"or AI conversations, screenshots, and keystrokes. Anything that looks like a credential is redacted " +
		"before sending."},
	{Label: "Where it goes:", Text: "Kameas's cloud in Ohio, USA (AWS us-east-2), in a database area dedicated to " +
		"your organization. Kameas processes it only on your organization's instructions."},
	{Label: "Who sees what:", Text: "predictions about your work come back only to your own tools. Your " +
		"organization's administrators do not see your individual predictions. Kameas engineers have no " +
		"standing access; emergency access is logged."},
	{Label: "How long it is kept:", Text: "workflow events and task summaries for {retention period} days, then " +
		"deleted. Predictions and your organization's models for the subscription. If you leave the " +
		"organization, sending stops immediately and your stored data is deleted within 72 hours unless your " +
		"organization has chosen to keep it for the rest of the retention period. Backups expire within 30 days."},
	{Label: "Your rights:", Text: "your organization is responsible for this data. To access, correct, export or " +
		"delete it, contact your organization's administrator. You can also read Kameas's Privacy Policy, " +
		"section 13."},
}

// Notice action buttons (the text's "Buttons:" line, verbatim).
const (
	// MLNoticeActionTurnOn: set the workflow_events opt-in AND acknowledge
	// the notice (member_choice).
	MLNoticeActionTurnOn = "Turn on Offload"
	// MLNoticeActionNotNow: close the notice; no request (member_choice).
	MLNoticeActionNotNow = "Not now"
	// MLNoticeActionRead: acknowledge the notice (required: policy on).
	MLNoticeActionRead = "I have read this"
)

// MLNoticeSection is one bold-labelled paragraph of the notice.
type MLNoticeSection struct {
	// Label is the bold label, as written (e.g. "Where it goes:").
	Label string `json:"label"`
	// Text is the rest of the paragraph.
	Text string `json:"text"`
}

// MLNoticeView is the fallback notice, rendered for one org, retention
// and policy, as structured fields (the panel lays them out; the words
// are counsel's, verbatim).
type MLNoticeView struct {
	Heading  string            `json:"heading"`
	Intro    string            `json:"intro"`
	Sections []MLNoticeSection `json:"sections"`
	// PolicyText is the {If Member Choice:} paragraph (policy
	// member_choice) or the {If Required:} one (policy on); "" otherwise.
	PolicyText string `json:"policyText"`
	// Policy is the org_policy the notice was rendered for.
	Policy string `json:"policy"`
	// Actions are the button labels for that policy, in order:
	// member_choice → [Turn on Offload, Not now], or [I have read this]
	// when re-acknowledging (Offload already on); on → [I have read this];
	// none otherwise.
	Actions []string `json:"actions"`
}

// localNoticeTextRevision is the Fleet notice-text revision of the
// fallback notice above — the only notice text this harness can render. It
// changes ONLY when the template text above changes (in lockstep with the
// kenaz-fleet text revision that text corresponds to); never bump it for
// any other reason. The harness acknowledges this revision and nothing
// newer: when Fleet requires a higher notice_text_revision (kenaz-fleet
// PR #225), the user has not seen that text here, so the panel routes them
// to the Fleet dashboard instead of acknowledging it.
const localNoticeTextRevision = 1

// localExclusionsTemplate is the version of Fleet's exclusions paragraph
// template whose legacy-notes sentence this harness mirrors
// (CloudMLExclusions.vue LEGACY_NOTES_LEAD): template 2, kenaz-fleet PR #289
// (c9b08ca2, legal kameas-ai-11, 2026-10-16). The fallback ack sends it as
// "exclusions_template"; Fleet logs a mismatch, never refuses. Bump it
// whenever the mirrored exclusions sentence changes — NOT
// localNoticeTextRevision, which the exclusions paragraph never moves.
const localExclusionsTemplate = 2

// mlDashboardConsentPath is the Fleet dashboard's hosted-inference consent
// card, relative to the active profile's FleetBaseURL (stable per Fleet).
const mlDashboardConsentPath = "/settings#hosted-inference"

// mlDashboardConsentURL builds the consent-card link from the active fleet
// profile's base URL; "" when the base is unknown or not an http(s) URL.
func mlDashboardConsentURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	u, err := url.Parse(base)
	if base == "" || err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	return base + mlDashboardConsentPath
}

// mlOrgFallback fills {org} when the cached identity has no org name.
const mlOrgFallback = "Your organization"

// RenderMLNotice fills the fallback notice for display: the org name, the
// retention period and the policy block (member_choice or on).
//
// Buttons are legal's fixed labels (kameas-ai-11, 2026-10-16): a required
// policy (on), or re-acknowledging an earlier notice (reack: member_choice
// with Offload already turned on), reads "I have read this"; a first
// member_choice notice reads "Turn on Offload" / "Not now". Never "I
// agree", "Accept" or "Consent".
func RenderMLNotice(orgName string, retentionDays int, policy string, reack bool) MLNoticeView {
	org := strings.TrimSpace(orgName)
	if org == "" {
		org = mlOrgFallback
	}
	fill := strings.NewReplacer(
		"{Organization name}", org,
		"{retention period}", strconv.Itoa(retentionDays),
	).Replace
	n := MLNoticeView{
		Heading:  fill(mlNoticeHeading),
		Intro:    fill(mlNoticeIntro),
		Sections: make([]MLNoticeSection, len(mlNoticeSections)),
		Policy:   policy,
		Actions:  []string{},
	}
	for i, s := range mlNoticeSections {
		n.Sections[i] = MLNoticeSection{Label: s.Label, Text: fill(s.Text)}
	}
	switch policy {
	case fleet.MLPolicyMemberChoice:
		n.PolicyText = mlNoticeMemberChoice
		n.Actions = []string{MLNoticeActionTurnOn, MLNoticeActionNotNow}
		if reack {
			n.Actions = []string{MLNoticeActionRead}
		}
	case fleet.MLPolicyOn:
		n.PolicyText = mlNoticeRequired
		n.Actions = []string{MLNoticeActionRead}
	}
	return n
}

// PlainText joins the notice into paragraphs separated by a blank line:
// heading, intro, each "Label Text", then the policy paragraph.
func (n MLNoticeView) PlainText() string {
	parts := []string{n.Heading, n.Intro}
	for _, s := range n.Sections {
		parts = append(parts, s.Label+" "+s.Text)
	}
	if n.PolicyText != "" {
		parts = append(parts, n.PolicyText)
	}
	return strings.Join(parts, "\n\n")
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

// FleetEnrolledIdentity is the identity of this session's last successful
// enroll: Fleet's org id (the enroll response's org_id — the same value as
// identity.json's, NOT the token's Zitadel resource-owner claim) and the
// node id enroll registered (fleet.NodeID, which Fleet stores as
// node_registrations.node_id). enrolled is false before the first enroll
// and after sign-out / node_removed. In-memory only: no keychain, no
// network — the ML producer's consent gate reads it first so a signed-out
// or not-yet-enrolled install never touches the keychain on the gate path
// (ml-producer-01MLPRD01 WP03).
func (a *API) FleetEnrolledIdentity() (orgID, nodeID string, enrolled bool) {
	if a == nil || a.fleet == nil {
		return "", "", false
	}
	a.fleet.mu.RLock()
	defer a.fleet.mu.RUnlock()
	return a.fleet.enrolledOrgID, a.fleet.enrolledNodeID, a.fleet.enrolled
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
	// NoticeText is the notice as plain text: the hub's body_text when
	// NoticeFromHub, otherwise the fallback Notice joined into paragraphs
	// (MLNoticeView.PlainText); "" until /me/ml is loaded.
	NoticeText string `json:"noticeText"`
	// Notice is the fallback notice (counsel's rev-1 text) as structured
	// fields for the panel to lay out, rendered for this org, retention and
	// policy. nil when the hub's notice is shown or NoticeNeedsDashboard.
	Notice *MLNoticeView `json:"notice,omitempty"`
	// NoticeTitle is the hub ml_notice item's title, shown as the heading
	// above its verbatim body_text (Fleet's rev-2 notice keeps the heading
	// out of body_text). "" unless NoticeFromHub.
	NoticeTitle string `json:"noticeTitle,omitempty"`
	// NoticeChanged is set on the answer to an ack that Fleet refused with
	// 409 policy_changed: the state was re-read and the (new) notice must
	// be shown again.
	NoticeChanged bool `json:"noticeChanged,omitempty"`
	// NoticeTextRevision is the notice-text revision Fleet requires (0 =
	// an older Fleet that does not send it); AckedTextRevision is the one
	// this member last acknowledged.
	NoticeTextRevision int `json:"noticeTextRevision"`
	AckedTextRevision  int `json:"ackedTextRevision"`
	// NoticeNeedsDashboard: an ack is required for a notice text NEWER than
	// the one this harness renders (localNoticeTextRevision). NoticeText is
	// then left empty and the panel offers no Acknowledge button — the user
	// must review and approve the new text on the Fleet dashboard.
	NoticeNeedsDashboard bool `json:"noticeNeedsDashboard"`
	// NoticeDashboardURL is the dashboard consent card
	// (<fleet base>/settings#hosted-inference), set only while
	// NoticeNeedsDashboard and the active profile's base URL is known.
	NoticeDashboardURL string `json:"noticeDashboardUrl,omitempty"`
	// NoticeFromHub: NoticeText is the body_text of the pending-approvals
	// hub's ml_notice item (WP06), verbatim, including any "Changed since you
	// last approved" section. The panel then approves NoticeItemID through
	// Fleet_ApproveItem, so the text acknowledged is exactly the text shown.
	// False when the hub is unavailable (an older Fleet): NoticeText is the
	// local template (or NoticeNeedsDashboard routes to the dashboard).
	NoticeFromHub bool   `json:"noticeFromHub"`
	NoticeItemID  string `json:"noticeItemId,omitempty"`

	// Org exclusions (WP05; contract "Typed exclusions"), shown read-only:
	// "Your organization excludes: …". The producer matches the paths and
	// commands on this device before anything is hashed or queued.
	// Always non-nil lists ([] on the wire) so the panel never branches
	// on null.
	ExclusionPaths    []string `json:"exclusionPaths"`
	ExclusionCommands []string `json:"exclusionCommands"`
	// ExcludeBrowser is shown for completeness; the harness sends no
	// browser data, so it changes nothing here.
	ExcludeBrowser    bool `json:"excludeBrowser"`
	ExclusionsVersion int  `json:"exclusionsVersion"`
	// LegacyExclusionNotes are the org's old free-text exclusions: display
	// only, never matched.
	LegacyExclusionNotes []string `json:"legacyExclusionNotes"`

	// FleetError is the last /me/ml read error ("" on success).
	FleetError string `json:"fleetError,omitempty"`

	// Shipping is the producer's status; nil until the shipper is wired.
	Shipping *MLShippingStatusView `json:"shipping,omitempty"`
}

// ErrMLNotWired is returned by the ML write paths when Fleet is not wired
// or the user is signed out.
var ErrMLNotWired = errors.New("settings: cloud ML is not available (fleet not wired or signed out)")

// mlFill copies a decoded MeML onto the view. fleetBaseURL is the active
// fleet profile's base URL (for the dashboard link; may be "").
func mlFill(v *MLStatusView, m fleet.MeML, fleetBaseURL string) {
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
	v.NoticeTextRevision = m.NoticeTextRevision
	v.AckedTextRevision = m.AckedTextRevision
	v.NoticeNeedsDashboard = m.NoticeAckRequired && m.NoticeTextRevision > localNoticeTextRevision
	v.NoticeText = ""
	v.Notice = nil
	v.NoticeTitle = ""
	v.NoticeDashboardURL = ""
	if v.NoticeNeedsDashboard {
		// Never show the local (older) text for a newer required revision.
		v.NoticeDashboardURL = mlDashboardConsentURL(fleetBaseURL)
	} else {
		n := RenderMLNotice(v.OrgName, m.RetentionDays, m.OrgPolicy, mlNoticeReack(m))
		v.Notice = &n
		v.NoticeText = n.PlainText()
	}
	v.ExclusionPaths = append([]string{}, m.Exclusions.Paths...)
	v.ExclusionCommands = append([]string{}, m.Exclusions.Commands...)
	v.ExcludeBrowser = m.Exclusions.ExcludeBrowser
	v.ExclusionsVersion = m.ExclusionsVersion
	v.LegacyExclusionNotes = append([]string{}, m.LegacyExclusionNotes...)
}

// mlNoticeReack reports whether a member_choice notice is a
// re-acknowledgement: Offload is already on for the member, so the notice
// asks them to read the (changed) text, not to turn Offload on.
func mlNoticeReack(m fleet.MeML) bool {
	return m.OrgPolicy == fleet.MLPolicyMemberChoice && m.UserWorkflowEventsOptedIn
}

// mlBase builds the view's session/capability part and reports whether a
// /me/ml read may be made (signed in AND entitled).
func (a *API) mlBase(ctx context.Context) (MLStatusView, *fleet.Client, bool) {
	v := MLStatusView{
		Shipping:          a.mlShippingStatus(),
		ExclusionPaths:    []string{},
		ExclusionCommands: []string{},
		// legacy notes too: the wire shape never carries null lists.
		LegacyExclusionNotes: []string{},
	}
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
	mlFill(&v, m, c.Profile().FleetBaseURL)
	if m.NoticeAckRequired {
		if item, ok := hubMLNotice(ctx, c); ok {
			applyHubNotice(&v, item)
		}
	}
	return v, nil
}

// applyHubNotice replaces the locally rendered notice (or the dashboard
// routing) with the hub's ml_notice item (WP06): Fleet renders the text,
// including the per-member "Changed since you last approved" section, and
// the harness shows it verbatim and approves it through the hub. Without a
// hub item (an older Fleet: 404) the view keeps the pre-hub fallback: the
// local template at localNoticeTextRevision, or the dashboard link when Fleet
// requires a newer revision.
func applyHubNotice(v *MLStatusView, item PendingApprovalView) {
	v.NoticeText = item.BodyText
	// Fleet's rev-2 ml_notice carries its heading in the item title, not in
	// body_text (which opens with a "What's new" panel).
	v.NoticeTitle = item.Title
	v.Notice = nil
	v.NoticeFromHub = true
	v.NoticeItemID = item.ID
	v.NoticeNeedsDashboard = false
	v.NoticeDashboardURL = ""
}

// FleetMLAckNotice implements SettingsAPI: acknowledges the notice version
// the panel showed, through the notice button the member clicked (button:
// MLNoticeActionTurnOn or MLNoticeActionRead; it picks the ack action), with the text revision this harness rendered
// (localNoticeTextRevision). It first re-reads /me/ml: when Fleet requires
// a NEWER notice text (NoticeNeedsDashboard) nothing is posted — that text
// was never shown here — and the view is returned for the panel to route
// the user to the dashboard. text_revision is sent only when Fleet reports
// one (NoticeTextRevision > 0), so an older Fleet sees the original body.
// A 409 policy_changed is not an error to the caller: the state is re-read
// and returned with NoticeChanged set, so the panel shows the new notice
// again.
func (a *API) FleetMLAckNotice(ctx context.Context, noticeVersion int, button string) (MLStatusView, error) {
	v, c, ok := a.mlBase(ctx)
	if !ok {
		return v, ErrMLNotWired
	}
	cur, err := c.GetMeML(ctx)
	if err != nil {
		return v, err
	}
	base := v
	mlFill(&base, cur, c.Profile().FleetBaseURL)
	if base.NoticeNeedsDashboard {
		return base, nil
	}
	textRevision := 0
	if cur.NoticeTextRevision > 0 {
		textRevision = localNoticeTextRevision
	}
	action, err := mlNoticeAckAction(cur.OrgPolicy, button)
	if err != nil {
		return base, err
	}
	// ai_usage_epoch from the same fresh read, echoed exactly as served
	// whenever the notice includes the AI-usage section (include_ai_usage);
	// a stale one is a 409, handled below. The epoch never turns the action
	// into include_ai_usage — that is the AI-usage add-on control's alone.
	// exclusions_template: the panel rendered the exclusions from this
	// harness's own copy of Fleet's template (localExclusionsTemplate).
	m, err := c.AckMLNotice(ctx, action, noticeVersion, textRevision, cur.AckAIUsageEpoch(), localExclusionsTemplate)
	if errors.Is(err, fleet.ErrMLPolicyChanged) {
		fresh, ferr := a.FleetMLStatus(ctx)
		fresh.NoticeChanged = true
		return fresh, ferr
	}
	if err != nil {
		return v, err
	}
	mlFill(&v, m, c.Profile().FleetBaseURL)
	return v, nil
}

// mlNoticeAckAction maps the org_policy the notice was shown for, and the
// notice button the member clicked, to the explicit notice-ack action
// (legal, kameas-ai-11, 2026-10-16): member_choice "Turn on Offload" →
// turn_on; on (required) "I have read this" → read; member_choice "I have
// read this" (a re-acknowledgement, Offload already on) → read. Any other
// pairing — a policy with no notice button, or a button that policy never
// shows — is refused and nothing is acknowledged.
func mlNoticeAckAction(policy, button string) (string, error) {
	switch {
	case policy == fleet.MLPolicyMemberChoice && button == MLNoticeActionTurnOn:
		return fleet.MLNoticeAckActionTurnOn, nil
	case (policy == fleet.MLPolicyOn || policy == fleet.MLPolicyMemberChoice) && button == MLNoticeActionRead:
		return fleet.MLNoticeAckActionRead, nil
	}
	return "", fmt.Errorf("settings: ml notice: no acknowledgement action for org_policy %q and button %q", policy, button)
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
