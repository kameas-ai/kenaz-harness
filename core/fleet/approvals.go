package fleet

// approvals.go — the harness side of Fleet's pending-approvals hub
// (ml-producer-01MLPRD01 WP06; kenaz-fleet docs/contract-pending-approvals.md,
// spec §12 A-13).
//
// GET /api/v1/me/pending-approvals lists everything the signed-in member must
// agree to (legal re-acceptance, the hosted-inference notice, informational
// exclusion changes, ...). Each item carries the request that approves it.
// Two rules make that safe to act on:
//
//   - The decode is strict on the contract's keys (a missing or mistyped key
//     fails the whole read) and tolerant of extra keys and unknown kinds:
//     Fleet adds kinds and fields additively, and an unknown kind is kept so
//     the UI can render it generically.
//   - The approve action is sent ONLY when its method + path is exactly in
//     approveAllowlist (ApprovePendingItem below is the single enforcement
//     point). Anything else is refused before any request is made, and the
//     refusal is logged without the body. Fleet chooses the body; the harness
//     never lets Fleet choose where an authenticated POST goes.

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// Pending-approval kinds the harness renders specifically. Any other kind is
// kept and rendered generically (contract: "render unknown kinds generically").
const (
	ApprovalKindLegalAcceptance    = "legal_acceptance"
	ApprovalKindMLNotice           = "ml_notice"
	ApprovalKindMLExclusionsChange = "ml_exclusions_change"
)

// Approve paths the harness will POST to (contract "Client behaviour" 3;
// tasks.md WP06 allowlist). Exact match on method AND path: no prefixes, no
// query strings, no normalisation.
const (
	ApprovePathLegalAcceptances = "/api/v1/me/legal-acceptances"
	ApprovePathMLNoticeAck      = "/api/v1/me/ml/notice-ack"
	ApprovePathMLExclusionsSeen = "/api/v1/me/ml/exclusions-seen"
)

// approveAllowlist is method + " " + path for every approve action the harness
// may send.
var approveAllowlist = map[string]struct{}{
	http.MethodPost + " " + ApprovePathLegalAcceptances: {},
	http.MethodPost + " " + ApprovePathMLNoticeAck:      {},
	http.MethodPost + " " + ApprovePathMLExclusionsSeen: {},
}

// ApproveActionAllowed reports whether an approve action's method + path is
// exactly in the allowlist.
func ApproveActionAllowed(method, path string) bool {
	_, ok := approveAllowlist[method+" "+path]
	return ok
}

// Fleet error codes on the approve endpoints that mean "what you were shown
// is stale; refetch and show the updated item" (contract "Client behaviour" 4).
const (
	ApprovalCodeLegalAcceptanceOutdated = "legal_acceptance_outdated"
)

// ErrApproveActionNotAllowed is returned (and nothing is sent) when an
// item's approve action is not in the allowlist.
var ErrApproveActionNotAllowed = errors.New("fleet: pending approval action is not in the harness allowlist; nothing was sent")

// ErrPendingApprovalsUnavailable means this Fleet does not serve the hub
// (404: an older Fleet). Callers fall back to their pre-hub flows.
var ErrPendingApprovalsUnavailable = errors.New("fleet: pending-approvals hub is not available on this Fleet (404)")

// ErrPendingApprovalsDecode wraps every strict-decode refusal of a hub body.
var ErrPendingApprovalsDecode = errors.New("fleet: pending-approvals response did not match the contract")

// ErrApprovalStale is matched (errors.Is) by an *ApprovalError for 409
// policy_changed or 400 legal_acceptance_outdated: refetch and show again.
var ErrApprovalStale = errors.New("fleet: the item changed since it was shown")

// ApprovalError is a non-2xx answer from an approve endpoint.
type ApprovalError struct {
	Status int
	Code   string
}

func (e *ApprovalError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("fleet: approve: HTTP %d", e.Status)
	}
	return fmt.Sprintf("fleet: approve: HTTP %d %s", e.Status, e.Code)
}

// Is lets errors.Is match ErrApprovalStale.
func (e *ApprovalError) Is(target error) bool {
	if target != ErrApprovalStale {
		return false
	}
	return (e.Status == http.StatusConflict && e.Code == MLCodePolicyChanged) ||
		(e.Status == http.StatusBadRequest && e.Code == ApprovalCodeLegalAcceptanceOutdated)
}

// ApproveAction is the request that approves an item, as Fleet sent it.
type ApproveAction struct {
	Method string
	Path   string
	// Body is the JSON object to send, byte-for-byte as received.
	Body json.RawMessage
}

