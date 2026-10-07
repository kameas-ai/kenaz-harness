package fleet

// handoff_receive.go — team handoff accept + delete
// (device-keys-handoff-01DEVKH01 WP05; kenaz-fleet contract §10.3).
//
// GET /api/v1/handoff/{id} returns every key wrap of the item; this device
// picks the entry whose fingerprint matches ITS OWN handoff key
// (seed + node id), unwraps the content key (aad = key_id) and opens each
// event (aad = session_id:seq). Legacy mode "direct" items (a v0.91
// single-key sender; fleet still stores them until we signal O5) use the
// v1 derivation with the same device key.
//
// The fetch is rate limited fleet-side (burst 5, 30/min → 429 +
// Retry-After); it is retried at most twice, waiting the advertised time
// (capped), never hot-looped.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

const (
	handoffFetchRetries     = 2
	handoffFetchMaxWait     = 30 * time.Second
	handoffFetchDefaultWait = 5 * time.Second
	handoffFetchMaxBody     = handoffMaxBodyBytes + (4 << 20) // events re-encoded + wraps
)

// ErrHandoffNotForThisDevice: the item was wrapped to this user's OTHER
// devices (or to a previous key of this one — e.g. before a recovery-code
// import), so this device cannot open it. Fleet makes no promise otherwise.
var ErrHandoffNotForThisDevice = &HandoffError{Code: "not_for_this_device",
	msg: "This shared session was sent to another of your devices (or to an earlier key of this one), so it can't be opened here."}

// ErrHandoffItemGone: fleet's uniform 404 handoff_not_found.
var ErrHandoffItemGone = &HandoffError{Code: "handoff_not_found", Status: http.StatusNotFound,
	msg: "This shared session is no longer available — it expired, was dismissed, or was already opened on another of your devices."}

// AcceptedHandoff is a decrypted inbox item ready to persist.
type AcceptedHandoff struct {
	InboxItemID  string
	SessionID    string
	SenderUserID string
	// SenderEmail comes from the last Inbox() listing; may be empty.
	SenderEmail string
	// Mode is "wrapped" (v2) or "direct" (legacy v1).
	Mode string
	// Events are plaintext, seq-ordered. Privacy: never logged.
	Events []SessionEventRecord
}

type handoffGetRecipient struct {
	KeyID              string `json:"key_id"`
	Fingerprint        string `json:"fingerprint"`
	EphemeralPublicKey []byte `json:"ephemeral_public_key"`
	WrappedKey         []byte `json:"wrapped_key"`
	WrapNonce          []byte `json:"wrap_nonce"`
}

type handoffGetResponse struct {
	InboxItemID        string                `json:"inbox_item_id"`
	SessionID          string                `json:"session_id"`
	SenderUserID       string                `json:"sender_user_id"`
	Mode               string                `json:"mode"`
	EphemeralPublicKey []byte                `json:"ephemeral_public_key"`
	Recipients         []handoffGetRecipient `json:"recipients"`
	Events             []wireEvent           `json:"events"`
}

func defaultSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// fetchHandoff GETs one item, honouring 429 Retry-After with a bounded
// number of waits.
func (h *HandoffHandler) fetchHandoff(ctx context.Context, inboxItemID string) (handoffGetResponse, error) {
	sleep := h.sleep
	if sleep == nil {
		sleep = defaultSleep
	}
	path := "/api/v1/handoff/" + url.PathEscape(inboxItemID)
	for attempt := 0; ; attempt++ {
		resp, err := h.client.Get(ctx, path)
		if err != nil {
			return handoffGetResponse{}, handoffTransportError(err)
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, handoffFetchMaxBody))
		_ = resp.Body.Close()
		if isPlainNotFound(resp.StatusCode, resp.Header.Get("Content-Type"), body) {
			return handoffGetResponse{}, h.client.markEndpointUnsupported(FeatureTeamHandoff, "/api/v1/handoff/{id}")
		}
		switch resp.StatusCode {
		case http.StatusOK:
			var out handoffGetResponse
			if err := json.Unmarshal(body, &out); err != nil {
				return handoffGetResponse{}, fmt.Errorf("fleet: accept share: parse: %w", err)
			}
			return out, nil
		case http.StatusNotFound:
			return handoffGetResponse{}, ErrHandoffItemGone
		case http.StatusTooManyRequests:
			herr := mapHandoffHTTPError(resp.StatusCode, resp.Header, body)
			if attempt >= handoffFetchRetries {
				herr.msg = "Too many shared sessions were opened in a short time. Please wait a moment and try again."
				return handoffGetResponse{}, herr
			}
			wait := herr.RetryAfter
			if wait <= 0 {
				wait = handoffFetchDefaultWait
			}
			if wait > handoffFetchMaxWait {
				wait = handoffFetchMaxWait
			}
			logging.L().Info("fleet.handoff.fetch_rate_limited", "wait_ms", wait.Milliseconds(), "attempt", attempt+1)
			if err := sleep(ctx, wait); err != nil {
				return handoffGetResponse{}, err
			}
		default:
			return handoffGetResponse{}, mapHandoffHTTPError(resp.StatusCode, resp.Header, body)
		}
	}
}

