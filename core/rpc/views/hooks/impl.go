// Concrete HooksAPI implementation. Wraps core/hooks.Registry +
// core/hooks.BuiltinRegistry. The rpc layer constructs exactly one
// instance per process and shares the same Registry with the runner
// the LLM impl calls — Add / Remove on the API is reflected on the
// next chat send without a restart.
package hooks

import (
	"context"
	"errors"
	"fmt"

	corehooks "github.com/kameas-ai/kenaz-harness/core/hooks"
)

// Registry is the surface the impl uses. Mirrors core/hooks.Registry's
// public methods so the impl can be tested with a fake.
type Registry interface {
	List() []corehooks.Hook
	Get(id string) (corehooks.Hook, error)
	Add(h corehooks.Hook) error
	Update(h corehooks.Hook) error
	Remove(id string) error
}

// BuiltinDescriber surfaces the registered builtins for the UI's
// "AvailableBuiltins" dropdown.
type BuiltinDescriber interface {
	Builtins() []corehooks.BuiltinDescriptor
}

// API is the concrete HooksAPI implementation.
type API struct {
	reg      Registry
	builtins BuiltinDescriber
}

// Config bundles the impl's dependencies.
type Config struct {
	Registry Registry
	Builtins BuiltinDescriber
}

// New constructs a HooksAPI.
func New(cfg Config) *API {
	return &API{reg: cfg.Registry, builtins: cfg.Builtins}
}

// ErrUnavailable is returned when the impl was built without a
// backing registry. The chassis still boots; the surface fails fast
// so the UI can render a "hooks unavailable" empty state.
var ErrUnavailable = errors.New("hooks: registry unavailable")

func (a *API) List(_ context.Context) ([]Hook, error) {
	if a == nil || a.reg == nil {
		return []Hook{}, nil
	}
	stored := a.reg.List()
	out := make([]Hook, 0, len(stored))
	for _, h := range stored {
		out = append(out, hookToWire(h))
	}
	return out, nil
}

func (a *API) Get(_ context.Context, id string) (Hook, error) {
	if a == nil || a.reg == nil {
		return Hook{}, ErrUnavailable
	}
	h, err := a.reg.Get(id)
	if err != nil {
		return Hook{}, err
	}
	return hookToWire(h), nil
}

func (a *API) Add(_ context.Context, in HookInput) (Hook, error) {
	if a == nil || a.reg == nil {
		return Hook{}, ErrUnavailable
	}
	h := wireToHook(in)
	if err := a.reg.Add(h); err != nil {
		return Hook{}, err
	}
	return hookToWire(h), nil
}

func (a *API) Update(_ context.Context, in HookInput) error {
	if a == nil || a.reg == nil {
		return ErrUnavailable
	}
	return a.reg.Update(wireToHook(in))
}

func (a *API) Remove(_ context.Context, id string) error {
	if a == nil || a.reg == nil {
		return ErrUnavailable
	}
	return a.reg.Remove(id)
}

func (a *API) AvailableBuiltins(_ context.Context) ([]BuiltinDescriptor, error) {
	if a == nil || a.builtins == nil {
		return []BuiltinDescriptor{}, nil
	}
	stored := a.builtins.Builtins()
	out := make([]BuiltinDescriptor, 0, len(stored))
	for _, d := range stored {
		out = append(out, BuiltinDescriptor{
			ID:            d.ID,
			Name:          d.Name,
			Description:   d.Description,
			Events:        d.Events,
			DefaultConfig: d.DefaultConfig,
		})
	}
	return out, nil
}

// InstallStarterMemoryHooks adds the two preinstalled memory hooks.
// Idempotent: ErrHookExists is swallowed so the second toggle from
// "off" → "on" doesn't surface an error.
func (a *API) InstallStarterMemoryHooks(_ context.Context) error {
	if a == nil || a.reg == nil {
		return ErrUnavailable
	}
	for _, h := range corehooks.StarterMemoryHooks() {
		if err := a.reg.Add(h); err != nil && !errors.Is(err, corehooks.ErrHookExists) {
			return fmt.Errorf("hooks: install starter %s: %w", h.ID, err)
		}
	}
	return nil
}

// RemoveStarterMemoryHooks removes the two preinstalled memory
// hooks. ErrHookNotFound is swallowed so the toggle going off when
// the hooks were never installed (e.g. user edited them away by hand)
// is still a no-op success.
func (a *API) RemoveStarterMemoryHooks(_ context.Context) error {
	if a == nil || a.reg == nil {
		return ErrUnavailable
	}
	for _, h := range corehooks.StarterMemoryHooks() {
		if err := a.reg.Remove(h.ID); err != nil && !errors.Is(err, corehooks.ErrHookNotFound) {
			return fmt.Errorf("hooks: remove starter %s: %w", h.ID, err)
		}
	}
	return nil
}

func hookToWire(h corehooks.Hook) Hook {
	return Hook{
		ID:      h.ID,
		Name:    h.Name,
		Event:   h.Event,
		Kind:    h.Kind,
		Enabled: h.Enabled,
		Match: Match{
			SessionIDs: h.Match.SessionIDs,
			Kinds:      h.Match.Kinds,
			Models:     h.Match.Models,
		},
		Builtin:   h.Builtin,
		Command:   h.Command,
		MCPTool:   h.MCPTool,
		TimeoutMs: effectiveWireTimeoutMs(h),
		Config:    h.Config,
	}
}

// effectiveWireTimeoutMs surfaces the per-hook timeout to the wire shape,
// preferring the typed Hook.TimeoutMs field but falling back to a legacy
// config.timeout_ms entry that the v0.8.0 HookEditor wrote before the
// typed wire field existed. Once an old hook is opened+saved in the
// post-v0.8.x editor, the typed field wins; the Config entry becomes
// inert leftover that any subsequent migration can clean up.
func effectiveWireTimeoutMs(h corehooks.Hook) int {
	if h.TimeoutMs > 0 {
		return h.TimeoutMs
	}
	if h.Config == nil {
		return 0
	}
	v, ok := h.Config["timeout_ms"]
	if !ok {
		return 0
	}
	// JSON-decoded numbers arrive as float64; YAML/SQLite may give int.
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

func wireToHook(in Hook) corehooks.Hook {
	return corehooks.Hook{
		ID:      in.ID,
		Name:    in.Name,
		Event:   in.Event,
		Kind:    in.Kind,
		Enabled: in.Enabled,
		Match: corehooks.HookMatch{
			SessionIDs: in.Match.SessionIDs,
			Kinds:      in.Match.Kinds,
			Models:     in.Match.Models,
		},
		Builtin:   in.Builtin,
		Command:   in.Command,
		MCPTool:   in.MCPTool,
		TimeoutMs: in.TimeoutMs,
		Config:    in.Config,
	}
}

// Compile-time witness.
var _ HooksAPI = (*API)(nil)
