package fleet

// handoff_send.go — team handoff v2 send + the typed, human-readable error
// set (device-keys-handoff-01DEVKH01 WP04; kenaz-fleet contract §10.3).
//
// One share = one content key K (32 random bytes): every event is sealed
// once under K with aad = session_id:seq; K is wrapped to EVERY active
// device key of the recipient with a fresh ephemeral per key (aad =
// key_id). recipients[].key_id must EQUAL fleet's active set, else 409
// recipient_keys_stale with details.public_keys — the client re-wraps K
// (events unchanged) to that set and retries ONCE (OQ-6), under the same
// client_handoff_id so a racing duplicate dedupes server-side.
//
// The v1 single-key send body is GONE (OQ-8; fleet O5 removes its accept
// path once harnesses send v2). Accept still reads v1 "direct" items.
//
// Every non-2xx maps to a *HandoffError whose Error() is the copy the share
// dialog shows — never "status NNN" (unwired-ledger 2026-10-06 item 4).

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// Fleet's handoff caps (contract §10.3), pre-checked client-side so an
// oversized share fails with readable copy before any upload.
const (
	handoffMaxEvents      = 5000
	handoffMaxEventBytes  = 2 << 20  // per encrypted_payload
	handoffMaxBodyBytes   = 16 << 20 // whole POST body
	handoffMaxRecipients  = 16
	handoffStaleRetries   = 1
	handoffSendPath       = "/api/v1/handoff/send"
	handoffErrBodyMaxRead = 1 << 20 // stale-key details carry up to 16 keys
)

// HandoffError is a typed handoff failure. Error() is human copy; Code is
// fleet's error code (or a client-side code) for programmatic branching.
type HandoffError struct {
	// Code is fleet's {code} (e.g. "recipient_keys_stale") or a client
	// code ("session_empty", "too_large").
	Code string
	// Status is the HTTP status (0 for client-side checks).
	Status int
	// RetryAfter is fleet's Retry-After on 429 (0 when absent).
	RetryAfter time.Duration
	msg        string
	cause      error
}

func (e *HandoffError) Error() string { return e.msg }

// Unwrap exposes the transport cause (sign-in / network), if any.
func (e *HandoffError) Unwrap() error { return e.cause }

// Is lets callers match ErrHandoffRecipientNotFound for the recipient-
// lookup-shaped codes.
func (e *HandoffError) Is(target error) bool {
	return target == ErrHandoffRecipientNotFound &&
		(e.Code == "recipient_not_found" || e.Code == "public_key_not_found")
}

func handoffErr(code string, status int, msg string) *HandoffError {
	return &HandoffError{Code: code, Status: status, msg: msg}
}

// ErrHandoffSessionEmpty: nothing to share (checked before any network).
var ErrHandoffSessionEmpty = handoffErr("session_empty", 0, "This session has no messages to share yet.")

// handoffCopy is the dialog copy per fleet code.
func handoffCopy(code string, retryAfter time.Duration) string {
	switch code {
	case "handoff_empty":
		return "This session has no messages to share yet."
	case "recipient_keys_stale":
		return "Your teammate's devices changed while sharing. Please try again."
	case "recipient_no_key", "public_key_not_found":
		return "Your teammate doesn't have a device that can receive shared sessions yet. Ask them to sign in to the app, then try again."
	case "recipient_not_found":
		return "That teammate couldn't be found in your organization."
	case "invalid_recipient":
		return "You can't share a session with yourself."
	case "sender_pending_limit":
		return "You already have 10 shared sessions waiting for this teammate. Ask them to open or dismiss some first."
	case "recipient_inbox_full":
		return "Your teammate's shared-session inbox is full. Ask them to clear some items, then try again."
	case "handoff_too_large":
		return "This session is too large to share (limits: 16 MiB, 5,000 messages, 2 MiB per message)."
	case "client_handoff_id_conflict":
		return "This share collided with an earlier one. Please try again."
	case "rate_limited":
		if retryAfter > 0 {
			return fmt.Sprintf("You've shared too many sessions recently. Try again in %s.", humanWait(retryAfter))
		}
		return "You've shared too many sessions recently. Try again in a little while."
	case "capability_not_in_tier":
		return "Sharing sessions with teammates isn't included in your organization's plan."
	}
	if strings.HasPrefix(code, "permission_denied") {
		return "Your role in this organization doesn't allow sharing sessions."
	}
	return ""
}

func humanWait(d time.Duration) string {
	switch {
	case d < time.Minute:
		s := int(d.Seconds())
		if s <= 1 {
			return "a second"
		}
		return fmt.Sprintf("%d seconds", s)
	case d < 2*time.Minute:
		return "a minute"
	}
	return fmt.Sprintf("%d minutes", int(d.Minutes()+0.5))
}

