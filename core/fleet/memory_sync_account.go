package fleet

// memory_sync_account.go: whose memory the lane is syncing (legal D164).
//
// The sync state, the note store and the forget queue all live per device
// profile, and a profile can be used by more than one account. Everything the
// lane sends is scoped to the account the identity file names, and that file
// is the single answer to "who is signed in": sign-in removes it before saving
// new tokens (settings runSignIn) and enroll writes it back, so "no identity"
// is what the window between two accounts looks like, and the lane sends
// nothing during it.

import (
	"context"
	"errors"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/memory"
)

// errNoAccount: the signed-in account isn't known, or isn't the one the sync
// state belongs to. Nothing may be sent.
var errNoAccount = errors.New("fleet: memory sync: not signed in")

// AccountKey is the identity memory ownership is keyed on, "" when either half
// is missing. Fleet keeps memory per org and user, so the same person in two
// orgs is two accounts here too.
func AccountKey(id Identity) string {
	if id.OrgID == "" || id.UserID == "" {
		return ""
	}
	return id.OrgID + ":" + id.UserID
}

// CurrentAccountKey is the signed-in account for the profile at dataDir, ""
// when there is none. The memory store stamps new notes with it.
func CurrentAccountKey(dataDir string) string {
	id, err := LoadIdentity(dataDir)
	if err != nil {
		return ""
	}
	return AccountKey(id)
}

func (m *MemorySync) identity() (Identity, error) {
	if m.cfg.Identity != nil {
		return m.cfg.Identity()
	}
	return LoadIdentity(m.cfg.DataDir)
}

func (m *MemorySync) currentAccount() string {
	id, err := m.identity()
	if err != nil {
		return ""
	}
	return AccountKey(id)
}

// accountLabel names the signed-in account for the one-time line.
func (m *MemorySync) accountLabel() string {
	id, err := m.identity()
	switch {
	case err != nil:
		return "this account"
	case id.Email != "":
		return id.Email
	case id.DisplayName != "":
		return id.DisplayName
	}
	return "this account"
}

// scope is the account the current sync state acts for.
func (m *MemorySync) scope() memory.AccountScope {
	st := m.state()
	return memory.AccountScope{Account: st.Account, LegacyOwner: st.LegacyOwner}
}

// ensureAccount makes the sync state the signed-in account's, parking the
// previous account's so it resumes when they return. A new account starts with
// sync off and an empty cursor. The first account ever seen keeps the state it
// finds and becomes the legacy owner (D164). The caller holds cycleMu.
func (m *MemorySync) ensureAccount() (memory.AccountScope, error) {
	key := m.currentAccount()
	if key == "" {
		return memory.AccountScope{}, errNoAccount
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.st.Account == key {
		return memory.AccountScope{Account: key, LegacyOwner: m.st.LegacyOwner}, nil
	}
	prev := m.st
	if m.st.Account == "" {
		m.st.Account, m.st.LegacyOwner = key, key
	} else {
		parked := make(map[string]memSyncAccountState, len(prev.Parked)+1)
		for k, v := range prev.Parked {
			parked[k] = v
		}
		parked[prev.Account] = prev.memSyncAccountState
		m.st.memSyncAccountState = parked[key]
		delete(parked, key)
		m.st.Account, m.st.Parked = key, parked
	}
	if err := m.saveStateLocked(); err != nil {
		m.st = prev
		return memory.AccountScope{}, err
	}
	m.settings, m.settingsAt = nil, m.now()
	m.nextAttempt, m.failures = m.now(), 0
	m.skipped = map[string]int64{}
	logging.L().Info("fleet.memory_sync.account_recorded", "first", prev.Account == "")
	return memory.AccountScope{Account: key, LegacyOwner: m.st.LegacyOwner}, nil
}

// ContinueOlderNotes records that the legacy owner continued past the one-time
// line, so unstamped notes may upload. Refused for anyone else.
func (m *MemorySync) ContinueOlderNotes(_ context.Context) error {
	m.cycleMu.Lock()
	defer m.cycleMu.Unlock()
	scope, err := m.ensureAccount()
	if err != nil {
		return err
	}
	if scope.Account != scope.LegacyOwner {
		return errors.New("fleet: memory sync: these notes belong to another account")
	}
	if err := m.update(func(s *memSyncState) { s.OlderNotesAck = true }); err != nil {
		return err
	}
	m.Kick()
	return nil
}
