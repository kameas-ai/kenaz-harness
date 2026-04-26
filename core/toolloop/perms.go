// Permission resolution for the toolloop. WP02 surface: a small
// interface plus three concrete resolvers (static config, session
// overrides, merged) that the loop consults between tool detection
// and dispatch.
//
// Match priority (shared by every resolver):
//
//	exact server + exact tool   >
//	exact server + wildcard tool>
//	wildcard server + exact tool>
//	wildcard server + wildcard  >
//	default (auto_allow)
//
// Wildcards are the literal string "*". No glob / regex support — the
// rule schema is intentionally narrow so the resolver stays predictable
// and the surface stays auditable. WP05 lands the confirm-each modal
// flow; for now confirm_each is treated as auto_allow at dispatch time
// while the resolver still surfaces the verdict for telemetry.
package toolloop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ToolPolicy is the per-(server, tool) decision the resolver returns.
type ToolPolicy string

const (
	PolicyAutoAllow   ToolPolicy = "auto_allow"
	PolicyConfirmEach ToolPolicy = "confirm_each"
	PolicyDeny        ToolPolicy = "deny"
)

// PermissionResolver maps a (sessionID, server, tool) triple to a
// concrete Resolution. Implementations must be safe for concurrent
// use — the loop calls Resolve on its hot path, potentially from the
// future concurrent-dispatch worker pool (WP04).
type PermissionResolver interface {
	Resolve(ctx context.Context, sessionID, server, tool string) (Resolution, error)
}

// permRule is one entry in a resolver's rule list. Server and Tool may
// each be "*" to match any value; the priority logic in matchRules
// disambiguates overlapping entries.
type permRule struct {
	Server string     `json:"server"`
	Tool   string     `json:"tool"`
	Policy ToolPolicy `json:"policy"`
	Reason string     `json:"reason,omitempty"`
}

// matchRules picks the highest-priority rule for (server, tool). The
// priority is: exact+exact > exact+wildcard > wildcard+exact >
// wildcard+wildcard. Returns ok=false when no rule matches; the caller
// supplies the default policy.
func matchRules(rules []permRule, server, tool string) (permRule, bool) {
	var (
		best     permRule
		bestRank int
		found    bool
	)
	for _, r := range rules {
		rank := ruleRank(r, server, tool)
		if rank == 0 {
			continue
		}
		if !found || rank > bestRank {
			best = r
			bestRank = rank
			found = true
		}
	}
	return best, found
}

// ruleRank returns 0 for non-matching rules; otherwise a positive
// integer where higher is more specific. Encoding:
//
//	4 — exact server + exact tool
//	3 — exact server + wildcard tool
//	2 — wildcard server + exact tool
//	1 — wildcard server + wildcard tool
func ruleRank(r permRule, server, tool string) int {
	srvExact := r.Server == server
	srvWild := r.Server == "*"
	toolExact := r.Tool == tool
	toolWild := r.Tool == "*"
	switch {
	case srvExact && toolExact:
		return 4
	case srvExact && toolWild:
		return 3
	case srvWild && toolExact:
		return 2
	case srvWild && toolWild:
		return 1
	}
	return 0
}

// validatePolicy returns an error for unknown policy strings so a
// malformed config surfaces at load-time, not at dispatch-time.
func validatePolicy(p ToolPolicy) error {
	switch p {
	case PolicyAutoAllow, PolicyConfirmEach, PolicyDeny:
		return nil
	}
	return fmt.Errorf("toolloop/perms: unknown policy %q", p)
}

// staticConfig is the on-disk schema for the global allow/deny rules.
// Persisted at <DataDir>/mcp_servers.json. Schema:
//
//	{
//	  "version": 1,
//	  "rules": [
//	    {"server": "filesystem", "tool": "*", "policy": "confirm_each"},
//	    {"server": "*", "tool": "exec", "policy": "deny",
//	     "reason": "shell exec disabled by policy"}
//	  ]
//	}
type staticConfig struct {
	Version int        `json:"version"`
	Rules   []permRule `json:"rules"`
}

