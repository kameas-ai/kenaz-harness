// Package policy is a v1 stub of the policy-engine-01KQ1A3N surface that
// the ACP orchestration mission consumes for inbound and outbound gates.
//
// Per plan §6.5: the v1 stub is a no-op `AllowAll` guard. When the
// real policy engine lands, it implements the same Guard interface.
//
// CI lint catches lingering references to the no-op default before
// release per plan §6.5 / §8 R10.
package policy

import "context"

// Decision is the gate outcome.
type Decision int

const (
	// Allow lets the request proceed.
	Allow Decision = iota
	// Deny refuses the request with a typed reason.
	Deny
)

// Direction indicates whether a gate is for inbound or outbound traffic.
type Direction string

const (
	DirectionInbound  Direction = "inbound"
	DirectionOutbound Direction = "outbound"
)

// Request describes the call being gated.
type Request struct {
	Direction Direction
	PeerID    string
	SkillID   string
	AgentID   string
}

// Result is what a Guard returns.
type Result struct {
	Decision Decision
	Reason   string
}

// Guard is the policy gate interface consumed by core/acp/{client,server}/.
type Guard interface {
	Allow(ctx context.Context, req Request) Result
}

// AllowAll is the v1 no-op guard. TODO: policy-engine — replace before
// release.
type AllowAll struct{}

// Allow returns Allow for every request.
func (AllowAll) Allow(_ context.Context, _ Request) Result {
	return Result{Decision: Allow}
}

// DenyList is a deterministic test/development guard that denies a
// configured set of (Direction, PeerID, SkillID) tuples and allows
// everything else.
type DenyList struct {
	Denied map[string]string // key = Direction|PeerID|SkillID, value = reason
}

// Allow checks the deny set.
func (d DenyList) Allow(_ context.Context, req Request) Result {
	if d.Denied == nil {
		return Result{Decision: Allow}
	}
	key := string(req.Direction) + "|" + req.PeerID + "|" + req.SkillID
	if reason, ok := d.Denied[key]; ok {
		return Result{Decision: Deny, Reason: reason}
	}
	// Also support direction-only deny (peer agnostic).
	dirKey := string(req.Direction) + "|*|*"
	if reason, ok := d.Denied[dirKey]; ok {
		return Result{Decision: Deny, Reason: reason}
	}
	return Result{Decision: Allow}
}