// PendingApproval is one hub item. Exactly the contract's fields.
type PendingApproval struct {
	ID      string
	Kind    string
	Title   string
	Summary string
	// BodyText is plain text to render as text (never HTML); "" when absent.
	BodyText string
	// DocumentURL / DocumentSHA256 name an external document (legal items);
	// both "" when absent. The hash pins the exact bytes Version means.
	DocumentURL    string
	DocumentSHA256 string
	Version        string
	// Blocking describes what is paused until this is approved.
	Blocking string
	// Required: true = blocking, false = informational.
	Required bool
	Approve  ApproveAction
}

// PendingApprovals is GET /api/v1/me/pending-approvals.
type PendingApprovals struct {
	Items []PendingApproval
	// Count is Fleet's count as sent; callers count Items.
	Count int
}

// Find returns the item with id.
func (p PendingApprovals) Find(id string) (PendingApproval, bool) {
	for _, it := range p.Items {
		if it.ID == id {
			return it, true
		}
	}
	return PendingApproval{}, false
}

type pendingApprovalsWire struct {
	Items *[]json.RawMessage `json:"items"`
	Count *int               `json:"count"`
}

type pendingApprovalWire struct {
	ID             *string         `json:"id"`
	Kind           *string         `json:"kind"`
	Title          *string         `json:"title"`
	Summary        *string         `json:"summary"`
	BodyText       *string         `json:"body_text"`
	DocumentURL    *string         `json:"document_url"`
	DocumentSHA256 *string         `json:"document_sha256"`
	Version        *string         `json:"version"`
	Blocking       *string         `json:"blocking"`
	Required       *bool           `json:"required"`
	Approve        json.RawMessage `json:"approve"`
}

type approveWire struct {
	Method *string          `json:"method"`
	Path   *string          `json:"path"`
	Body   *json.RawMessage `json:"body"`
}

// DecodePendingApprovals decodes a hub body. Required keys per item: id,
// kind, title, summary, version, blocking (string), required (bool) and
// approve {method, path, body (object)}. body_text and document_url are each
// optional; a document_url must be an absolute http(s) URL and comes with a
// 64-hex document_sha256. Unknown keys are ignored; unknown kinds are kept.
func DecodePendingApprovals(raw []byte) (PendingApprovals, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var w pendingApprovalsWire
	if err := dec.Decode(&w); err != nil {
		return PendingApprovals{}, fmt.Errorf("%w: %v", ErrPendingApprovalsDecode, err)
	}
	if dec.More() {
		return PendingApprovals{}, fmt.Errorf("%w: trailing data after the object", ErrPendingApprovalsDecode)
	}
	if w.Items == nil || *w.Items == nil {
		return PendingApprovals{}, fmt.Errorf("%w: items missing or null", ErrPendingApprovalsDecode)
	}
	if w.Count == nil || *w.Count < 0 {
		return PendingApprovals{}, fmt.Errorf("%w: count missing or negative", ErrPendingApprovalsDecode)
	}
	out := PendingApprovals{Items: make([]PendingApproval, 0, len(*w.Items)), Count: *w.Count}
	seen := map[string]bool{}
	for i, rawItem := range *w.Items {
		it, err := decodePendingApproval(rawItem)
		if err != nil {
			return PendingApprovals{}, fmt.Errorf("items[%d]: %w", i, err)
		}
		if seen[it.ID] {
			return PendingApprovals{}, fmt.Errorf("%w: items[%d]: duplicate id", ErrPendingApprovalsDecode, i)
		}
		seen[it.ID] = true
		out.Items = append(out.Items, it)
	}
	return out, nil
}

func decodePendingApproval(raw json.RawMessage) (PendingApproval, error) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || t[0] != '{' {
		return PendingApproval{}, fmt.Errorf("%w: item is not an object", ErrPendingApprovalsDecode)
	}
	var w pendingApprovalWire
	if err := json.Unmarshal(t, &w); err != nil {
		return PendingApproval{}, fmt.Errorf("%w: %v", ErrPendingApprovalsDecode, err)
	}
	missing := func(k string) error { return fmt.Errorf("%w: missing key %q", ErrPendingApprovalsDecode, k) }
	switch {
	case w.ID == nil || strings.TrimSpace(*w.ID) == "":
		return PendingApproval{}, missing("id")
	case w.Kind == nil || strings.TrimSpace(*w.Kind) == "":
		return PendingApproval{}, missing("kind")
	case w.Title == nil:
		return PendingApproval{}, missing("title")
	case w.Summary == nil:
		return PendingApproval{}, missing("summary")
	case w.Version == nil:
		return PendingApproval{}, missing("version")
	case w.Blocking == nil:
		return PendingApproval{}, missing("blocking")
	case w.Required == nil:
		return PendingApproval{}, missing("required")
	case w.Approve == nil:
		return PendingApproval{}, missing("approve")
	}
	it := PendingApproval{
		ID: *w.ID, Kind: *w.Kind, Title: *w.Title, Summary: *w.Summary,
		Version: *w.Version, Blocking: *w.Blocking, Required: *w.Required,
	}
	if w.BodyText != nil {
		it.BodyText = *w.BodyText
	}
	if w.DocumentURL != nil && *w.DocumentURL != "" {
		u, err := url.Parse(*w.DocumentURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return PendingApproval{}, fmt.Errorf("%w: document_url is not an absolute http(s) URL", ErrPendingApprovalsDecode)
		}
		if w.DocumentSHA256 == nil || !isSHA256Hex(*w.DocumentSHA256) {
			return PendingApproval{}, fmt.Errorf("%w: document_url without a 64-hex document_sha256", ErrPendingApprovalsDecode)
		}
		it.DocumentURL, it.DocumentSHA256 = *w.DocumentURL, strings.ToLower(*w.DocumentSHA256)
	}
	act, err := decodeApproveAction(w.Approve)
	if err != nil {
		return PendingApproval{}, err
	}
	it.Approve = act
	return it, nil
}

