package fleet

// notice_acks.go: member notice texts and acks (kenaz-fleet spec 017 T23,
// docs/contract-harness-sync.md §17).
//
// Fleet serves the exact words legal registered for a notice family on the
// kenaz-harness surface (GET /api/v1/me/notice-text) and keeps one ack store
// per org and member (GET|POST /api/v1/me/notice-acks). The harness shows the
// served text verbatim, checks its hash here, and sends the served edition and
// hash back unchanged. It never hashes a text of its own to invent a value.
//
// Until legal registers a text, every notice-text request answers 404
// notice_not_registered. That is the dormant state: nothing is shown, nothing
// is recorded, and nothing is held (§17.4 sequencing).
//
// The Agent Telemetry family is never posted here: its ack rides the opt-in
// PUT as notice_version "kenaz-harness/<edition>" (§17.2,
// HarnessTelemetryNoticeVersion).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
)

const (
	// NoticeSurfaceHarness is the surface token the harness's texts are
	// registered under (§17.3 "Surfaces").
	NoticeSurfaceHarness = "kenaz-harness"
	// NoticeFamilyMemoryAccess is the memory-access notice (D168(2)).
	NoticeFamilyMemoryAccess = "memory-access"
	// NoticeFamilyAgentTelemetry is the Agent Telemetry notice.
	NoticeFamilyAgentTelemetry = "agent-telemetry"

	// NoticeActionAck / NoticeActionWithdraw are the two ack-store actions.
	NoticeActionAck      = "ack"
	NoticeActionWithdraw = "withdraw"

	// MinMemoryAccessEdition is Fleet's legal.MinMemoryNotice (memory-access/v1).
	MinMemoryAccessEdition = 1

	// telemetryNoticeVersionPrefix is the harness's own notice_version form.
	telemetryNoticeVersionPrefix = "kenaz-harness/"
	// telemetryNoticeVersionMax is Fleet's notice_version length limit.
	telemetryNoticeVersionMax = 64
)

var (
	// ErrNoticeNotRegistered: Fleet has no text for this family on this
	// surface (404), or refused an ack tuple it does not know (422). The
	// notice is dormant: show nothing and record nothing.
	ErrNoticeNotRegistered = errors.New("fleet: notice not registered")

	// ErrNoticeTextMismatch: the served text's bytes do not hash to the
	// served text_hash, or the body names another family, surface or
	// edition shape. The text is refused, never shown.
	ErrNoticeTextMismatch = errors.New("fleet: served notice text does not match its hash")

	// ErrNoticeOrgInactive: an ack was refused because the org's
	// subscription has lapsed (403 subscription_inactive). A withdraw is
	// always accepted. A paused org answers OrgPausedError instead.
	ErrNoticeOrgInactive = errors.New("fleet: organization is not active")
)

// editionRE is §17.1's `<family>/v<N>[-suffix]`.
var editionRE = regexp.MustCompile(`^([a-z0-9-]{1,64})/v([0-9]{1,9})(-[A-Za-z0-9._-]+)?$`)

// ParseNoticeEdition splits an edition id into its family and number. A bare
// "vN" is never an edition.
func ParseNoticeEdition(edition string) (family string, n int, ok bool) {
	m := editionRE.FindStringSubmatch(edition)
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, false
	}
	return m[1], n, true
}

// NoticeEditionMeets reports whether edition is in family at or above min
// (numeric on N, a suffixed variant satisfies the same N).
func NoticeEditionMeets(edition, family string, min int) bool {
	f, n, ok := ParseNoticeEdition(edition)
	return ok && f == family && n >= min
}

// NoticeText is one registered text as Fleet served it.
type NoticeText struct {
	Family   string `json:"family"`
	Edition  string `json:"edition"`
	Surface  string `json:"surface"`
	Text     string `json:"text"`
	TextHash string `json:"text_hash"`
}

