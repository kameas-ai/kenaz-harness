package fleet

// team_handoff.go — team session handoff: re-encrypt + route + inbox.
//
// Flow:
//   1. Caller retrieves decrypted session events (via SessionSyncer.Resume or
//      direct DB access — outside this layer).
//   2. ShareSession re-encrypts events with the recipient's public key
//      (obtained from the fleet identity service) and POSTs to handoff/send.
//   3. Inbox: Inbox() returns items shared with the current user.
//   4. AcceptShare: downloads the shared payload, decrypts with the recipient's
//      private key, and returns events for the caller to persist.
//
// Key exchange model (v0.21.0):
//   Each user has an asymmetric key pair in the fleet identity service.
//   Fleet acts as the KX broker: GET /api/v1/identity/public-key?user_id=<id>
//   returns the recipient's X25519 public key.
//   For v0.21.0 we use a simplified model: the re-encrypt-on-send approach
//   means the sender decrypts and re-encrypts with the recipient's key.
//   The actual key agreement uses X25519 ECDH + HKDF + XChaCha20-Poly1305
//   (the same AEAD as context_crypto.go, different key derivation path).
//
// Privacy invariant: session content is NEVER logged. The sender's decrypted
// events are only in memory; they are re-encrypted before being transmitted.
// Audit emits only opaque IDs (session_id, recipient_user_id, inbox_item_id).

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// ErrTeamHandoffCapabilityRequired is returned when sharing requires
// team_session_handoff which is not in the user's tier.
var ErrTeamHandoffCapabilityRequired = errors.New("fleet: team_session_handoff capability required")

// ErrHandoffRecipientNotFound is returned when the recipient user ID cannot
// be resolved to a public key.
var ErrHandoffRecipientNotFound = errors.New("fleet: handoff recipient not found")

// TeamMember is a member of the user's org visible in the "Share with…" picker.
type TeamMember struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	// CanReceive is true when fleet has a registered X25519 public key for
	// this member, meaning they can receive encrypted session handoffs.
	// Members without a registered key are listed but cannot be selected as
	// handoff recipients.
	CanReceive bool `json:"can_receive"`
}

// InboxItem is a session share received by the current user.
type InboxItem struct {
	InboxItemID  string    `json:"inbox_item_id"`
	SessionID    string    `json:"session_id"`
	SenderUserID string    `json:"sender_user_id"`
	SenderEmail  string    `json:"sender_email"`
	ReceivedAt   time.Time `json:"received_at"`
}

// HandoffHandler manages team session handoffs.
type HandoffHandler struct {
	client  *Client
	emitter contextaudit.Emitter
	caps    *Capabilities
}

// NewHandoffHandler constructs a HandoffHandler.
func NewHandoffHandler(client *Client, emitter contextaudit.Emitter, caps *Capabilities) *HandoffHandler {
	return &HandoffHandler{client: client, emitter: emitter, caps: caps}
}

// ListTeam returns org members visible in the share picker.
func (h *HandoffHandler) ListTeam(ctx context.Context) ([]TeamMember, error) {
	if h.client == nil || h.client.isNop {
		return nil, ErrFleetDisabled
	}

	resp, err := h.client.Get(ctx, "/api/v1/team/members")
	if err != nil {
		return nil, fmt.Errorf("fleet: list team: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fleet: list team: status %d", resp.StatusCode)
	}
	var members []TeamMember
	if err := json.Unmarshal(body, &members); err != nil {
		return nil, fmt.Errorf("fleet: list team: parse: %w", err)
	}
	return members, nil
}