// mapHandoffHTTPError turns a fleet error response into a *HandoffError.
func mapHandoffHTTPError(status int, header http.Header, body []byte) *HandoffError {
	var ra time.Duration
	if v := strings.TrimSpace(header.Get("Retry-After")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			ra = time.Duration(n) * time.Second
		}
	}
	code := ""
	if env, ok := parseFleetError(body); ok {
		code = env.Code
	}
	if code == "" {
		switch status {
		case http.StatusRequestEntityTooLarge:
			code = "handoff_too_large"
		case http.StatusTooManyRequests:
			code = "rate_limited"
		}
	}
	msg := handoffCopy(code, ra)
	if msg == "" {
		if code != "" {
			msg = fmt.Sprintf("Sharing failed (fleet error %q). Please try again later.", code)
		} else {
			msg = fmt.Sprintf("Sharing failed (fleet returned HTTP %d). Please try again later.", status)
		}
	}
	return &HandoffError{Code: code, Status: status, RetryAfter: ra, msg: msg}
}

// handoffTransportError maps a request that never produced a fleet
// answer (sign-in gone, network, 5xx after the client's retries) to
// readable copy, keeping the cause reachable via errors.Is/As.
func handoffTransportError(err error) error {
	switch {
	case errors.Is(err, ErrNotSignedIn), errors.Is(err, ErrTokenExpired):
		return &HandoffError{Code: "not_signed_in", msg: "Sign in to your organization to share sessions.", cause: err}
	case errors.Is(err, context.Canceled):
		return err
	}
	return &HandoffError{Code: "unavailable", msg: "Sharing failed: fleet is unavailable right now. Please try again later.", cause: err}
}

// handoffSendRequest is the v2 POST /api/v1/handoff/send body.
type handoffSendRequest struct {
	SessionID       string        `json:"session_id"`
	RecipientUserID string        `json:"recipient_user_id"`
	ClientHandoffID string        `json:"client_handoff_id"`
	Recipients      []handoffWrap `json:"recipients"`
	Events          []wireEvent   `json:"events"`
}

