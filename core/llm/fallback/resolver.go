package fallback

import (
	"context"
	"fmt"
)

// model-request-path-live-01PMDL01 WP07: a concrete Resolver implementing
// the 3-level chain-ID hierarchy documented in the package doc comment
// (node attr → session default → app default), plus the context-borne
// node-attr override that lets a single call site honor a per-node
// FallbackChainId without threading a new parameter through the Streamer
// interface.
//
// Runner.Stream(ctx, req) already calls resolver.Resolve(ctx, req.SessionID)
// unconditionally on the primary call's error path — StoreResolver is the
// first concrete Resolver implementation wired against that contract.

// chainIDOverrideKey is the context key for a node-attr-level chain ID
// override (highest priority in the resolution hierarchy).
type chainIDOverrideKey struct{}

// WithChainIDOverride returns a context carrying a node-attr-level fallback
// chain ID override. The call site that has access to the firing node's
// ModelAttrs.FallbackChainId / EscalateAttrs.FallbackChainId (model /
// escalate node kinds) sets this before invoking Runner.Stream so
// StoreResolver.Resolve treats it as the highest-priority source, per the
// hierarchy documented on the fallback package. An empty id is a no-op —
// it does not clear a previously-set override.
func WithChainIDOverride(parent context.Context, chainID string) context.Context {
	if chainID == "" {
		return parent
	}
	return context.WithValue(parent, chainIDOverrideKey{}, chainID)
}

// ChainIDOverrideFromContext returns the node-attr-level chain ID override
// pinned via WithChainIDOverride, if any.
func ChainIDOverrideFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(chainIDOverrideKey{}).(string)
	return v, ok && v != ""
}

// StoreResolver is a Resolver that implements the 3-level chain-ID
// hierarchy (node attr → session default → app default) and then looks the
// resolved ID up via Store (operator-saved chains take precedence, falling
// through to the bundled defaults — mirroring the precedence documented on
// LoadBundled).
//
// SessionDefault and AppDefault are dependency-injected lookups so this
// type stays decoupled from the concrete session-store / settings-store
// implementations: the call site wires whatever accessors it has (e.g. a
// session-settings repository, an app-config reader) without StoreResolver
// needing to import those packages.
type StoreResolver struct {
	// Store resolves a chain ID to its persisted (operator-editable)
	// definition. Required.
	Store Store

	// SessionDefault, when non-nil, returns the session-level default
	// chain ID (2nd priority). Returning ("", nil) means "no session
	// default configured" — resolution continues to AppDefault.
	SessionDefault func(ctx context.Context, sessionID string) (string, error)

	// AppDefault, when non-nil, returns the app-level default chain ID
	// (3rd / lowest priority). Returning ("", nil) means "no app default
	// configured" — resolution yields (nil, nil), i.e. no fallback.
	AppDefault func(ctx context.Context) (string, error)

	// bundled loads the embedded default chains. Overridable in tests;
	// nil defaults to LoadBundled.
	bundled func() ([]*Chain, error)
}

// Resolve implements Resolver. It determines the winning chain ID per the
// hierarchy (node-attr context override → SessionDefault → AppDefault),
// then resolves that ID to a *Chain via Store, falling back to the bundled
// defaults when the Store has no operator override for that ID.
//
// Returns (nil, nil) — "no fallback configured" — when no level of the
// hierarchy names a chain ID. Returns a non-nil error only when a chain ID
// WAS resolved but could not be loaded (e.g. Store.Get failed); per
// Runner.Stream's contract this still results in the primary error being
// surfaced unchanged (fail-open on fallback misconfiguration), never
// blocking the turn.
func (s *StoreResolver) Resolve(ctx context.Context, sessionID string) (*Chain, error) {
	chainID, source, err := s.resolveChainID(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("fallback.StoreResolver: resolving chain id (%s level): %w", source, err)
	}
	if chainID == "" {
		return nil, nil
	}

	if s.Store != nil {
		c, gerr := s.Store.Get(ctx, chainID)
		if gerr != nil {
			return nil, fmt.Errorf("fallback.StoreResolver: store lookup %q: %w", chainID, gerr)
		}
		if c != nil {
			return c, nil
		}
	}

	// No operator override on file — fall through to the bundled defaults.
	loadBundled := s.bundled
	if loadBundled == nil {
		loadBundled = LoadBundled
	}
	bundled, berr := loadBundled()
	if berr != nil {
		return nil, fmt.Errorf("fallback.StoreResolver: load bundled: %w", berr)
	}
	for _, c := range bundled {
		if c.ID == chainID {
			return c, nil
		}
	}

	// A chain ID was explicitly configured (node/session/app level) but
	// names neither an operator-saved chain nor a bundled default. Treat
	// this as "no fallback configured" rather than erroring the whole
	// turn — a stale/typo'd chain ID must not turn an otherwise-recoverable
	// primary error into a harder failure.
	return nil, nil
}

// resolveChainID walks the 3-level hierarchy and returns the winning chain
// ID plus which level produced it (for error attribution).
func (s *StoreResolver) resolveChainID(ctx context.Context, sessionID string) (id string, source string, err error) {
	if override, ok := ChainIDOverrideFromContext(ctx); ok {
		return override, "node", nil
	}
	if s.SessionDefault != nil {
		id, err = s.SessionDefault(ctx, sessionID)
		if err != nil {
			return "", "session", err
		}
		if id != "" {
			return id, "session", nil
		}
	}
	if s.AppDefault != nil {
		id, err = s.AppDefault(ctx)
		if err != nil {
			return "", "app", err
		}
		if id != "" {
			return id, "app", nil
		}
	}
	return "", "", nil
}
