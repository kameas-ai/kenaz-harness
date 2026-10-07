package fleet

// device_keys.go — per-device public key registration
// (device-keys-handoff-01DEVKH01 WP02; kenaz-fleet contract §10.1).
//
//	POST /api/v1/enroll                 optional handoff_public_key + signing_public_key,
//	                                    registered in the SAME transaction as the node upsert
//	PUT  /api/v1/me/nodes/{node_id}/keys same body; rotation without a full enroll
//	                                    (recovery-code seed import)
//	DELETE /api/v1/me/nodes/{node_id}   self-unenroll: revokes this node's keys
//
// Key material:
//   - handoff: X25519, derived per device from the context seed + node id
//     (context_crypto.go DeriveHandoffPrivKey). Enroll mints the seed when
//     absent (OQ-4) so every enrolled device can receive handoffs.
//   - signing: the random per-device ed25519 key (signing.go), unchanged;
//     registering it makes audit batches verified (fleet §8.3).
//
// Fleet answers (2026-10-07, binding): enroll-with-keys is ATOMIC — a bad
// key (400 invalid_public_key) or a 17th active handoff key (409
// too_many_device_keys) fails the WHOLE enroll. enrollIdentity therefore
// retries WITHOUT keys so the device still enrolls, and records why it
// cannot receive handoffs (KeyRegistration). Identical key = no-op,
// different key = rotation. Dormant devices keep their keys and count
// toward the 16-key cap until unenrolled — hence self-unenroll on explicit
// sign-out (UnenrollNode).
//
// Privacy: only PUBLIC keys and fingerprints leave this file. Private key
// bytes and the seed are never logged.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// Key registration outcomes surfaced to the session snapshot.
const (
	// KeyRegRegistered: the device's keys were accepted (registered,
	// rotated or an identical no-op).
	KeyRegRegistered = "registered"
	// KeyRegTooManyDevices: fleet refused a 17th active handoff key; the
	// device enrolled without keys and cannot receive handoffs.
	KeyRegTooManyDevices = "too_many_devices"
	// KeyRegInvalidKey: fleet rejected a key as invalid (400
	// invalid_public_key); the device enrolled without keys.
	KeyRegInvalidKey = "invalid_key"
	// KeyRegUnavailable: no key material could be produced locally (OS
	// keychain unavailable, no data dir); enrolled without keys.
	KeyRegUnavailable = "unavailable"
)

// Human copy for the non-registered outcomes (the share/receive surfaces
// show these verbatim).
const (
	keyRegTooManyCopy  = "This device can't receive shared sessions: your account has too many devices registered. Remove an old device in the fleet dashboard, then sign in again."
	keyRegInvalidCopy  = "This device can't receive shared sessions: fleet rejected its device key. Sign out and back in to retry."
	keyRegUnavailCopy  = "This device can't receive shared sessions: its device key could not be created (OS keychain unavailable)."
	keyRegNodeGoneCopy = "This device was removed by an org admin."
)

// KeyRegistration is the most recent device-key registration outcome.
// Zero value = never attempted this process.
type KeyRegistration struct {
	Status string
	// Message is human copy for a non-registered status; empty otherwise.
	Message string
	// HandoffFingerprint is sha256:<hex> of the registered handoff key
	// (shown so a teammate can compare fingerprints — fleet trust model).
	HandoffFingerprint string
	SigningFingerprint string
	At                 time.Time
}

type keyRegState struct {
	mu   sync.Mutex
	last KeyRegistration
}

func (s *keyRegState) set(r KeyRegistration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.At = time.Now().UTC()
	s.last = r
}

func (s *keyRegState) get() KeyRegistration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

// KeyRegistration returns the outcome of this device's most recent key
// registration (zero value when none was attempted this process).
func (c *Client) KeyRegistration() KeyRegistration {
	if c == nil {
		return KeyRegistration{}
	}
	return c.keyReg.get()
}

// ErrNodeRemoved is returned by enroll and PUT keys when fleet answers 403
// node_removed: an org admin removed this (user, node_id). TERMINAL for this
// node id — the device must stop enrolling, sign out and show "removed by
// admin"; re-enroll works only under a NEW node id after a fresh sign-in
// (fleet contract §10.1).
var ErrNodeRemoved = errors.New("fleet: this device was removed by an org admin")

// ErrDeviceKeysRejected wraps a 400 invalid_public_key / 409
// too_many_device_keys from PUT keys (enroll handles them by retrying
// without keys). Neither signs the device out.
var ErrDeviceKeysRejected = errors.New("fleet: device keys rejected")

// deviceKeysBody is the key half of the enroll body and the whole PUT body.
type deviceKeysBody struct {
	HandoffPublicKey string `json:"handoff_public_key,omitempty"`
	SigningPublicKey string `json:"signing_public_key,omitempty"`
}

func (b deviceKeysBody) empty() bool { return b.HandoffPublicKey == "" && b.SigningPublicKey == "" }

// localKeyMaterial is the device's public keys plus fingerprints.
type localKeyMaterial struct {
	body               deviceKeysBody
	handoffFingerprint string
	signingFingerprint string
}

