package fleet

// memory_sync_account.go: whose memory the lane is syncing (legal D164).
//
// The sync state, the note store and the forget queue all live per device
// profile, and a profile can be used by more than one account. An account is
// only trusted when the identity file and the token agree: enroll records which
// token it enrolled with, and each request checks the token it is about to send
// against that record at the moment it is attached. Desktop and served mode go
// through the same check, so a mismatch or an unreadable token sends nothing.

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

// CurrentAccountKey is who a new note belongs to for the profile at dataDir: the
// confirmed account; "" with no tokens (signed out); the token's pending stamp
// while it can't be tied to an account yet, as before a sign-in's enroll answers
// or while enroll keeps failing; or memory.AccountUnresolved if the token can't
// be read. Nobody can upload an unresolved note, which is the safe way to fail.
func CurrentAccountKey(dataDir string) string {
	tok, err := accessTokenStrict()
	if err != nil {
		return memory.AccountUnresolved
	}
	if tok == "" {
		return ""
	}
	key := tokenKeyOf(tok)
	if key == "" {
		return memory.AccountUnresolved
	}
	id, err := LoadIdentity(dataDir)
	if err != nil || AccountKey(id) == "" || id.TokenKey != key {
		return memory.PendingAccount(key)
	}
	return AccountKey(id)
}

// verifiedIdentity is the signed-in account, only if the identity was enrolled
// with the current token.
func (m *MemorySync) verifiedIdentity() (Identity, bool) {
	id, err := LoadIdentity(m.cfg.DataDir)
	if err != nil || AccountKey(id) == "" || id.TokenKey == "" || id.TokenKey != TokenIdentityKey() {
		return Identity{}, false
	}
	return id, true
}

func (m *MemorySync) currentAccount() string {
	id, ok := m.verifiedIdentity()
	if !ok {
		return ""
	}
	return AccountKey(id)
}

type requiredTokenKeyCtx struct{}

// requireTokenKey makes Client refuse to send under any other account's token.
// Checked where the token is attached, so a sign-in landing between this
// request's account check and its send can't slip through.
func requireTokenKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, requiredTokenKeyCtx{}, key)
}

func requiredTokenKey(ctx context.Context) string {
	key, _ := ctx.Value(requiredTokenKeyCtx{}).(string)
	return key
}

// accountLabel names the signed-in account for the one-time line.
func (m *MemorySync) accountLabel() string {
	id, ok := m.verifiedIdentity()
	switch {
	case !ok:
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
	m.mu.Lock()
	defer m.mu.Unlock()
	return memory.AccountScope{Account: m.st.Account, LegacyOwner: m.st.LegacyOwner, TokenKey: m.tokenKey}
}

// ensureAccount makes the sync state the signed-in account's, parking the
// previous account's so it resumes when they return. A new account starts with
// sync off and an empty cursor. The first account ever seen keeps the state it
// finds and becomes the legacy owner (D164). The caller holds cycleMu.
func (m *MemorySync) ensureAccount() (memory.AccountScope, error) {
	id, ok := m.verifiedIdentity()
	if !ok {
		return memory.AccountScope{}, errNoAccount
	}
	key := AccountKey(id)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokenKey = id.TokenKey
	if m.st.Account == key {
		return memory.AccountScope{Account: key, LegacyOwner: m.st.LegacyOwner, TokenKey: id.TokenKey}, nil
	}
	prev := m.st
	if m.st.Account == "" {
		m.st.Account, m.st.LegacyOwner = key, key
		// An erase left unfinished by an earlier version can't be tied to this
		// account, and it can't be undone, so it isn't run on its behalf. It is
		// offered instead, once. The opt-out half does run, and says so.
		m.st.InheritedErase = m.st.ForgetAllPending
		m.st.InheritedOptOut = m.st.DisablePending
		m.st.ForgetAllPending = false
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
	return memory.AccountScope{Account: key, LegacyOwner: m.st.LegacyOwner, TokenKey: id.TokenKey}, nil
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