type handoffSendResponse struct {
	InboxItemID string    `json:"inbox_item_id"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// HandoffSendResult is a successful share.
type HandoffSendResult struct {
	InboxItemID string
	ExpiresAt   time.Time
	// RecipientDevices is how many device keys the content key was
	// wrapped to (after any stale-key retry).
	RecipientDevices int
}

// staleKeysDetails is the 409 recipient_keys_stale details shape (same
// entries as §10.2 public_keys).
type staleKeysDetails struct {
	Details struct {
		PublicKeys []publicKeyEntry `json:"public_keys"`
	} `json:"details"`
}

// wrapToAll wraps the content key to every key in keys.
func wrapToAll(keys []publicKeyEntry, contentKey []byte) ([]handoffWrap, error) {
	if len(keys) == 0 {
		return nil, handoffErr("recipient_no_key", 0, handoffCopy("recipient_no_key", 0))
	}
	if len(keys) > handoffMaxRecipients {
		return nil, handoffErr("too_many_recipient_keys", 0,
			"Your teammate has more registered devices than a share can address. Ask them to remove an old device.")
	}
	if err := validateKeySet(keys); err != nil {
		return nil, fmt.Errorf("fleet: share session: %w", err)
	}
	wraps := make([]handoffWrap, 0, len(keys))
	for _, k := range keys {
		w, err := wrapContentKey(rand.Reader, k.PublicKey, k.KeyID, contentKey)
		if err != nil {
			return nil, fmt.Errorf("fleet: share session: wrap: %w", err)
		}
		wraps = append(wraps, w)
	}
	return wraps, nil
}

// ShareSession encrypts plainEvents (self-contained transcript events,
// seq 1..N) once under a fresh content key, wraps the key to every active
// device of the recipient and posts the v2 handoff. plainEvents must be
// non-empty.
//
// Privacy invariant: plainEvents are encrypted in memory before any
// network call; no event bytes or key material are logged.
func (h *HandoffHandler) ShareSession(ctx context.Context, sessionID, recipientUserID string, plainEvents []SessionEventRecord) (HandoffSendResult, error) {
	if h.client == nil || h.client.isNop {
		return HandoffSendResult{}, ErrFleetDisabled
	}
	if h.caps != nil && !h.caps.Has(CapTeamSessionHandoff) {
		return HandoffSendResult{}, ErrTeamHandoffCapabilityRequired
	}
	if err := h.client.endpointUnsupported(FeatureTeamHandoff); err != nil {
		return HandoffSendResult{}, err
	}
	if len(plainEvents) == 0 {
		return HandoffSendResult{}, ErrHandoffSessionEmpty
	}
	if len(plainEvents) > handoffMaxEvents {
		return HandoffSendResult{}, handoffErr("too_large", 0,
			fmt.Sprintf("This session has %d messages; a share is limited to %d.", len(plainEvents), handoffMaxEvents))
	}

	// Encrypt every event ONCE under the content key.
	contentKey := make([]byte, handoffContentKeyLen)
	if _, err := rand.Read(contentKey); err != nil {
		return HandoffSendResult{}, fmt.Errorf("fleet: share session: content key: %w", err)
	}
	events := make([]wireEvent, 0, len(plainEvents))
	bodyEstimate := 512
	for _, r := range plainEvents {
		ct, nonce, err := sealHandoffEvent(contentKey, sessionID, r.Seq, r.Bytes)
		if err != nil {
			return HandoffSendResult{}, fmt.Errorf("fleet: share session: encrypt event seq=%d: %w", r.Seq, err)
		}
		if len(ct) > handoffMaxEventBytes {
			return HandoffSendResult{}, handoffErr("too_large", 0,
				fmt.Sprintf("Message %d of this session is larger than the 2 MiB a share allows per message.", r.Seq))
		}
		bodyEstimate += (len(ct)+len(nonce))*4/3 + 64
		events = append(events, wireEvent{Seq: r.Seq, EncryptedPayload: ct, Nonce: nonce})
	}
	if bodyEstimate > handoffMaxBodyBytes {
		return HandoffSendResult{}, handoffErr("too_large", 0, handoffCopy("handoff_too_large", 0))
	}

	keys, err := h.fetchRecipientKeys(ctx, recipientUserID)
	if err != nil {
		if errors.Is(err, ErrHandoffRecipientNotFound) {
			return HandoffSendResult{}, handoffErr("public_key_not_found", http.StatusNotFound, handoffCopy("public_key_not_found", 0))
		}
		if errors.Is(err, ErrEndpointUnsupported) {
			return HandoffSendResult{}, err
		}
		if errors.Is(err, ErrNotSignedIn) || errors.Is(err, ErrTokenExpired) {
			return HandoffSendResult{}, handoffTransportError(err)
		}
		return HandoffSendResult{}, &HandoffError{Code: "lookup_failed",
			msg: "Couldn't look up your teammate's devices right now. Please try again later.", cause: err}
	}

	req := handoffSendRequest{
		SessionID:       sessionID,
		RecipientUserID: recipientUserID,
		// Fresh per share invocation: dedupes a double-submitted dialog
		// and the stale-key retry, never two deliberate shares.
		ClientHandoffID: generateNodeID(),
		Events:          events,
	}
	for attempt := 0; ; attempt++ {
		req.Recipients, err = wrapToAll(keys, contentKey)
		if err != nil {
			return HandoffSendResult{}, err
		}
		res, stale, err := h.postHandoff(ctx, req)
		if err == nil {
			logging.L().Info("fleet.handoff.session_shared_outbound",
				"session_id", shortID(sessionID),
				"recipient", shortID(recipientUserID),
				"inbox_item_id", shortID(res.InboxItemID),
				"devices", len(req.Recipients),
				"events", len(events),
			)
			h.emitAudit(ctx, contextaudit.KindFleetSessionSharedOutbound, contextaudit.FleetSessionHandoffPayload{
				SessionID:       sessionID,
				RecipientUserID: recipientUserID,
				InboxItemID:     res.InboxItemID,
			})
			res.RecipientDevices = len(req.Recipients)
			return res, nil
		}
		if stale == nil || attempt >= handoffStaleRetries {
			return HandoffSendResult{}, err
		}
		// 409 recipient_keys_stale: a device was added/removed between
		// lookup and send. Re-wrap to fleet's current set, retry once.
		logging.L().Info("fleet.handoff.recipient_keys_stale_rewrap", "devices", len(stale))
		keys = stale
	}
}

// postHandoff POSTs one send. On 409 recipient_keys_stale it returns the
// fresh key set from details.public_keys alongside the error.
func (h *HandoffHandler) postHandoff(ctx context.Context, req handoffSendRequest) (HandoffSendResult, []publicKeyEntry, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return HandoffSendResult{}, nil, fmt.Errorf("fleet: share session: marshal: %w", err)
	}
	if len(data) > handoffMaxBodyBytes {
		return HandoffSendResult{}, nil, handoffErr("too_large", 0, handoffCopy("handoff_too_large", 0))
	}
	resp, err := h.client.Post(ctx, handoffSendPath, "application/json", bytes.NewReader(data))
	if err != nil {
		return HandoffSendResult{}, nil, handoffTransportError(err)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, handoffErrBodyMaxRead))
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if isPlainNotFound(resp.StatusCode, resp.Header.Get("Content-Type"), body) {
		return HandoffSendResult{}, nil, h.client.markEndpointUnsupported(FeatureTeamHandoff, handoffSendPath)
	}
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		var out handoffSendResponse
		if err := json.Unmarshal(body, &out); err != nil || out.InboxItemID == "" {
			return HandoffSendResult{}, nil, fmt.Errorf("fleet: share session: unreadable success response")
		}
		return HandoffSendResult{InboxItemID: out.InboxItemID, ExpiresAt: out.ExpiresAt}, nil, nil
	}
	herr := mapHandoffHTTPError(resp.StatusCode, resp.Header, body)
	if resp.StatusCode == http.StatusConflict && herr.Code == "recipient_keys_stale" {
		var d staleKeysDetails
		if json.Unmarshal(body, &d) == nil && len(d.Details.PublicKeys) > 0 {
			return HandoffSendResult{}, d.Details.PublicKeys, herr
		}
	}
	return HandoffSendResult{}, nil, herr
}