// localDeviceKeys assembles this device's public keys for nodeID. It mints
// the context seed when absent (OQ-4: every enrolled device registers a
// handoff key, else can_receive stays false for users who never toggled
// sync). Either key may be missing when its source failed; the error
// reports the handoff-key failure, if any.
func (c *Client) localDeviceKeys(nodeID string) (localKeyMaterial, error) {
	var out localKeyMaterial
	if c == nil || c.dataDir == "" || nodeID == "" {
		return out, ErrHandoffIdentityUnavailable
	}
	if s, err := NewDeviceSigner(c.dataDir); err == nil {
		pub := s.PublicKey()
		out.body.SigningPublicKey = base64.StdEncoding.EncodeToString(pub)
		out.signingFingerprint = KeyFingerprint(pub)
	} else {
		logging.L().Warn("fleet.device_keys.signing_key_unavailable", "err", err.Error())
	}
	seed, err := SeedKey()
	if err != nil {
		return out, fmt.Errorf("fleet: device keys: context seed: %w", err)
	}
	priv, err := DeriveHandoffPrivKey(seed, nodeID)
	if err != nil {
		return out, err
	}
	pub := priv.PublicKey().Bytes()
	out.body.HandoffPublicKey = base64.StdEncoding.EncodeToString(pub)
	out.handoffFingerprint = KeyFingerprint(pub)
	return out, nil
}

// fleetErrorEnvelope is fleet's JSON error shape {code, message, details?}.
type fleetErrorEnvelope struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func parseFleetError(body []byte) (fleetErrorEnvelope, bool) {
	var env fleetErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil || env.Code == "" {
		return fleetErrorEnvelope{}, false
	}
	return env, true
}

// keyRejection classifies an enroll / PUT keys response as a key-material
// rejection (status code, KeyReg* outcome) or not.
func keyRejection(status int, body []byte) (string, bool) {
	env, ok := parseFleetError(body)
	if !ok {
		return "", false
	}
	switch {
	case status == http.StatusConflict && env.Code == "too_many_device_keys":
		return KeyRegTooManyDevices, true
	case status == http.StatusBadRequest && env.Code == "invalid_public_key":
		return KeyRegInvalidKey, true
	}
	return "", false
}

func keyRegCopy(status string) string {
	switch status {
	case KeyRegTooManyDevices:
		return keyRegTooManyCopy
	case KeyRegInvalidKey:
		return keyRegInvalidCopy
	case KeyRegUnavailable:
		return keyRegUnavailCopy
	}
	return ""
}

// isNodeRemoved reports a 403 node_removed envelope.
func isNodeRemoved(status int, body []byte) bool {
	if status != http.StatusForbidden {
		return false
	}
	env, ok := parseFleetError(body)
	return ok && env.Code == "node_removed"
}

// RegisterDeviceKeys re-registers this device's keys for nodeID via
// PUT /api/v1/me/nodes/{node_id}/keys — rotation without a full enroll.
// Called after a recovery-code seed import (the derived handoff key
// changed). Returns ErrNodeRemoved on 403 node_removed and
// ErrDeviceKeysRejected (wrapped, with human copy) on 400/409.
func (c *Client) RegisterDeviceKeys(ctx context.Context, nodeID string) error {
	if c == nil || c.isNop {
		return ErrFleetDisabled
	}
	km, kerr := c.localDeviceKeys(nodeID)
	if km.body.empty() {
		c.keyReg.set(KeyRegistration{Status: KeyRegUnavailable, Message: keyRegUnavailCopy})
		if kerr == nil {
			kerr = ErrHandoffIdentityUnavailable
		}
		return kerr
	}
	data, err := json.Marshal(km.body)
	if err != nil {
		return fmt.Errorf("fleet: put device keys: marshal: %w", err)
	}
	resp, err := c.Put(ctx, "/api/v1/me/nodes/"+url.PathEscape(nodeID)+"/keys", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("fleet: put device keys: %w", err)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
		reg := KeyRegistration{Status: KeyRegRegistered, HandoffFingerprint: km.handoffFingerprint, SigningFingerprint: km.signingFingerprint}
		if kerr != nil {
			reg = KeyRegistration{Status: KeyRegUnavailable, Message: keyRegUnavailCopy, SigningFingerprint: km.signingFingerprint}
		}
		c.keyReg.set(reg)
		logging.L().Info("fleet.device_keys.registered", "via", "put", "handoff", km.body.HandoffPublicKey != "")
		return nil
	case isNodeRemoved(resp.StatusCode, body):
		c.keyReg.set(KeyRegistration{Status: "", Message: keyRegNodeGoneCopy})
		return ErrNodeRemoved
	}
	if st, ok := keyRejection(resp.StatusCode, body); ok {
		c.keyReg.set(KeyRegistration{Status: st, Message: keyRegCopy(st)})
		return fmt.Errorf("%w: %s", ErrDeviceKeysRejected, keyRegCopy(st))
	}
	if env, ok := parseFleetError(body); ok {
		return fmt.Errorf("fleet: put device keys: status %d (%s)", resp.StatusCode, env.Code)
	}
	return fmt.Errorf("fleet: put device keys: status %d", resp.StatusCode)
}

// UnenrollNode self-unenrolls nodeID (DELETE /api/v1/me/nodes/{node_id}):
// fleet revokes this node's keys so a dormant device stops counting toward
// the 16-key cap. Idempotent; 204 and 404 node_not_found are both success
// (the node is not enrolled either way). Not sticky — a later enroll
// re-registers keys.
func (c *Client) UnenrollNode(ctx context.Context, nodeID string) error {
	if c == nil || c.isNop {
		return ErrFleetDisabled
	}
	if nodeID == "" {
		return nil
	}
	resp, err := c.Delete(ctx, "/api/v1/me/nodes/"+url.PathEscape(nodeID))
	if err != nil {
		return fmt.Errorf("fleet: unenroll node: %w", err)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return nil
	}
	if env, ok := parseFleetError(body); ok && resp.StatusCode == http.StatusNotFound && env.Code == "node_not_found" {
		return nil
	}
	return fmt.Errorf("fleet: unenroll node: status %d", resp.StatusCode)
}
