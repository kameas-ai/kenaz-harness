package fleet

// device_keys_refused.go — persists the sticky reauth_refused key-registration
// state across restarts (device_keys_stepup.go KeyRegReauthRefused).
//
// reauth_refused is held on the Client, so without this a restart forgot it:
// the first tokenless enroll then recorded plain reauth_required and an
// SSO-only member saw "Sign in again" once more before the admin message.
//
// The file <dataDir>/fleet/key_reauth_refused.json (0600) holds only the
// account it belongs to (the access-token sub) and when it was recorded —
// never a token. It is read once per Client, applied only to that same
// account, and removed on a successful key registration or when it belongs
// to a different account than the one signed in.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

const reauthRefusedFileName = "key_reauth_refused.json"

type persistedReauthRefusal struct {
	Sub string    `json:"sub"`
	At  time.Time `json:"at"`
}

func reauthRefusedPath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", reauthRefusedFileName)
}

// ReauthRefusedPath is the persisted refusal file (removed on sign-out).
func ReauthRefusedPath(dataDir string) string { return reauthRefusedPath(dataDir) }

// ContextSharesPath is the contexts view's share ledger file (where this
// device last shared each Curated entry); here so sign-out can remove it.
func ContextSharesPath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "context_shares.json")
}

// AccountScopedFiles are the per-account local records sign-out removes:
// the share ledger, the pulled location index and the step-up refusal.
func AccountScopedFiles(dataDir string) []string {
	return []string{ContextSharesPath(dataDir), PulledLocationsPath(dataDir), ReauthRefusedPath(dataDir)}
}

func readReauthRefusal(dataDir string) (persistedReauthRefusal, bool) {
	b, err := os.ReadFile(reauthRefusedPath(dataDir))
	if err != nil {
		return persistedReauthRefusal{}, false
	}
	var r persistedReauthRefusal
	if json.Unmarshal(b, &r) != nil || r.Sub == "" {
		// Unreadable: it can only ever mean "unknown"; drop it.
		_ = os.Remove(reauthRefusedPath(dataDir))
		return persistedReauthRefusal{}, false
	}
	return r, true
}

// loadPersistedRefusal seeds the in-memory state from disk, once per Client,
// when nothing has been recorded this process yet.
func (c *Client) loadPersistedRefusal() {
	if c == nil || c.dataDir == "" {
		return
	}
	c.keyReg.loadOnce.Do(func() {
		r, ok := readReauthRefusal(c.dataDir)
		if !ok {
			return
		}
		if cur := currentSubject(); cur != "" && cur != r.Sub {
			_ = os.Remove(reauthRefusedPath(c.dataDir))
			return
		}
		c.keyReg.mu.Lock()
		defer c.keyReg.mu.Unlock()
		if c.keyReg.last.Status == "" {
			c.keyReg.last = KeyRegistration{Status: KeyRegReauthRefused, Message: keyRegRefusedCopy, At: r.At}
			c.keyReg.refusedFor = r.Sub
		}
	})
}

// persistReauthRefused records reauth_refused for sub on disk.
func (c *Client) persistReauthRefused(sub string, at time.Time) {
	if c == nil || c.dataDir == "" || sub == "" {
		return
	}
	if err := os.MkdirAll(filepath.Join(c.dataDir, "fleet"), 0o700); err != nil {
		logging.L().Warn("fleet.device_keys.refused_persist_failed", "err", err.Error())
		return
	}
	b, err := json.Marshal(persistedReauthRefusal{Sub: sub, At: at})
	if err != nil {
		return
	}
	if err := atomicWriteFile(reauthRefusedPath(c.dataDir), string(b)); err != nil {
		logging.L().Warn("fleet.device_keys.refused_persist_failed", "err", err.Error())
	}
}

// setKeyReg records a non-refused outcome. A successful registration clears
// the persisted refusal; so does any outcome recorded while signed in as a
// different account than the one the refusal belongs to.
func (c *Client) setKeyReg(r KeyRegistration) {
	c.loadPersistedRefusal()
	c.keyReg.set(r)
	if c.dataDir == "" {
		return
	}
	if r.Status == KeyRegRegistered {
		_ = os.Remove(reauthRefusedPath(c.dataDir))
		return
	}
	if p, ok := readReauthRefusal(c.dataDir); ok {
		if cur := currentSubject(); cur != "" && cur != p.Sub {
			_ = os.Remove(reauthRefusedPath(c.dataDir))
		}
	}
}