// AcceptShare fetches inboxItemID, decrypts it with THIS device's handoff
// key and returns the plaintext events for the caller to persist. It does
// not delete the item (the caller deletes after a successful persist —
// OQ-1).
//
// Privacy invariant: returned plaintext event bytes are never logged.
func (h *HandoffHandler) AcceptShare(ctx context.Context, inboxItemID string) (AcceptedHandoff, error) {
	if h.client == nil || h.client.isNop {
		return AcceptedHandoff{}, ErrFleetDisabled
	}
	if err := h.client.endpointUnsupported(FeatureTeamHandoff); err != nil {
		return AcceptedHandoff{}, err
	}
	payload, err := h.fetchHandoff(ctx, inboxItemID)
	if err != nil {
		return AcceptedHandoff{}, err
	}
	priv, err := LoadOwnHandoffPrivKey(h.client.dataDir)
	if err != nil {
		return AcceptedHandoff{}, &HandoffError{Code: "no_device_key", cause: err,
			msg: "This device has no key for opening shared sessions yet. Sign in again, then retry."}
	}
	ownFP := KeyFingerprint(priv.PublicKey().Bytes())

	records := make([]SessionEventRecord, 0, len(payload.Events))
	switch payload.Mode {
	case "wrapped":
		var mine *handoffGetRecipient
		for i := range payload.Recipients {
			if payload.Recipients[i].Fingerprint == ownFP {
				mine = &payload.Recipients[i]
				break
			}
		}
		if mine == nil {
			return AcceptedHandoff{}, ErrHandoffNotForThisDevice
		}
		ck, err := unwrapContentKey(priv, mine.EphemeralPublicKey, mine.WrappedKey, mine.WrapNonce, mine.KeyID)
		if err != nil {
			return AcceptedHandoff{}, &HandoffError{Code: "decrypt_failed", cause: err,
				msg: "This shared session couldn't be decrypted on this device."}
		}
		for _, we := range payload.Events {
			pt, err := openHandoffEvent(ck, payload.SessionID, we.Seq, we.EncryptedPayload, we.Nonce)
			if err != nil {
				return AcceptedHandoff{}, &HandoffError{Code: "decrypt_failed", cause: err,
					msg: "This shared session couldn't be decrypted on this device (it may have been altered)."}
			}
			records = append(records, SessionEventRecord{Seq: we.Seq, Bytes: pt})
		}
	case "direct":
		// Legacy v1 single-key item: only openable by the device whose key
		// it was addressed to.
		for _, r := range payload.Recipients {
			if r.Fingerprint != "" && r.Fingerprint != ownFP {
				return AcceptedHandoff{}, ErrHandoffNotForThisDevice
			}
		}
		key, err := deriveV1DirectKey(priv, payload.EphemeralPublicKey)
		if err != nil {
			return AcceptedHandoff{}, &HandoffError{Code: "decrypt_failed", cause: err,
				msg: "This shared session couldn't be decrypted on this device."}
		}
		for _, we := range payload.Events {
			pt, err := Decrypt(key, we.EncryptedPayload, we.Nonce)
			if err != nil {
				return AcceptedHandoff{}, ErrHandoffNotForThisDevice
			}
			records = append(records, SessionEventRecord{Seq: we.Seq, Bytes: pt})
		}
	default:
		return AcceptedHandoff{}, &HandoffError{Code: "unknown_mode",
			msg: "This shared session uses a format this version of the app can't open. Update the app and try again."}
	}
	if len(records) == 0 {
		return AcceptedHandoff{}, &HandoffError{Code: "handoff_empty", msg: "This shared session is empty."}
	}

	h.inboxMu.Lock()
	email := h.senderEmails[inboxItemID]
	h.inboxMu.Unlock()

	logging.L().Info("fleet.handoff.session_accepted",
		"inbox_item_id", shortID(inboxItemID),
		"session_id", shortID(payload.SessionID),
		"sender", shortID(payload.SenderUserID),
		"mode", payload.Mode,
		"events", len(records),
	)
	h.emitAudit(ctx, contextaudit.KindFleetSessionSharedInbound, contextaudit.FleetSessionHandoffPayload{
		SessionID:       payload.SessionID,
		RecipientUserID: payload.SenderUserID,
		InboxItemID:     inboxItemID,
	})
	return AcceptedHandoff{
		InboxItemID:  inboxItemID,
		SessionID:    payload.SessionID,
		SenderUserID: payload.SenderUserID,
		SenderEmail:  email,
		Mode:         payload.Mode,
		Events:       records,
	}, nil
}

// DeleteShare removes inboxItemID from the recipient's inbox (DELETE
// /api/v1/handoff/{id}: recipient-only, 204, idempotent; fleet erases the
// ciphertext and wraps at once). A 404 handoff_not_found is success — the
// item is gone either way.
func (h *HandoffHandler) DeleteShare(ctx context.Context, inboxItemID string) error {
	if h.client == nil || h.client.isNop {
		return ErrFleetDisabled
	}
	if err := h.client.endpointUnsupported(FeatureTeamHandoff); err != nil {
		return err
	}
	resp, err := h.client.Delete(ctx, "/api/v1/handoff/"+url.PathEscape(inboxItemID))
	if err != nil {
		return handoffTransportError(err)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	if isPlainNotFound(resp.StatusCode, resp.Header.Get("Content-Type"), body) {
		return h.client.markEndpointUnsupported(FeatureTeamHandoff, "/api/v1/handoff/{id}")
	}
	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK, http.StatusNotFound:
		h.inboxMu.Lock()
		delete(h.senderEmails, inboxItemID)
		h.inboxMu.Unlock()
		return nil
	}
	return mapHandoffHTTPError(resp.StatusCode, resp.Header, body)
}