// ShareSession re-encrypts session events with the recipient's public key and
// routes them through fleet handoff. plainEvents must already be decrypted
// (the caller fetches them from the local DB or via Resume).
//
// Privacy invariant: plainEvents are re-encrypted in memory before any network
// call. No event bytes are logged.
func (h *HandoffHandler) ShareSession(ctx context.Context, sessionID, recipientUserID string, plainEvents []SessionEventRecord) error {
	if h.client == nil || h.client.isNop {
		return ErrFleetDisabled
	}
	if h.caps != nil && !h.caps.Has(CapTeamSessionHandoff) {
		return ErrTeamHandoffCapabilityRequired
	}

	// Fetch recipient's public key from the identity service.
	recipientPubKey, err := h.fetchRecipientPublicKey(ctx, recipientUserID)
	if err != nil {
		return fmt.Errorf("fleet: share session: %w", err)
	}

	// Derive a ephemeral per-handoff key via ECDH + HKDF.
	handoffKey, ephemeralPubKeyBytes, err := deriveHandoffKey(recipientPubKey)
	if err != nil {
		return fmt.Errorf("fleet: share session: derive handoff key: %w", err)
	}

	// Re-encrypt all events with the handoff key.
	wireEvents := make([]wireEvent, 0, len(plainEvents))
	for _, r := range plainEvents {
		ct, nonce, err := Encrypt(handoffKey, r.Bytes)
		if err != nil {
			return fmt.Errorf("fleet: share session: encrypt event seq=%d: %w", r.Seq, err)
		}
		wireEvents = append(wireEvents, wireEvent{
			Seq:              r.Seq,
			EncryptedPayload: ct,
			Nonce:            nonce,
		})
	}

	// POST to fleet handoff.
	handoffReq := map[string]any{
		"session_id":           sessionID,
		"recipient_user_id":    recipientUserID,
		"ephemeral_public_key": ephemeralPubKeyBytes,
		"events":               wireEvents,
	}
	data, err := json.Marshal(handoffReq)
	if err != nil {
		return fmt.Errorf("fleet: share session: marshal: %w", err)
	}

	resp, err := h.client.Post(ctx, "/api/v1/handoff/send", "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("fleet: share session: POST: %w", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("fleet: share session: status %d", resp.StatusCode)
	}

	logging.L().Info("fleet.handoff.session_shared_outbound",
		"session_id", shortID(sessionID),
		"recipient", shortID(recipientUserID),
	)
	h.emitAudit(ctx, contextaudit.KindFleetSessionSharedOutbound, contextaudit.FleetSessionHandoffPayload{
		SessionID:       sessionID,
		RecipientUserID: recipientUserID,
	})
	return nil
}

// Inbox returns sessions shared with the current user.
func (h *HandoffHandler) Inbox(ctx context.Context) ([]InboxItem, error) {
	if h.client == nil || h.client.isNop {
		return nil, ErrFleetDisabled
	}

	resp, err := h.client.Get(ctx, "/api/v1/handoff/inbox")
	if err != nil {
		return nil, fmt.Errorf("fleet: inbox: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fleet: inbox: status %d", resp.StatusCode)
	}
	var items []InboxItem
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("fleet: inbox: parse: %w", err)
	}
	return items, nil
}

// acceptHandoffResponse is the envelope returned by GET /api/v1/handoff/{id}.
type acceptHandoffResponse struct {
	SessionID          string      `json:"session_id"`
	SenderUserID       string      `json:"sender_user_id"`
	EphemeralPublicKey []byte      `json:"ephemeral_public_key"`
	Events             []wireEvent `json:"events"`
}

// AcceptShare fetches the handoff payload for inboxItemID, decrypts it with
// the current user's private key, and returns the plain SessionEventRecords.
// The caller is responsible for persisting them as a new local session.
//
// Privacy invariant: returned plaintext event bytes are never logged.
func (h *HandoffHandler) AcceptShare(ctx context.Context, inboxItemID string) ([]SessionEventRecord, error) {
	if h.client == nil || h.client.isNop {
		return nil, ErrFleetDisabled
	}

	resp, err := h.client.Get(ctx, "/api/v1/handoff/"+inboxItemID)
	if err != nil {
		return nil, fmt.Errorf("fleet: accept share: GET: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fleet: accept share: status %d", resp.StatusCode)
	}

	var payload acceptHandoffResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("fleet: accept share: parse: %w", err)
	}

	// Derive the same handoff key using our private key + sender's ephemeral pubkey.
	handoffKey, err := h.deriveReceiveKey(payload.EphemeralPublicKey)
	if err != nil {
		return nil, fmt.Errorf("fleet: accept share: derive key: %w", err)
	}

	// Decrypt all events.
	records := make([]SessionEventRecord, 0, len(payload.Events))
	for _, we := range payload.Events {
		pt, err := Decrypt(handoffKey, we.EncryptedPayload, we.Nonce)
		if err != nil {
			return nil, fmt.Errorf("fleet: accept share: decrypt seq=%d: %w", we.Seq, err)
		}
		records = append(records, SessionEventRecord{Seq: we.Seq, Bytes: pt})
	}

	logging.L().Info("fleet.handoff.session_accepted",
		"inbox_item_id", shortID(inboxItemID),
		"session_id", shortID(payload.SessionID),
		"sender", shortID(payload.SenderUserID),
		"events", len(records),
	)
	h.emitAudit(ctx, contextaudit.KindFleetSessionSharedInbound, contextaudit.FleetSessionHandoffPayload{
		SessionID:       payload.SessionID,
		RecipientUserID: payload.SenderUserID,
		InboxItemID:     inboxItemID,
	})
	return records, nil
}

