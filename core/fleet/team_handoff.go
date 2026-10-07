package fleet

// team_handoff.go — team session handoff: re-encrypt + route + inbox.
//
// Flow (device-keys-handoff-01DEVKH01, kenaz-fleet contract §10):
//   1. The caller serializes the local session into self-contained events
//      (core/session EncodeHandoffTranscript) — outside this layer.
//   2. ShareSession (handoff_send.go) seals the events once under a random
//      content key and wraps that key to EVERY active device key of the
//      recipient (pinned v2 construction, handoff_crypto.go).
//   3. Inbox: Inbox() returns items shared with the current user.
//   4. AcceptShare: downloads one item, unwraps the content key with THIS
//      device's key (or, for a legacy v1 "direct" item, derives the v1
//      key), decrypts the events and returns them for the caller to persist.
//
// Key directory: fleet is a trusted key directory (contract §10.3 trust
// model): GET /api/v1/identity/public-key returns every active device key
// of a teammate. Device keys are registered at enroll (device_keys.go).
//
// Privacy invariant: session content is NEVER logged. The sender's decrypted
// events are only in memory; they are re-encrypted before being transmitted.
// Audit emits only opaque IDs (session_id, recipient_user_id, inbox_item_id).

import (
	"context"
	"crypto/ecdh"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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

	if err := h.client.endpointUnsupported(FeatureTeamMembers); err != nil {
		return nil, err
	}
	const teamMembersPath = "/api/v1/team/members"
	// Fleet paginates the roster out-of-band (kenaz-fleet
	// handlers_team_members.go): the body stays a BARE array, ?limit= (max
	// 1000) sizes a page, and X-Next-Cursor carries the next page's cursor
	// (absent on the last page). Follow it until absent. The server already
	// excludes the caller — no client-side filtering.
	var members []TeamMember
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < teamMembersMaxPages; page++ {
		q := url.Values{}
		q.Set("limit", strconv.Itoa(teamMembersPageLimit))
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		resp, err := h.client.Get(ctx, teamMembersPath+"?"+q.Encode())
		if err != nil {
			return nil, fmt.Errorf("fleet: list team: %w", err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if isPlainNotFound(resp.StatusCode, resp.Header.Get("Content-Type"), body) {
			return nil, h.client.markEndpointUnsupported(FeatureTeamMembers, teamMembersPath)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fleet: list team: status %d", resp.StatusCode)
		}
		var pageMembers []TeamMember
		if err := json.Unmarshal(body, &pageMembers); err != nil {
			return nil, fmt.Errorf("fleet: list team: parse: %w", err)
		}
		members = append(members, pageMembers...)
		next := strings.TrimSpace(resp.Header.Get(teamMembersCursorHeader))
		if next == "" || seen[next] {
			return members, nil // last page (or a server repeating a cursor)
		}
		seen[next] = true
		cursor = next
	}
	return members, fmt.Errorf("fleet: list team: more than %d pages", teamMembersMaxPages)
}

// Roster pagination (kenaz-fleet handlers_team_members.go).
const (
	teamMembersPageLimit    = 500
	teamMembersMaxPages     = 100
	teamMembersCursorHeader = "X-Next-Cursor"
)

// ShareSession (v2 send) lives in handoff_send.go.

// Inbox returns sessions shared with the current user.
func (h *HandoffHandler) Inbox(ctx context.Context) ([]InboxItem, error) {
	if h.client == nil || h.client.isNop {
		return nil, ErrFleetDisabled
	}

	if err := h.client.endpointUnsupported(FeatureTeamHandoff); err != nil {
		return nil, err
	}
	const inboxPath = "/api/v1/handoff/inbox"
	resp, err := h.client.Get(ctx, inboxPath)
	if err != nil {
		return nil, fmt.Errorf("fleet: inbox: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if isPlainNotFound(resp.StatusCode, resp.Header.Get("Content-Type"), body) {
		return nil, h.client.markEndpointUnsupported(FeatureTeamHandoff, inboxPath)
	}
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

	if err := h.client.endpointUnsupported(FeatureTeamHandoff); err != nil {
		return nil, err
	}
	resp, err := h.client.Get(ctx, "/api/v1/handoff/"+url.PathEscape(inboxItemID))
	if err != nil {
		return nil, fmt.Errorf("fleet: accept share: GET: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if isPlainNotFound(resp.StatusCode, resp.Header.Get("Content-Type"), body) {
		return nil, h.client.markEndpointUnsupported(FeatureTeamHandoff, "/api/v1/handoff/{id}")
	}
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

// publicKeyEntry is one active handoff device key of a user (fleet
// contract §10.2 public_keys[]). PublicKey is base64 std on the wire
// (fleet encodes []byte), decoded by encoding/json.
type publicKeyEntry struct {
	KeyID       string    `json:"key_id"`
	NodeID      string    `json:"node_id"`
	PublicKey   []byte    `json:"public_key"`
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"created_at"`
}

// publicKeyResponse is GET /api/v1/identity/public-key: the newest active
// key at top level (v1 back-compat) plus public_keys[] — EVERY active
// device key, which a v2 sender must wrap to.
type publicKeyResponse struct {
	UserID      string           `json:"user_id"`
	PublicKey   []byte           `json:"public_key"`
	Fingerprint string           `json:"fingerprint"`
	PublicKeys  []publicKeyEntry `json:"public_keys"`
}

// RecipientDevice is one receiving device of a teammate, for the share
// dialog's trust display (fleet is a trusted key directory: show
// fingerprints). No key bytes leave core/fleet.
type RecipientDevice struct {
	KeyID       string
	Fingerprint string
	CreatedAt   time.Time
}

// ErrHandoffRecipientKeyInvalid means fleet returned a key entry that is
// not a usable 32-byte X25519 key or whose fingerprint does not match its
// bytes — refused rather than encrypted to.
var ErrHandoffRecipientKeyInvalid = errors.New("fleet: handoff recipient key invalid")

// validateKeySet checks every entry: 32-byte X25519 point, non-empty
// key_id, and fingerprint == sha256:<hex> of the key (fleet computes it the
// same way — a mismatch means a corrupted directory answer).
func validateKeySet(keys []publicKeyEntry) error {
	seen := map[string]bool{}
	for _, k := range keys {
		if k.KeyID == "" || seen[k.KeyID] {
			return fmt.Errorf("%w: missing or duplicate key_id", ErrHandoffRecipientKeyInvalid)
		}
		seen[k.KeyID] = true
		if _, err := ecdh.X25519().NewPublicKey(k.PublicKey); err != nil {
			return fmt.Errorf("%w: %v", ErrHandoffRecipientKeyInvalid, err)
		}
		if k.Fingerprint != "" && k.Fingerprint != KeyFingerprint(k.PublicKey) {
			return fmt.Errorf("%w: fingerprint mismatch", ErrHandoffRecipientKeyInvalid)
		}
	}
	return nil
}

// fetchRecipientKeys retrieves the recipient's FULL active handoff key set
// (device-keys-handoff-01DEVKH01 FR-3). The uniform JSON 404
// public_key_not_found (missing / other-org / suspended / keyless user)
// stays ErrHandoffRecipientNotFound; a plain mux 404 latches the route as
// unsupported. When public_keys is absent (pre-#182 fleet) the top-level
// key is returned with an empty KeyID — usable by nothing in v2, so the
// caller reports it as not receivable.
func (h *HandoffHandler) fetchRecipientKeys(ctx context.Context, recipientUserID string) ([]publicKeyEntry, error) {
	if err := h.client.endpointUnsupported(FeatureIdentityPublicKey); err != nil {
		return nil, err
	}
	const publicKeyPath = "/api/v1/identity/public-key"
	resp, err := h.client.Get(ctx, publicKeyPath+"?user_id="+url.QueryEscape(recipientUserID))
	if err != nil {
		return nil, fmt.Errorf("fleet: fetch public key: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	// A plain mux 404 means the ROUTE does not exist (latch); a JSON 404
	// is the application saying this recipient has no key.
	if isPlainNotFound(resp.StatusCode, resp.Header.Get("Content-Type"), body) {
		return nil, h.client.markEndpointUnsupported(FeatureIdentityPublicKey, publicKeyPath)
	}
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
	keys := pkResp.PublicKeys
	if keys == nil && len(pkResp.PublicKey) > 0 {
		keys = []publicKeyEntry{{PublicKey: pkResp.PublicKey, Fingerprint: pkResp.Fingerprint}}
	}
	if len(keys) == 0 {
		return nil, ErrHandoffRecipientNotFound
	}
	return keys, nil
}

// RecipientDevices lists the teammate's receiving devices (key ids +
// fingerprints) for the share dialog. ErrHandoffRecipientNotFound when the
// teammate has no active key.
func (h *HandoffHandler) RecipientDevices(ctx context.Context, recipientUserID string) ([]RecipientDevice, error) {
	if h.client == nil || h.client.isNop {
		return nil, ErrFleetDisabled
	}
	keys, err := h.fetchRecipientKeys(ctx, recipientUserID)
	if err != nil {
		return nil, err
	}
	out := make([]RecipientDevice, 0, len(keys))
	for _, k := range keys {
		fp := k.Fingerprint
		if fp == "" {
			fp = KeyFingerprint(k.PublicKey)
		}
		out = append(out, RecipientDevice{KeyID: k.KeyID, Fingerprint: fp, CreatedAt: k.CreatedAt})
	}
	return out, nil
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
	// Load our per-device (seed + node id) X25519 private key.
	recipientPriv, err := LoadOwnHandoffPrivKey(h.client.dataDir)
	if err != nil {
		return nil, fmt.Errorf("load own handoff private key: %w", err)
	}
	// X25519 ECDH: recipientPriv · ephemeralPub == ephemeralPriv · recipientPub,
	// then the same HKDF step as deriveHandoffKey.
	return deriveV1DirectKey(recipientPriv, ephemeralPubKeyBytes)
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
