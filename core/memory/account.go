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
//	AccountUnknown     a rewrite of an unstamped note; keeps its unknown owner
//	""                 made before this field existed
//
// The last two are "unstamped": nobody can say who made them, so only the
// account the device recorded first (AccountScope.LegacyOwner) may upload
// them. They stay on the device for every other account.
const (
	AccountSignedOut = "signed-out"
	AccountUnknown   = "unknown"
)

// AccountUnstamped reports whether the chunk's owner can't be known.
func (c Chunk) AccountUnstamped() bool {
	return c.Account == "" || c.Account == AccountUnknown
}

// AccountScope is the account a sync operation acts for. The zero value owns
// nothing, so a forgotten scope fails closed.
type AccountScope struct {
	Account     string
	LegacyOwner string
}

// Owns reports whether sync acting for this account may touch c.
func (s AccountScope) Owns(c Chunk) bool {
	if s.Account == "" {
		return false
	}
	if c.AccountUnstamped() {
		return s.LegacyOwner == s.Account
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