func decodeApproveAction(raw json.RawMessage) (ApproveAction, error) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || t[0] != '{' {
		return ApproveAction{}, fmt.Errorf("%w: approve is not an object", ErrPendingApprovalsDecode)
	}
	var w approveWire
	if err := json.Unmarshal(t, &w); err != nil {
		return ApproveAction{}, fmt.Errorf("%w: approve: %v", ErrPendingApprovalsDecode, err)
	}
	switch {
	case w.Method == nil || *w.Method == "":
		return ApproveAction{}, fmt.Errorf("%w: approve.method missing", ErrPendingApprovalsDecode)
	case w.Path == nil || *w.Path == "":
		return ApproveAction{}, fmt.Errorf("%w: approve.path missing", ErrPendingApprovalsDecode)
	case w.Body == nil:
		return ApproveAction{}, fmt.Errorf("%w: approve.body missing", ErrPendingApprovalsDecode)
	}
	body := bytes.TrimSpace(*w.Body)
	if len(body) == 0 || body[0] != '{' {
		return ApproveAction{}, fmt.Errorf("%w: approve.body is not an object", ErrPendingApprovalsDecode)
	}
	return ApproveAction{Method: *w.Method, Path: *w.Path, Body: append(json.RawMessage(nil), body...)}, nil
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// GetPendingApprovals reads GET /api/v1/me/pending-approvals. A 404 is
// ErrPendingApprovalsUnavailable (an older Fleet); any other non-2xx is an
// *ApprovalError. A suspended org gets an empty list from Fleet; a paused
// org is still served.
func (c *Client) GetPendingApprovals(ctx context.Context) (PendingApprovals, error) {
	if c == nil || c.isNop {
		return PendingApprovals{}, ErrFleetDisabled
	}
	resp, err := c.Get(ctx, "/api/v1/me/pending-approvals")
	if err != nil {
		return PendingApprovals{}, fmt.Errorf("fleet: get /me/pending-approvals: %w", err)
	}
	raw, err := readApprovalResponse(resp)
	if err != nil {
		var ae *ApprovalError
		if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
			return PendingApprovals{}, ErrPendingApprovalsUnavailable
		}
		return PendingApprovals{}, err
	}
	return DecodePendingApprovals(raw)
}

// ApprovePendingItem sends item.Approve verbatim — THE allowlist enforcement
// point. A method + path outside approveAllowlist is refused with
// ErrApproveActionNotAllowed before any request is built, and logged with
// the kind, method and path only (never the body). A stale item answers an
// *ApprovalError matching ErrApprovalStale.
func (c *Client) ApprovePendingItem(ctx context.Context, item PendingApproval) error {
	if c == nil || c.isNop {
		return ErrFleetDisabled
	}
	if !ApproveActionAllowed(item.Approve.Method, item.Approve.Path) {
		slog.Warn("fleet: pending approval action refused (not in the harness allowlist)",
			"kind", item.Kind, "method", item.Approve.Method, "path", item.Approve.Path)
		return ErrApproveActionNotAllowed
	}
	resp, err := c.Post(ctx, item.Approve.Path, "application/json", bytes.NewReader(item.Approve.Body))
	if err != nil {
		return fmt.Errorf("fleet: approve %s: %w", item.Kind, err)
	}
	_, err = readApprovalResponse(resp)
	return err
}

// readApprovalResponse drains and closes resp; a non-2xx is *ApprovalError.
func readApprovalResponse(resp *http.Response) ([]byte, error) {
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("fleet: pending approvals: read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		e := &ApprovalError{Status: resp.StatusCode}
		var env mlErrEnvelope
		if json.Unmarshal(raw, &env) == nil {
			e.Code = env.Code
		}
		return nil, e
	}
	return raw, nil
}