// NoticeTextHash is the lowercase hex SHA-256 of text's UTF-8 bytes.
func NoticeTextHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// Verify checks a served text before it is shown: the family and surface are
// the ones asked for, the edition is in that family, and the text's bytes hash
// to text_hash exactly. Any failure is ErrNoticeTextMismatch.
func (nt NoticeText) Verify(family string) error {
	switch {
	case nt.Family != family:
		return fmt.Errorf("%w: family %q, asked for %q", ErrNoticeTextMismatch, nt.Family, family)
	case nt.Surface != NoticeSurfaceHarness:
		return fmt.Errorf("%w: surface %q", ErrNoticeTextMismatch, nt.Surface)
	case !NoticeEditionMeets(nt.Edition, family, 1):
		return fmt.Errorf("%w: edition %q is not a %s edition", ErrNoticeTextMismatch, nt.Edition, family)
	case nt.Text == "":
		return fmt.Errorf("%w: empty text", ErrNoticeTextMismatch)
	case nt.TextHash != NoticeTextHash(nt.Text):
		return fmt.Errorf("%w: %s", ErrNoticeTextMismatch, nt.Edition)
	}
	return nil
}

// GetNoticeText fetches the registered text for family on the kenaz-harness
// surface. edition "" asks for the latest. The text is verified before it is
// returned. 404 is ErrNoticeNotRegistered: show nothing.
func (c *Client) GetNoticeText(ctx context.Context, family, edition string) (NoticeText, error) {
	if c == nil || c.isNop {
		return NoticeText{}, ErrFleetDisabled
	}
	q := url.Values{}
	q.Set("family", family)
	q.Set("surface", NoticeSurfaceHarness)
	if edition != "" {
		q.Set("edition", edition)
	}
	resp, err := c.Get(ctx, "/api/v1/me/notice-text?"+q.Encode())
	if err != nil {
		return NoticeText{}, fmt.Errorf("fleet: get notice text: %w", err)
	}
	defer drainClose(resp)
	if resp.StatusCode == http.StatusNotFound {
		// notice_not_registered, or a Fleet that predates the route: both
		// mean no text is served, so the notice stays dormant.
		return NoticeText{}, ErrNoticeNotRegistered
	}
	if resp.StatusCode != http.StatusOK {
		return NoticeText{}, noticeStatusError("get notice text", resp)
	}
	var nt NoticeText
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&nt); err != nil {
		return NoticeText{}, fmt.Errorf("fleet: get notice text: decode: %w", err)
	}
	if err := nt.Verify(family); err != nil {
		return NoticeText{}, err
	}
	if edition != "" && nt.Edition != edition {
		return NoticeText{}, fmt.Errorf("%w: asked for %s, served %s", ErrNoticeTextMismatch, edition, nt.Edition)
	}
	return nt, nil
}

// NoticeAckState is one family's latest row in the caller's current org.
type NoticeAckState struct {
	Family   string `json:"family"`
	Edition  string `json:"edition"`
	TextHash string `json:"text_hash"`
	Surface  string `json:"surface"`
	Action   string `json:"action"`
	// Acked: the member stands acked at or above the family's minimum.
	Acked   bool   `json:"acked"`
	ActedAt string `json:"acted_at"`
	// AckedSince is null when not acked.
	AckedSince *string `json:"acked_since"`
}

