package install

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// TopicCapabilityInstalled is published after every install (or update)
// whose consumer confirms it, for every provider. Payload: Event.
const TopicCapabilityInstalled = "capability:installed"

// TopicCapabilityUninstalled is published after every uninstall whose
// consumer confirms the capability is gone. Payload: Event.
const TopicCapabilityUninstalled = "capability:uninstalled"

// Event is the payload of both capability topics. It never carries Inputs.
type Event struct {
	Kind      Kind   `json:"kind"`
	ID        string `json:"id"`
	Version   string `json:"version,omitempty"`
	Installed bool   `json:"installed"`
	// Via is how the transition happened: "install", "update",
	// "uninstall", or "flow" (a per-kind flow — OAuth sign-in, device
	// code — completed the install and the framework observed it).
	Via string `json:"via"`
	// VerifyMethod / Verified / VerifyReason record the Verify step
	// (empty on uninstall and on observed flows).
	VerifyMethod VerifyMethod `json:"verify_method,omitempty"`
	Verified     bool         `json:"verified"`
	VerifyReason string       `json:"verify_reason,omitempty"`
	// Consumer is the consumer that confirmed the transition.
	Consumer string `json:"consumer,omitempty"`
}

// Publisher is the event sink (core/rpc wires the stream broker).
type Publisher interface {
	Emit(topic string, payload any)
}

// SignatureVerifier is the single verification hook for every signed
// payload, for every provider (register C-2's future payload signature lands
// here once). It returns
// verified=true when the signature checks against a trusted key;
// verified=false with a reason when no key is available to check against
// (the install proceeds, recorded as unverified); and an error when the
// signature is checked and does not match (the install is refused).
type SignatureVerifier func(ctx context.Context, ref Ref, payload []byte, signature string) (verified bool, reason string, err error)

// Result is what Install / Update return.
type Result struct {
	Ref          Ref
	State        State
	Verification Verification
	// Detail is the provider's per-kind return value (see Provider.Install).
	Detail any
}

// Framework is the registry + orchestrator every install goes through.
type Framework struct {
	mu        sync.RWMutex
	providers map[Kind]Provider
	pub       Publisher
	verify    SignatureVerifier
}

// New returns an empty framework. pub may be nil (events dropped — test
// chassis only); verify may be nil, in which case every signed payload is
// refused with ErrUnverifiable.
func New(pub Publisher, verify SignatureVerifier) *Framework {
	return &Framework{providers: map[Kind]Provider{}, pub: pub, verify: verify}
}

// Register adds p as the provider for kind. kind must equal p.Kind(); one
// provider per kind.
func (f *Framework) Register(kind Kind, p Provider) error {
	if p == nil {
		return fmt.Errorf("install: register %s: nil provider", kind)
	}
	if p.Kind() != kind {
		return fmt.Errorf("%w: registered as %q, provider says %q", ErrKindMismatch, kind, p.Kind())
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.providers[kind]; ok {
		return fmt.Errorf("%w: %s", ErrDuplicateProvider, kind)
	}
	f.providers[kind] = p
	return nil
}

// Registered returns the kinds with a provider, sorted.
func (f *Framework) Registered() []Kind {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]Kind, 0, len(f.providers))
	for k := range f.providers {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (f *Framework) provider(kind Kind) (Provider, error) {
	f.mu.RLock()
	p, ok := f.providers[kind]
	f.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKind, kind)
	}
	return p, nil
}