// ── key exchange helpers ──────────────────────────────────────────────────────

// publicKeyResponse is the JSON shape returned by the identity service.
type publicKeyResponse struct {
	UserID    string `json:"user_id"`
	PublicKey []byte `json:"public_key"` // X25519 public key bytes
}

// fetchRecipientPublicKey retrieves the recipient's X25519 public key from fleet.
func (h *HandoffHandler) fetchRecipientPublicKey(ctx context.Context, recipientUserID string) ([]byte, error) {
	resp, err := h.client.Get(ctx, "/api/v1/identity/public-key?user_id="+recipientUserID)
	if err != nil {
		return nil, fmt.Errorf("fleet: fetch public key: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrHandoffRecipientNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fleet: fetch public key: status %d", resp.StatusCode)
	}
	var pkResp publicKeyResponse
	if err := json.Unmarshal(body, &pkResp); err != nil {
		return nil, fmt.Errorf("fleet: fetch public key: parse: %w", err)
	}
	if len(pkResp.PublicKey) == 0 {
		return nil, ErrHandoffRecipientNotFound
	}
	return pkResp.PublicKey, nil
}

// deriveHandoffKey generates an ephemeral X25519 key pair, performs ECDH with
// the recipient's public key, and derives a 32-byte AEAD key via HKDF.
// Returns (handoffKey, ephemeralPublicKeyBytes, error).
func deriveHandoffKey(recipientPubKeyBytes []byte) ([]byte, []byte, error) {
	curve := ecdh.X25519()

	// Generate ephemeral key pair for this handoff.
	ephemeralPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate ephemeral key: %w", err)
	}
	ephemeralPub := ephemeralPriv.PublicKey()

	// Parse recipient public key.
	recipientPub, err := curve.NewPublicKey(recipientPubKeyBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse recipient public key: %w", err)
	}

	// ECDH.
	sharedSecret, err := ephemeralPriv.ECDH(recipientPub)
	if err != nil {
		return nil, nil, fmt.Errorf("ECDH: %w", err)
	}

	// Derive symmetric key: HKDF(sharedSecret, info=LabelHandoffKey).
	handoffKey, err := DeriveKey(sharedSecret[:32], LabelHandoffKey)
	if err != nil {
		return nil, nil, fmt.Errorf("derive handoff key: %w", err)
	}

	return handoffKey, ephemeralPub.Bytes(), nil
}

// deriveReceiveKey derives the same handoff key as the sender by performing
// X25519 ECDH with our seed-derived private key and the sender's ephemeral
// public key, then running the same HKDF step used in deriveHandoffKey.
//
// Key agreement symmetry (Diffie-Hellman):
//
//	sender:   sharedSecret = ephemeralPriv.ECDH(recipientPub)
//	receiver: sharedSecret = recipientPriv.ECDH(ephemeralPub)
//	          ⟹ both sides derive the same sharedSecret → same AEAD key.
//
// The recipient's private key is deterministically derived from the device
// context seed via LoadOwnHandoffPrivKey. The corresponding public key is
// what the fleet identity service returns for this user ID; the sender fetches
// it via fetchRecipientPublicKey, so both sides use the same key material.
func (h *HandoffHandler) deriveReceiveKey(ephemeralPubKeyBytes []byte) ([]byte, error) {
	// Load our seed-derived X25519 private key.
	recipientPriv, err := LoadOwnHandoffPrivKey()
	if err != nil {
		return nil, fmt.Errorf("load own handoff private key: %w", err)
	}

	// Parse the sender's ephemeral public key.
	curve := ecdh.X25519()
	ephemeralPub, err := curve.NewPublicKey(ephemeralPubKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("parse ephemeral public key: %w", err)
	}

	// X25519 ECDH: recipientPriv · ephemeralPub == ephemeralPriv · recipientPub.
	sharedSecret, err := recipientPriv.ECDH(ephemeralPub)
	if err != nil {
		return nil, fmt.Errorf("ECDH receive: %w", err)
	}

	// Same HKDF step as deriveHandoffKey.
	return DeriveKey(sharedSecret[:32], LabelHandoffKey)
}

// ── audit helper ──────────────────────────────────────────────────────────────

func (h *HandoffHandler) emitAudit(ctx context.Context, kind contextaudit.Kind, payload any) {
	if h.emitter == nil {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		logging.L().Warn("fleet.handoff.audit_marshal_failed", "kind", string(kind), "err", err.Error())
		return
	}
	_ = h.emitter.Emit(ctx, contextaudit.Event{
		Kind:    kind,
		TS:      time.Now().UTC(),
		Payload: raw,
	})
}
