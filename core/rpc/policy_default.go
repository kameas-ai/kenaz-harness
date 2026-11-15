package rpc

// Host policy default (launch spec L09 D1, decided 2026-11-14): a
// standalone Harness on the host runs the STARTER policy — reads and edits
// inside the agent workspace run without asking; shell commands, the
// built-in network tools and writes outside the workspace ask. In served
// (workbench) mode the VM is the boundary (decision D-h): none of this
// applies, and bash availability stays a Fleet decision
// (WithFleetGatedBash).
//
// R6: an install that predates this build keeps its behaviour ("legacy")
// until the person accepts the starter policy once. R7: the first-run
// notice (frontend PolicyDefaultNotice.vue) says which default is active
// and where to change it.

import (
	"errors"
	"sync/atomic"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
)

// PolicyDefaultWorkbench is the mode a served harness reports.
const PolicyDefaultWorkbench = "workbench"

// ErrPolicyDefaultServed refuses a change in served mode.
var ErrPolicyDefaultServed = errors.New("inside a workbench the VM is the boundary; your organization's Fleet policy decides what runs")

// ErrPolicyDefaultUnknown refuses a mode other than starter or legacy.
var ErrPolicyDefaultUnknown = errors.New(`policy default must be "starter" or "legacy"`)

// PolicyDefaultView is the binding's answer.
type PolicyDefaultView struct {
	// Mode is "starter", "legacy" or "workbench".
	Mode string `json:"mode"`
	// NoticeSeen is true once the first-run notice was dismissed or
	// answered.
	NoticeSeen bool `json:"noticeSeen"`
	// Upgraded is true when this install predates the starter policy and
	// kept its earlier behaviour (the notice then offers the starter
	// policy instead of announcing it).
	Upgraded bool `json:"upgraded"`
}

// policyDefault holds the resolved host default. nil-safe: a nil value is
// "legacy", never starter.
type policyDefault struct {
	store    settings.SettingsStore
	served   bool
	starter  atomic.Bool
	upgraded atomic.Bool
}

// newPolicyDefault resolves the default once at boot. An undecided store
// is decided and persisted here: "starter" for a fresh install, "legacy"
// for one that has booted before. The test for "booted before" is either
// first-boot marker this build's predecessors always set
// (BashAllowlistMigrated, set by the bash allowlist migration on the
// first boot of every cedar-era build, or FirstRunOnboardingCompleted).
//
// persist=false decides the same way without writing the store: rpc.New
// passes it under `go test`, whose settings store can be the developer's
// real file (check-tests-are-hermetic.sh).
func newPolicyDefault(store settings.SettingsStore, served bool, persist ...bool) *policyDefault {
	p := &policyDefault{store: store, served: served}
	if served || store == nil {
		return p
	}
	var mode string
	var upgraded bool
	decide := settings.Update
	if len(persist) > 0 && !persist[0] {
		decide = func(st settings.SettingsStore, fn func(*settings.Settings) error) error {
			cur, err := st.LoadAll()
			if err != nil {
				return err
			}
			return fn(&cur)
		}
	}
	err := decide(store, func(s *settings.Settings) error {
		switch s.PolicyDefault {
		case settings.PolicyDefaultStarter, settings.PolicyDefaultLegacy:
		default:
			if s.BashAllowlistMigrated || s.FirstRunOnboardingCompleted {
				s.PolicyDefault = settings.PolicyDefaultLegacy
			} else {
				s.PolicyDefault = settings.PolicyDefaultStarter
			}
		}
		mode = s.PolicyDefault
		upgraded = mode == settings.PolicyDefaultLegacy && !s.PolicyDefaultNoticeSeen
		return nil
	})
	if err != nil {
		// Unreadable settings: keep the earlier behaviour rather than
		// change what an agent may do (R6).
		logging.L().Warn("rpc.policy_default.resolve_failed", "err", err.Error())
		return p
	}
	p.starter.Store(mode == settings.PolicyDefaultStarter)
	p.upgraded.Store(upgraded)
	logging.L().Info("rpc.policy_default", "mode", mode)
	return p
}

// StarterActive reports whether the host starter policy is in force.
func (p *policyDefault) StarterActive() bool {
	return p != nil && !p.served && p.starter.Load()
}

// View returns the current state for the binding.
func (p *policyDefault) View() (PolicyDefaultView, error) {
	if p == nil {
		return PolicyDefaultView{Mode: settings.PolicyDefaultLegacy}, nil
	}
	if p.served {
		return PolicyDefaultView{Mode: PolicyDefaultWorkbench, NoticeSeen: true}, nil
	}
	v := PolicyDefaultView{Mode: settings.PolicyDefaultLegacy, Upgraded: p.upgraded.Load()}
	if p.starter.Load() {
		v.Mode = settings.PolicyDefaultStarter
	}
	if p.store != nil {
		s, err := p.store.LoadAll()
		if err != nil {
			return v, err
		}
		v.NoticeSeen = s.PolicyDefaultNoticeSeen
	}
	return v, nil
}

// Set switches the host default and marks the notice answered. It takes
// effect on the next request: every consumer reads StarterActive live.
func (p *policyDefault) Set(mode string) error {
	if p == nil || p.store == nil {
		return errors.New("settings store unavailable")
	}
	if p.served {
		return ErrPolicyDefaultServed
	}
	if mode != settings.PolicyDefaultStarter && mode != settings.PolicyDefaultLegacy {
		return ErrPolicyDefaultUnknown
	}
	if err := settings.Update(p.store, func(s *settings.Settings) error {
		s.PolicyDefault = mode
		s.PolicyDefaultNoticeSeen = true
		return nil
	}); err != nil {
		return err
	}
	p.starter.Store(mode == settings.PolicyDefaultStarter)
	p.upgraded.Store(false)
	logging.L().Info("rpc.policy_default.set", "mode", mode)
	return nil
}

// MarkNoticeSeen records that the first-run notice was dismissed.
func (p *policyDefault) MarkNoticeSeen() error {
	if p == nil || p.store == nil || p.served {
		return nil
	}
	return settings.Update(p.store, func(s *settings.Settings) error {
		s.PolicyDefaultNoticeSeen = true
		return nil
	})
}

// policyDefaultHolder is the API's accessor; a HarnessAPI that is not *API
// (a test fake) has no policy default and reports "legacy".
type policyDefaultHolder interface{ policyDefaultState() *policyDefault }

func (a *API) policyDefaultState() *policyDefault {
	if a == nil {
		return nil
	}
	return a.policyDefault
}

func (b *Bindings) policyDefault() *policyDefault {
	if h, ok := b.api.(policyDefaultHolder); ok {
		return h.policyDefaultState()
	}
	return nil
}
