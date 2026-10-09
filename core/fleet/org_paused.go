package fleet

// org_paused.go — the staff-only "pause paid features" org state
// (kenaz-fleet PR #206, contract confirmed by fleet 2026-10-08).
//
// While an org is paused, every customer route outside fleet's explicit
// allowlist answers
//
//	403 {"code":"org_paused","message":"…","details":{"paused_category":"…"}}
//
// — including GET /configs and POST /context/append. GET /me/capabilities,
// /me and /me/ml keep answering 200: capabilities carries "paused": true,
// "paused_category" and every capability false. That poll is the RECOVERY
// signal — a pause is reversible and nothing is deleted.
//
// A pause is NOT a tier problem (capability_not_in_tier) and NOT an auth
// problem (not_authorized / signed_out). Every harness consumer treats it as
// TRANSIENT: back off on its existing tiers, never latch permanently, never
// record a blocked-request row, and report the lane reason "org_paused".
// When the capability poll observes paused true→false the Client fans out
// OnOrgUnpaused so every circuit / latch org_paused set reopens at once
// (the same shape as the sign-in reset path).
//
// The choke point is Client.do: a 403 whose body is the org_paused envelope
// is converted into an *OrgPausedError there, so every consumer sees the
// same typed error on its error path — no consumer's "403 ⇒ not in tier /
// not authorized" branch can mislabel it.

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// CodeOrgPaused is fleet's 403 error code for a paused org.
const CodeOrgPaused = "org_paused"

// ReasonOrgPaused is the lane / status reason every fleet consumer reports
// while the org is paused.
const ReasonOrgPaused = "org_paused"

// Paused categories (the only part of a pause shown to the org's members).
// Exact strings confirmed by fleet for PR #206.
const (
	PausedCategoryBillingReview = "billing_review"
	PausedCategorySecurity      = "security"
	PausedCategoryAbuse         = "abuse"
	PausedCategoryLegal         = "legal"
	PausedCategoryOther         = "other"
)

// NormalizePausedCategory maps an absent or unknown category to "other", so
// the UI always has a one-liner to show.
func NormalizePausedCategory(c string) string {
	switch c = strings.TrimSpace(c); c {
	case PausedCategoryBillingReview, PausedCategorySecurity, PausedCategoryAbuse,
		PausedCategoryLegal, PausedCategoryOther:
		return c
	}
	return PausedCategoryOther
}

// OrgPausedCopy is the one user-facing sentence for a paused org on the Go
// side (error copy shown verbatim, e.g. the share dialog). The banner's
// per-category one-liners live in the frontend (orgPausedCopy.ts). Never
// upsell copy: a pause is not a plan problem.
const OrgPausedCopy = "Paused by your organization's account status — contact your admin."

// ErrOrgPaused is the sentinel every *OrgPausedError matches via errors.Is.
var ErrOrgPaused = errors.New("fleet: your organization's paid features are paused")

// OrgPausedError is a 403 org_paused refusal from any fleet route.
type OrgPausedError struct {
	// PausedCategory is one of the PausedCategory* constants (normalised).
	PausedCategory string
	// Message is the server's message, if any (not shown verbatim; the UI
	// owns the copy).
	Message string
}

func (e *OrgPausedError) Error() string {
	return ErrOrgPaused.Error() + " (" + e.PausedCategory + ")"
}

// Is makes errors.Is(err, ErrOrgPaused) match.
func (e *OrgPausedError) Is(target error) bool { return target == ErrOrgPaused }

// IsOrgPaused reports whether err is (or wraps) an org_paused refusal.
func IsOrgPaused(err error) bool { return err != nil && errors.Is(err, ErrOrgPaused) }

// OrgPausedCategoryOf returns the paused category carried by err, or "".
func OrgPausedCategoryOf(err error) string {
	var pe *OrgPausedError
	if errors.As(err, &pe) {
		return pe.PausedCategory
	}
	return ""
}

// ParseOrgPaused classifies a response status + body. It returns a non-nil
// *OrgPausedError only for a 403 carrying the JSON envelope code
// "org_paused". The category is details.paused_category (confirmed against
// #206's head); absent or unknown normalises to "other".
func ParseOrgPaused(status int, body []byte) *OrgPausedError {
	if status != http.StatusForbidden {
		return nil
	}
	env, ok := parseFleetError(body)
	if !ok || env.Code != CodeOrgPaused {
		return nil
	}
	cat, _ := env.Details["paused_category"].(string)
	return &OrgPausedError{PausedCategory: NormalizePausedCategory(cat), Message: env.Message}
}

// orgPausedPeekLimit bounds how much of a 403 body Client.do buffers to look
// for the envelope. The rest of the body (if any) is still delivered.
const orgPausedPeekLimit = 16 << 10