// staticResolver reads global allow/deny rules from a JSON file on
// disk. Missing file → default policy auto_allow. Malformed file →
// the file-loading helper surfaces an error; in-memory construction
// validates each rule's Policy.
type staticResolver struct {
	rules []permRule
}

// NewStaticResolver builds a resolver from an in-memory rule slice.
// Useful for tests and for callers that load the JSON themselves.
// Each rule's Policy is validated; an unknown value yields an error.
func NewStaticResolver(rules []permRule) (PermissionResolver, error) {
	for i, r := range rules {
		if err := validatePolicy(r.Policy); err != nil {
			return nil, fmt.Errorf("toolloop/perms: rule[%d]: %w", i, err)
		}
	}
	return &staticResolver{rules: append([]permRule(nil), rules...)}, nil
}

// NewStaticResolverFromFile loads rules from <path>. Missing file is
// not an error: the returned resolver behaves as auto_allow for every
// (server, tool). Malformed JSON or unknown policy strings return an
// error so wiring code can decide whether to soft-fail.
func NewStaticResolverFromFile(path string) (PermissionResolver, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &staticResolver{}, nil
		}
		return nil, fmt.Errorf("toolloop/perms: read %q: %w", path, err)
	}
	var cfg staticConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("toolloop/perms: parse %q: %w", path, err)
	}
	return NewStaticResolver(cfg.Rules)
}

// NewStaticResolverFromDataDir is the production-friendly helper used
// by the rpc layer: it reads <dataDir>/mcp_servers.json. Same
// soft-fail semantics as NewStaticResolverFromFile.
func NewStaticResolverFromDataDir(dataDir string) (PermissionResolver, error) {
	if dataDir == "" {
		return &staticResolver{}, nil
	}
	return NewStaticResolverFromFile(filepath.Join(dataDir, "mcp_servers.json"))
}

// Resolve is concurrency-safe: rules is read-only after construction.
func (r *staticResolver) Resolve(_ context.Context, _ /*sessionID*/, server, tool string) (Resolution, error) {
	if rule, ok := matchRules(r.rules, server, tool); ok {
		return Resolution{Server: server, Tool: tool, Policy: rule.Policy, Reason: rule.Reason}, nil
	}
	return Resolution{Server: server, Tool: tool, Policy: PolicyAutoAllow}, nil
}

// MCPOverride is a single per-session override entry. C2 is expected
// to land sessions.MCPOverrides with this exact shape; until then this
// type is the canonical wire form for WP02.
type MCPOverride struct {
	Server string
	Tool   string
	Policy ToolPolicy
	Reason string
}

// SessionOverrideReader is the narrow surface the session-override
// resolver consumes. The real session manager is expected to grow a
// matching method (sessions.MCPOverrides) in C2; until then the rpc
// layer can wire NoopSessionOverrideReader so the resolver always
// reports auto_allow.
type SessionOverrideReader interface {
	MCPOverrides(ctx context.Context, sessionID string) ([]MCPOverride, error)
}

// NoopSessionOverrideReader returns an empty override list for every
// session. Use it in tests and as the default until C2 lands.
type NoopSessionOverrideReader struct{}

func (NoopSessionOverrideReader) MCPOverrides(_ context.Context, _ string) ([]MCPOverride, error) {
	return nil, nil
}

// sessionOverrideResolver consults a SessionOverrideReader on every
// Resolve. The reader's result is not cached: the loop's iteration
// cadence is low enough that a per-call read is fine, and a per-
// session cache risks staleness when the user edits overrides between
// turns.
type sessionOverrideResolver struct {
	reader SessionOverrideReader
}

// NewSessionOverrideResolver wraps a SessionOverrideReader. A nil
// reader is normalized to NoopSessionOverrideReader so callers never
// have to nil-check.
func NewSessionOverrideResolver(r SessionOverrideReader) PermissionResolver {
	if r == nil {
		r = NoopSessionOverrideReader{}
	}
	return &sessionOverrideResolver{reader: r}
}

func (r *sessionOverrideResolver) Resolve(ctx context.Context, sessionID, server, tool string) (Resolution, error) {
	res, _, err := r.resolveWithMatch(ctx, sessionID, server, tool)
	return res, err
}

