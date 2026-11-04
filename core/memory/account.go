package memory

// Account ownership of notes (legal D164): a device profile can be used by
// more than one Fleet account, and memory sync must never carry one
// account's notes or forgets to another.
//
// A chunk's Account is set once, when the chunk is created, and never changes
// afterwards. Its values:
//
//	"<org>:<user>"     made (or pulled) while that account was signed in
//	AccountSignedOut   made while no account was signed in; stays on this
//	                   device and never uploads
//	"pending:<token>"  made with a token whose account wasn't confirmed yet;
//	                   belongs to whichever account that token is confirmed as
//	AccountUnresolved  the token couldn't be read; stays on this device
//	AccountUnknown     a rewrite of an unstamped note; keeps its unknown owner
//	""                 made before this field existed
//
// The last two are "unstamped": nobody can say who made them, so only the
// account the device recorded first (AccountScope.LegacyOwner) may upload
// them. They stay on the device for every other account. A note made during an
// account switch is never unstamped: its token says whose it is.
const (
	AccountSignedOut  = "signed-out"
	AccountUnresolved = "unresolved"
	AccountUnknown    = "unknown"

	accountPendingPrefix = "pending:"
)

// PendingAccount is the stamp for a note made with this token before its
// account was confirmed.
func PendingAccount(tokenKey string) string {
	return accountPendingPrefix + tokenKey
}

// AccountUnstamped reports whether the chunk's owner can't be known.
func (c Chunk) AccountUnstamped() bool {
	return c.Account == "" || c.Account == AccountUnknown
}

// AccountScope is the account a sync operation acts for. The zero value owns
// nothing, so a forgotten scope fails closed.
type AccountScope struct {
	Account     string
	LegacyOwner string
	// TokenKey is the token the account was confirmed with: it claims the
	// notes that token wrote before the confirmation.
	TokenKey string
}

// Owns reports whether sync acting for this account may touch c.
func (s AccountScope) Owns(c Chunk) bool {
	if s.Account == "" {
		return false
	}
	if c.AccountUnstamped() {
		return s.LegacyOwner == s.Account
	}
	if s.TokenKey != "" && c.Account == PendingAccount(s.TokenKey) {
		return true
	}
	return c.Account == s.Account
}

// OwnsForget reports whether a queued forget may be sent for this account.
func (s AccountScope) OwnsForget(op ForgetOp) bool {
	return s.Owns(Chunk{Account: op.Account})
}

// AccountStamper is the optional store capability the rpc wiring uses to
// install the source of the signed-in account.
type AccountStamper interface {
	SetAccountSource(fn func() string)
}