// checkOrgPaused inspects a 403 response. When it is org_paused the body is
// drained and closed and the typed error returned; otherwise resp.Body is
// replaced with a reader that replays the peeked bytes, so callers see the
// response unchanged.
func checkOrgPaused(resp *http.Response) *OrgPausedError {
	if resp == nil || resp.StatusCode != http.StatusForbidden || resp.Body == nil {
		return nil
	}
	peek, _ := io.ReadAll(io.LimitReader(resp.Body, orgPausedPeekLimit))
	if pe := ParseOrgPaused(resp.StatusCode, peek); pe != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return pe
	}
	resp.Body = &replayBody{Reader: io.MultiReader(bytes.NewReader(peek), resp.Body), closer: resp.Body}
	return nil
}

type replayBody struct {
	io.Reader
	closer io.Closer
}

func (r *replayBody) Close() error { return r.closer.Close() }

// OrgPauseStatus is the client's current view of the org pause.
type OrgPauseStatus struct {
	Paused         bool
	PausedCategory string
}

// orgPauseState tracks the pause as observed by this Client: set by any
// org_paused refusal (Client.do) or by a capability poll, cleared only by a
// capability poll reporting paused:false. Mutex-guarded.
type orgPauseState struct {
	mu         sync.Mutex
	paused     bool
	category   string
	onChange   []func(OrgPauseStatus)
	onUnpaused []func()
}

// ResetOrgPause forgets the observed pause WITHOUT firing any listener — a
// fleet session reset (sign-in / sign-out): the next session may be a
// different org, and only its own capability poll or refusal may say it is
// paused. No OnOrgUnpaused fan-out: nothing was unpaused.
func (c *Client) ResetOrgPause() {
	if c == nil || c.isNop {
		return
	}
	c.orgPause.mu.Lock()
	c.orgPause.paused, c.orgPause.category = false, ""
	c.orgPause.mu.Unlock()
}

// OrgPause returns the pause state this client has observed.
func (c *Client) OrgPause() OrgPauseStatus {
	if c == nil || c.isNop {
		return OrgPauseStatus{}
	}
	c.orgPause.mu.Lock()
	defer c.orgPause.mu.Unlock()
	return OrgPauseStatus{Paused: c.orgPause.paused, PausedCategory: c.orgPause.category}
}

// OnOrgPauseChange registers fn to run whenever the observed pause state
// (paused or category) changes. Runs on the observing goroutine, outside
// the lock.
func (c *Client) OnOrgPauseChange(fn func(OrgPauseStatus)) {
	if c == nil || c.isNop || fn == nil {
		return
	}
	c.orgPause.mu.Lock()
	c.orgPause.onChange = append(c.orgPause.onChange, fn)
	c.orgPause.mu.Unlock()
}

// OnOrgUnpaused registers fn to run when the pause lifts (paused true→false
// on a capability poll). Every consumer that held back on org_paused
// registers its reopen here — the one fan-out for the recovery.
func (c *Client) OnOrgUnpaused(fn func()) {
	if c == nil || c.isNop || fn == nil {
		return
	}
	c.orgPause.mu.Lock()
	c.orgPause.onUnpaused = append(c.orgPause.onUnpaused, fn)
	c.orgPause.mu.Unlock()
}

// observeOrgPaused records a pause seen on a refused request.
func (c *Client) observeOrgPaused(category string) {
	c.setOrgPause(true, category)
}

// ObserveCapabilitiesPause records the pause state a capability poll
// reported. The authoritative recovery path: paused:false after paused:true
// fires every OnOrgUnpaused listener.
func (c *Client) ObserveCapabilitiesPause(paused bool, category string) {
	c.setOrgPause(paused, category)
}

func (c *Client) setOrgPause(paused bool, category string) {
	if c == nil || c.isNop {
		return
	}
	if paused {
		category = NormalizePausedCategory(category)
	} else {
		category = ""
	}
	c.orgPause.mu.Lock()
	wasPaused, prevCat := c.orgPause.paused, c.orgPause.category
	c.orgPause.paused, c.orgPause.category = paused, category
	changed := wasPaused != paused || prevCat != category
	var onChange []func(OrgPauseStatus)
	var onUnpaused []func()
	if changed {
		onChange = append(onChange, c.orgPause.onChange...)
	}
	if wasPaused && !paused {
		onUnpaused = append(onUnpaused, c.orgPause.onUnpaused...)
	}
	c.orgPause.mu.Unlock()
	if !changed {
		return
	}
	if paused {
		logging.L().Warn("fleet.org_paused", "paused_category", category)
	} else {
		logging.L().Info("fleet.org_unpaused", "reopening", len(onUnpaused))
	}
	st := OrgPauseStatus{Paused: paused, PausedCategory: category}
	for _, fn := range onChange {
		fn(st)
	}
	for _, fn := range onUnpaused {
		fn()
	}
}