// GetNoticeAcks returns the caller's latest row per family in their current
// org. A family with no row is absent.
func (c *Client) GetNoticeAcks(ctx context.Context) ([]NoticeAckState, error) {
	if c == nil || c.isNop {
		return nil, ErrFleetDisabled
	}
	resp, err := c.Get(ctx, "/api/v1/me/notice-acks")
	if err != nil {
		return nil, fmt.Errorf("fleet: get notice acks: %w", err)
	}
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		return nil, noticeStatusError("get notice acks", resp)
	}
	var out struct {
		Families []NoticeAckState `json:"families"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("fleet: get notice acks: decode: %w", err)
	}
	return out.Families, nil
}

// FindNoticeAck returns family's row, if any.
func FindNoticeAck(rows []NoticeAckState, family string) (NoticeAckState, bool) {
	for _, r := range rows {
		if r.Family == family {
			return r, true
		}
	}
	return NoticeAckState{}, false
}

// MemoryNoticeSeen is §17.4's test: a memory-access row at or above the
// minimum, with either action. A withdraw has seen the notice too.
func MemoryNoticeSeen(rows []NoticeAckState) bool {
	r, ok := FindNoticeAck(rows, NoticeFamilyMemoryAccess)
	if !ok || !NoticeEditionMeets(r.Edition, NoticeFamilyMemoryAccess, MinMemoryAccessEdition) {
		return false
	}
	return r.Action == NoticeActionAck || r.Action == NoticeActionWithdraw
}

// NoticeAckResult is POST /me/notice-acks's answer.
type NoticeAckResult struct {
	Family  string `json:"family"`
	Edition string `json:"edition"`
	Action  string `json:"action"`
	ActedAt string `json:"acted_at"`
}

// PostNoticeAck records action ("ack" or "withdraw") for a text Fleet served,
// echoing its family, edition and hash unchanged. Errors: OrgPausedError
// (ack while paused), ErrNoticeOrgInactive (ack while lapsed),
// ErrNoticeNotRegistered (422: the tuple isn't in the registry).
func (c *Client) PostNoticeAck(ctx context.Context, nt NoticeText, action string) (NoticeAckResult, error) {
	if c == nil || c.isNop {
		return NoticeAckResult{}, ErrFleetDisabled
	}
	if action != NoticeActionAck && action != NoticeActionWithdraw {
		return NoticeAckResult{}, fmt.Errorf("fleet: notice ack: action %q is not ack or withdraw", action)
	}
	if nt.Family == NoticeFamilyAgentTelemetry {
		// §17.2: the telemetry family acks through the opt-in PUT only.
		return NoticeAckResult{}, errors.New("fleet: notice ack: agent-telemetry is acked through the opt-in endpoint")
	}
	if err := nt.Verify(nt.Family); err != nil {
		return NoticeAckResult{}, err
	}
	body := map[string]string{
		"family":    nt.Family,
		"edition":   nt.Edition,
		"text_hash": nt.TextHash,
		"surface":   NoticeSurfaceHarness,
		"action":    action,
	}
	resp, err := c.PostJSON(ctx, "/api/v1/me/notice-acks", body)
	if err != nil {
		if IsOrgPaused(err) {
			return NoticeAckResult{}, err
		}
		return NoticeAckResult{}, fmt.Errorf("fleet: notice ack: %w", err)
	}
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return NoticeAckResult{}, noticeStatusError("notice ack", resp)
	}
	var out NoticeAckResult
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return NoticeAckResult{}, fmt.Errorf("fleet: notice ack: decode: %w", err)
	}
	return out, nil
}

// HarnessTelemetryNoticeVersion is the notice_version the harness sends on the
// opt-in PUT for an Agent Telemetry edition Fleet served it (§17.2):
// "kenaz-harness/<edition>". Never the fleet-dashboard/ or +p<N> forms.
func HarnessTelemetryNoticeVersion(edition string) (string, error) {
	if !NoticeEditionMeets(edition, NoticeFamilyAgentTelemetry, 1) {
		return "", fmt.Errorf("fleet: %q is not an agent-telemetry edition", edition)
	}
	v := telemetryNoticeVersionPrefix + edition
	if len(v) > telemetryNoticeVersionMax {
		return "", fmt.Errorf("fleet: notice_version %q is longer than %d", v, telemetryNoticeVersionMax)
	}
	return v, nil
}

// ResolveHarnessTelemetryNotice re-fetches the Agent Telemetry edition the
// member was shown and returns its notice_version, only while Fleet still
// serves exactly that edition on the harness surface with a verified hash.
func (c *Client) ResolveHarnessTelemetryNotice(ctx context.Context, edition string) (string, error) {
	v, err := HarnessTelemetryNoticeVersion(edition)
	if err != nil {
		return "", err
	}
	if _, err := c.GetNoticeText(ctx, NoticeFamilyAgentTelemetry, edition); err != nil {
		return "", err
	}
	return v, nil
}

// noticeStatusError classifies a non-2xx notice answer.
func noticeStatusError(op string, resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	env, _ := parseFleetError(raw)
	switch {
	case resp.StatusCode == http.StatusUnprocessableEntity && env.Code == "notice_not_registered":
		return fmt.Errorf("fleet: %s: %w", op, ErrNoticeNotRegistered)
	case resp.StatusCode == http.StatusForbidden && env.Code == "subscription_inactive":
		return fmt.Errorf("fleet: %s: %w", op, ErrNoticeOrgInactive)
	case resp.StatusCode == http.StatusForbidden:
		if pe := ParseOrgPaused(resp.StatusCode, raw); pe != nil {
			return pe
		}
	}
	return fmt.Errorf("fleet: %s: status %d %s", op, resp.StatusCode, env.Code)
}
