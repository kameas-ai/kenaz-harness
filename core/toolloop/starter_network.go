package toolloop

import (
	"context"
	"sync/atomic"
)

// Host starter policy, network leg (launch spec L09 D1, Option B on the
// host): with the starter policy active, the harness's built-in network
// tools ask before they run. Reads and in-workspace edits are the fs
// gate's half (core/tools/fs GateOptions.StarterDefaults); shell commands
// already ask for every pattern without a grant.

// StarterNetworkTools are the (server, tool) pairs the starter policy
// treats as network calls: the built-in web fetch and web search tools.
// MCP servers the user installs make their own network calls out of
// process; their tools keep whatever per-tool policy the user set.
var StarterNetworkTools = map[[2]string]bool{
	{BuiltinServerName, "web_fetch"}:  true,
	{BuiltinServerName, "web_search"}: true,
}

// StarterNetworkReason is the confirmation prompt's reason line.
const StarterNetworkReason = "Starter policy: network calls ask first. Change it in Settings → Permissions."

// StarterNetworkResolver wraps a PermissionResolver. While Active reports
// true, an auto_allow verdict for a StarterNetworkTools pair becomes
// confirm_each with AskDespitePermit set. deny and confirm_each verdicts
// pass through unchanged, so a user's or the org's stricter rule always
// wins. With Active unset or false it is the inner resolver, byte for
// byte.
type StarterNetworkResolver struct {
	inner  PermissionResolver
	active atomic.Pointer[func() bool]
}

// NewStarterNetworkResolver wraps inner. Inactive until SetActive.
func NewStarterNetworkResolver(inner PermissionResolver) *StarterNetworkResolver {
	return &StarterNetworkResolver{inner: inner}
}

// SetActive installs the live "is the starter policy on" reader. It is
// read on every Resolve, so accepting or leaving the starter policy takes
// effect on the next tool call. nil turns the wrapper off.
func (r *StarterNetworkResolver) SetActive(fn func() bool) {
	if fn == nil {
		r.active.Store(nil)
		return
	}
	r.active.Store(&fn)
}

// Resolve implements PermissionResolver.
func (r *StarterNetworkResolver) Resolve(ctx context.Context, sessionID, server, tool string) (Resolution, error) {
	var res Resolution
	if r.inner == nil {
		res = Resolution{Server: server, Tool: tool, Policy: PolicyAutoAllow}
	} else {
		var err error
		res, err = r.inner.Resolve(ctx, sessionID, server, tool)
		if err != nil {
			return res, err
		}
	}
	if res.Policy != PolicyAutoAllow || !StarterNetworkTools[[2]string{server, tool}] {
		return res, nil
	}
	fn := r.active.Load()
	if fn == nil || !(*fn)() {
		return res, nil
	}
	res.Policy = PolicyConfirmEach
	res.AskDespitePermit = true
	if res.Reason == "" {
		res.Reason = StarterNetworkReason
	}
	return res, nil
}