// resolveWithMatch is the in-package extension that exposes the
// "matched a rule?" bit MergedResolver needs without bloating the
// public PermissionResolver surface. Returns (auto_allow, false) when
// no override applies.
func (r *sessionOverrideResolver) resolveWithMatch(ctx context.Context, sessionID, server, tool string) (Resolution, bool, error) {
	overrides, err := r.reader.MCPOverrides(ctx, sessionID)
	if err != nil {
		return Resolution{}, false, fmt.Errorf("toolloop/perms: session overrides: %w", err)
	}
	rules := make([]permRule, 0, len(overrides))
	for _, o := range overrides {
		rules = append(rules, permRule{
			Server: o.Server,
			Tool:   o.Tool,
			Policy: o.Policy,
			Reason: o.Reason,
		})
	}
	if rule, ok := matchRules(rules, server, tool); ok {
		return Resolution{Server: server, Tool: tool, Policy: rule.Policy, Reason: rule.Reason}, true, nil
	}
	return Resolution{Server: server, Tool: tool, Policy: PolicyAutoAllow}, false, nil
}

// MergedResolver composes a static (global) and session-scoped
// resolver. Session overrides win whenever they match a rule;
// otherwise the static verdict stands.
//
// "Match" here means "a session-side rule applied", not "the result
// is non-auto_allow" — that distinction matters because a session
// might explicitly allow what a global rule denies, and vice versa.
type MergedResolver struct {
	static  PermissionResolver
	session sessionMatchResolver
}

// sessionMatchResolver narrows what MergedResolver needs from its
// session resolver: a Resolve that distinguishes "matched a rule"
// from "no rule matched". The concrete sessionOverrideResolver
// satisfies it; arbitrary callers fall through the shim below.
type sessionMatchResolver interface {
	resolveWithMatch(ctx context.Context, sessionID, server, tool string) (Resolution, bool, error)
}

// NewMergedResolver composes a static and a session resolver. Either
// argument may be nil — a nil static defaults to auto_allow, a nil
// session resolver becomes the noop reader.
func NewMergedResolver(static, session PermissionResolver) PermissionResolver {
	if static == nil {
		static, _ = NewStaticResolver(nil)
	}
	if session == nil {
		session = NewSessionOverrideResolver(nil)
	}
	hook, ok := session.(sessionMatchResolver)
	if !ok {
		// External resolvers go through a shim that treats any
		// non-auto_allow result OR any auto_allow result with a
		// non-empty Reason as a match. This matches the documented
		// contract for custom session resolvers.
		hook = sessionResolverShim{inner: session}
	}
	return &MergedResolver{static: static, session: hook}
}

// sessionResolverShim adapts an arbitrary PermissionResolver into the
// resolveWithMatch surface MergedResolver expects.
type sessionResolverShim struct {
	inner PermissionResolver
}

func (s sessionResolverShim) resolveWithMatch(ctx context.Context, sessionID, server, tool string) (Resolution, bool, error) {
	res, err := s.inner.Resolve(ctx, sessionID, server, tool)
	if err != nil {
		return Resolution{}, false, err
	}
	matched := res.Policy != PolicyAutoAllow || res.Reason != ""
	return res, matched, nil
}

// Resolve composes session and static rules. Session matches win;
// otherwise the static verdict stands.
func (m *MergedResolver) Resolve(ctx context.Context, sessionID, server, tool string) (Resolution, error) {
	res, matched, err := m.session.resolveWithMatch(ctx, sessionID, server, tool)
	if err != nil {
		return Resolution{}, err
	}
	if matched {
		return res, nil
	}
	return m.static.Resolve(ctx, sessionID, server, tool)
}

// allowAllResolver is the loop's default when Config.Permissions is
// nil — every (server, tool) returns auto_allow without touching disk
// or sessions.
type allowAllResolver struct{}

func (allowAllResolver) Resolve(_ context.Context, _, server, tool string) (Resolution, error) {
	return Resolution{Server: server, Tool: tool, Policy: PolicyAutoAllow}, nil
}