// List merges every matching provider's listing. A provider that fails
// outright becomes an Unavailable row for its kind (reason "error") —
// the surface shows why the kind is missing instead of dropping it.
func (f *Framework) List(ctx context.Context, filter Filter) (Listing, error) {
	if filter.Kind != "" {
		if _, err := f.provider(filter.Kind); err != nil {
			return Listing{}, err
		}
	}
	out := Listing{Items: []Item{}}
	for _, kind := range f.Registered() {
		if filter.Kind != "" && filter.Kind != kind {
			continue
		}
		p, err := f.provider(kind)
		if err != nil {
			continue
		}
		l, err := p.List(ctx, filter)
		if err != nil {
			out.Unavailable = append(out.Unavailable, Unavailable{
				Kind: kind, Reason: "error", Message: err.Error(),
			})
			continue
		}
		for _, it := range l.Items {
			if !matches(it, filter) {
				continue
			}
			it.Kind = kind
			out.Items = append(out.Items, it)
		}
		for _, u := range l.Unavailable {
			if filter.Source != "" && u.Source != "" && u.Source != filter.Source {
				continue
			}
			u.Kind = kind
			out.Unavailable = append(out.Unavailable, u)
		}
	}
	sort.SliceStable(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return out, nil
}

// matches applies the filter's source and query to one item. Providers may
// pre-filter; the framework re-applies so every provider behaves the same.
func matches(it Item, f Filter) bool {
	if f.Source != "" && it.Source != f.Source {
		return false
	}
	q := strings.ToLower(strings.TrimSpace(f.Query))
	if q == "" {
		return true
	}
	hay := append([]string{it.Name, it.Description, it.ID}, it.Keywords...)
	for _, h := range hay {
		if strings.Contains(strings.ToLower(h), q) {
			return true
		}
	}
	return false
}

// Detail returns one item from its provider.
func (f *Framework) Detail(ctx context.Context, kind Kind, id string) (Item, error) {
	p, err := f.provider(kind)
	if err != nil {
		return Item{}, err
	}
	it, err := p.Detail(ctx, id)
	if err != nil {
		return Item{}, err
	}
	it.Kind = kind
	return it, nil
}

// Install runs the pipeline for every provider: requirements → verify →
// provider install → consumer confirmation → event.
func (f *Framework) Install(ctx context.Context, ref Ref, in Inputs) (Result, error) {
	return f.install(ctx, ref, in, "install")
}

func (f *Framework) install(ctx context.Context, ref Ref, in Inputs, via string) (Result, error) {
	p, err := f.provider(ref.Kind)
	if err != nil {
		return Result{}, err
	}
	reqs, err := p.Requirements(ctx, ref.ID)
	if err != nil {
		return Result{}, err
	}
	if err := checkRequirements(reqs, in); err != nil {
		return Result{}, err
	}

	v, err := p.Verify(ctx, ref)
	if err != nil {
		if errors.Is(err, ErrRevoked) {
			// skill-library-01SKLIB01 WP01: terminal, user-facing ("revoked
			// by your org"); returned once, never retried.
			logging.L().Info("install.revoked", "kind", string(ref.Kind), "id", ref.ID, "version", ref.Version)
		}
		return Result{}, err
	}
	if v.Method == VerifySignature {
		if f.verify == nil {
			return Result{}, fmt.Errorf("%w: %s %s", ErrUnverifiable, ref.Kind, ref.ID)
		}
		ok, reason, verr := f.verify(ctx, ref, v.Payload, v.Signature)
		if verr != nil {
			return Result{}, fmt.Errorf("%w: %s %s: %v", ErrVerificationFailed, ref.Kind, ref.ID, verr)
		}
		v.Verified, v.Reason = ok, reason
		if !ok {
			logging.L().Warn("install.unverified",
				"kind", string(ref.Kind), "id", ref.ID, "version", ref.Version, "reason", reason)
		}
	} else {
		// builtin / local: the binary or the device is the authority;
		// there is no detached signature to check.
		v.Verified = v.Method == VerifyBuiltin
	}

	// Whether the item was installed BEFORE this call decides whether an
	// unconfirmed install may be cleaned up: a re-install / update of a
	// working capability must never be removed because one post-install
	// consumer read failed (re-review blocker). An unreadable prior state
	// counts as "maybe installed" — no cleanup.
	prior, perr := p.InstalledState(ctx, ref.ID)
	newInstall := perr == nil && !prior.Installed

	detail, err := p.Install(ctx, InstallRequest{Ref: ref, Inputs: in, Verification: v})
	if err != nil {
		return Result{}, err
	}

	st, err := p.InstalledState(ctx, ref.ID)
	if err != nil {
		if newInstall {
			f.cleanupUnconsumed(ctx, p, ref)
		}
		return Result{}, fmt.Errorf("%w: %s %s: reading consumer state: %v", ErrNotConsumed, ref.Kind, ref.ID, err)
	}
	if !st.Installed {
		if newInstall {
			f.cleanupUnconsumed(ctx, p, ref)
		}
		return Result{}, fmt.Errorf("%w: %s %s", ErrNotConsumed, ref.Kind, ref.ID)
	}

	f.emit(TopicCapabilityInstalled, Event{
		Kind: ref.Kind, ID: ref.ID, Version: firstNonEmpty(st.Version, ref.Version),
		Installed: true, Via: via,
		VerifyMethod: v.Method, Verified: v.Verified, VerifyReason: v.Reason,
		Consumer: st.Consumer,
	})
	// The payload bytes are not part of the result: callers need the
	// verdict, not a second copy of the download.
	v.Payload = nil
	return Result{Ref: ref, State: st, Verification: v, Detail: detail}, nil
}

// Update installs the newer version the provider resolves for id.
func (f *Framework) Update(ctx context.Context, kind Kind, id string) (Result, error) {
	p, err := f.provider(kind)
	if err != nil {
		return Result{}, err
	}
	ref, err := p.Update(ctx, id)
	if err != nil {
		return Result{}, err
	}
	if ref.Kind == "" {
		ref.Kind = kind
	}
	if ref.ID == "" {
		ref.ID = id
	}
	return f.install(ctx, ref, Inputs{}, "update")
}

// Uninstall removes id through its provider, refusing org-managed items,
// and confirms with the consumer before announcing it.
func (f *Framework) Uninstall(ctx context.Context, kind Kind, id string) error {
	p, err := f.provider(kind)
	if err != nil {
		return err
	}
	// Fail closed (review L1): if the item cannot be described, it cannot
	// be shown not to be org-managed, so nothing is removed.
	it, derr := p.Detail(ctx, id)
	if derr != nil {
		return fmt.Errorf("install: uninstall %s %s: %w", kind, id, derr)
	}
	if it.ReadOnly {
		reason := it.ReadOnlyReason
		if reason == "" {
			reason = "provisioned by your org"
		}
		return fmt.Errorf("%w: %s %s: %s", ErrReadOnly, kind, id, reason)
	}
	if err := p.Uninstall(ctx, id); err != nil {
		return err
	}
	st, err := p.InstalledState(ctx, id)
	if err != nil {
		return fmt.Errorf("%w: %s %s: reading consumer state: %v", ErrStillConsumed, kind, id, err)
	}
	if st.Installed {
		return fmt.Errorf("%w: %s %s", ErrStillConsumed, kind, id)
	}
	f.emit(TopicCapabilityUninstalled, Event{
		Kind: kind, ID: id, Installed: false, Via: "uninstall", Consumer: st.Consumer,
	})
	return nil
}

// State reads id's installed state from its consumer.
func (f *Framework) State(ctx context.Context, kind Kind, id string) (State, error) {
	p, err := f.provider(kind)
	if err != nil {
		return State{}, err
	}
	return p.InstalledState(ctx, id)
}

// Observe is for a per-kind flow that completes an install the framework
// did not start (an MCP OAuth sign-in or device-code approval installs and
// respawns the recipe itself). wasInstalled is the consumer's state read
// (State) before the flow ran. Observe announces capability:installed only
// for a transition — the consumer lists it now and did not before — so
// re-authenticating an already-enabled recipe emits nothing. It never
// announces an install the consumer does not confirm.
func (f *Framework) Observe(ctx context.Context, kind Kind, id string, wasInstalled bool) (State, error) {
	p, err := f.provider(kind)
	if err != nil {
		return State{}, err
	}
	st, err := p.InstalledState(ctx, id)
	if err != nil {
		return State{}, err
	}
	if st.Installed && !wasInstalled {
		f.emit(TopicCapabilityInstalled, Event{
			Kind: kind, ID: id, Version: st.Version, Installed: true, Via: "flow", Consumer: st.Consumer,
		})
	}
	return st, nil
}

// cleanupUnconsumed removes what a provider's Install left behind when its
// consumer did not confirm the capability (review M1): no keychain entry,
// store row or file is stranded by a refused install. Called only for an
// item that was NOT installed before the call. Best effort — a failure is
// logged; the install still fails and nothing is announced.
func (f *Framework) cleanupUnconsumed(ctx context.Context, p Provider, ref Ref) {
	if err := p.Uninstall(ctx, ref.ID); err != nil {
		logging.L().Warn("install.unconsumed_cleanup_failed",
			"kind", string(ref.Kind), "id", ref.ID, "err", err.Error())
	}
}

func (f *Framework) emit(topic string, ev Event) {
	if f.pub == nil {
		return
	}
	f.pub.Emit(topic, ev)
}

// checkRequirements enforces the input-bearing requirement kinds.
func checkRequirements(reqs []Requirement, in Inputs) error {
	var missing []string
	for _, r := range reqs {
		if !r.Required || r.Satisfied {
			continue
		}
		switch r.Kind {
		case RequirementKey:
			if strings.TrimSpace(in.Secrets[r.Name]) == "" {
				missing = append(missing, displayOf(r))
			}
		case RequirementConfig, RequirementDirectory:
			if v, ok := in.Config[r.Name]; !ok || isEmptyValue(v) {
				missing = append(missing, displayOf(r))
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s", ErrRequirementsUnmet, strings.Join(missing, ", "))
	}
	return nil
}

func displayOf(r Requirement) string {
	if r.Display != "" {
		return r.Display
	}
	return r.Name
}

func isEmptyValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == ""
	case []any:
		return len(t) == 0
	case []string:
		return len(t) == 0
	}
	return false
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
