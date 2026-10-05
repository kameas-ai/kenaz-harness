package install_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/install"
)

// fakeProvider is the WP03 test double: an in-memory provider whose
// "consumer" is a map the test controls. It deliberately lets a test make
// Install succeed WITHOUT the consumer seeing anything (consumerIgnores),
// which is exactly the badge-only shape FR-1 must refuse. Race-safe: the
// framework may be driven from several goroutines.
type fakeProvider struct {
	kind install.Kind

	mu              sync.Mutex
	items           map[string]install.Item
	consumer        map[string]string // id -> installed version
	consumerIgnores bool              // Install "succeeds" but registers nothing
	consumerSticky  bool              // Uninstall "succeeds" but the consumer keeps it
	verification    install.Verification
	listErr         error
	unavailable     []install.Unavailable
	requirements    []install.Requirement
	updateRef       install.Ref
	updateErr       error
	detailErr       error
	// stateErrAfterInstall makes every InstalledState read after an
	// Install fail (a transient consumer read error).
	stateErrAfterInstall bool
	installed            bool
	// artifacts stands in for what an install leaves outside the consumer
	// (a keychain entry, a store row): Install writes, Uninstall clears.
	artifacts map[string]bool

	installCalls   []install.InstallRequest
	uninstallCalls []string
}

func newFake(kind install.Kind) *fakeProvider {
	return &fakeProvider{
		kind:         kind,
		items:        map[string]install.Item{},
		consumer:     map[string]string{},
		verification: install.Verification{Method: install.VerifyBuiltin},
		artifacts:    map[string]bool{},
	}
}

func (p *fakeProvider) add(it install.Item) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.items[it.ID] = it
}

func (p *fakeProvider) Kind() install.Kind { return p.kind }

func (p *fakeProvider) List(_ context.Context, _ install.Filter) (install.Listing, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.listErr != nil {
		return install.Listing{}, p.listErr
	}
	out := install.Listing{Unavailable: p.unavailable}
	for id, it := range p.items {
		v, ok := p.consumer[id]
		it.State = install.State{Installed: ok, Version: v}
		out.Items = append(out.Items, it)
	}
	return out, nil
}

func (p *fakeProvider) Detail(_ context.Context, id string) (install.Item, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.detailErr != nil {
		return install.Item{}, p.detailErr
	}
	it, ok := p.items[id]
	if !ok {
		return install.Item{}, install.ErrNotFound
	}
	return it, nil
}

func (p *fakeProvider) Requirements(_ context.Context, _ string) ([]install.Requirement, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requirements, nil
}

func (p *fakeProvider) Verify(_ context.Context, _ install.Ref) (install.Verification, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.verification, nil
}

func (p *fakeProvider) Install(_ context.Context, req install.InstallRequest) (any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.installCalls = append(p.installCalls, req)
	p.installed = true
	p.artifacts[req.Ref.ID] = true
	if !p.consumerIgnores {
		p.consumer[req.Ref.ID] = req.Ref.Version
	}
	return "detail:" + req.Ref.ID, nil
}

func (p *fakeProvider) Uninstall(_ context.Context, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.uninstallCalls = append(p.uninstallCalls, id)
	delete(p.artifacts, id)
	if !p.consumerSticky {
		delete(p.consumer, id)
	}
	return nil
}

func (p *fakeProvider) InstalledState(_ context.Context, id string) (install.State, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stateErrAfterInstall && p.installed {
		return install.State{}, errors.New("consumer read: transient I/O error")
	}
	v, ok := p.consumer[id]
	return install.State{Installed: ok, Version: v, Consumer: "fake consumer"}, nil
}

func (p *fakeProvider) Update(_ context.Context, _ string) (install.Ref, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.updateRef, p.updateErr
}

func (p *fakeProvider) hasArtifact(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.artifacts[id]
}

func (p *fakeProvider) uninstalls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.uninstallCalls...)
}

func (p *fakeProvider) calls() []install.InstallRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]install.InstallRequest(nil), p.installCalls...)
}

type emitted struct {
	topic string
	ev    install.Event
}

type fakePublisher struct {
	mu  sync.Mutex
	got []emitted
}

func (f *fakePublisher) Emit(topic string, payload any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, emitted{topic: topic, ev: payload.(install.Event)})
}

func (f *fakePublisher) snapshot() []emitted {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]emitted(nil), f.got...)
}

func newFramework(t *testing.T, verify install.SignatureVerifier, ps ...*fakeProvider) (*install.Framework, *fakePublisher) {
	t.Helper()
	pub := &fakePublisher{}
	fw := install.New(pub, verify)
	for _, p := range ps {
		if err := fw.Register(p.kind, p); err != nil {
			t.Fatalf("Register(%s): %v", p.kind, err)
		}
	}
	return fw, pub
}

func TestRegister_RejectsMismatchAndDuplicate(t *testing.T) {
	fw := install.New(nil, nil)
	p := newFake(install.KindSkill)
	if err := fw.Register(install.KindWorkflow, p); !errors.Is(err, install.ErrKindMismatch) {
		t.Fatalf("mismatched kind: got %v, want ErrKindMismatch", err)
	}
	if err := fw.Register(install.KindSkill, p); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := fw.Register(install.KindSkill, newFake(install.KindSkill)); !errors.Is(err, install.ErrDuplicateProvider) {
		t.Fatalf("duplicate: got %v, want ErrDuplicateProvider", err)
	}
	if got := fw.Registered(); len(got) != 1 || got[0] != install.KindSkill {
		t.Fatalf("Registered() = %v", got)
	}
}

func TestInstall_ConsumerConfirmed_EmitsInstalled(t *testing.T) {
	p := newFake(install.KindWorkflow)
	p.add(install.Item{ID: "w1", Name: "W1", Source: install.SourceBuiltin})
	fw, pub := newFramework(t, nil, p)

	res, err := fw.Install(context.Background(), install.Ref{Kind: install.KindWorkflow, ID: "w1", Version: "v1"}, install.Inputs{})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !res.State.Installed || res.Detail != "detail:w1" {
		t.Fatalf("result = %+v", res)
	}
	if !res.Verification.Verified || res.Verification.Method != install.VerifyBuiltin {
		t.Fatalf("builtin item should be verified-by-binary: %+v", res.Verification)
	}
	evs := pub.snapshot()
	if len(evs) != 1 || evs[0].topic != install.TopicCapabilityInstalled {
		t.Fatalf("events = %+v, want one %s", evs, install.TopicCapabilityInstalled)
	}
	ev := evs[0].ev
	if ev.Kind != install.KindWorkflow || ev.ID != "w1" || !ev.Installed || ev.Via != "install" || ev.Consumer != "fake consumer" {
		t.Fatalf("event = %+v", ev)
	}
}

// FR-1 by construction: a provider whose Install returns success while the
// consumer sees nothing is the badge-only install. The framework must
// refuse it and announce nothing.
func TestInstall_BadgeOnly_RefusedWithErrNotConsumed(t *testing.T) {
	p := newFake(install.KindBundle)
	p.consumerIgnores = true
	fw, pub := newFramework(t, nil, p)

	_, err := fw.Install(context.Background(), install.Ref{Kind: install.KindBundle, ID: "b1"}, install.Inputs{})
	if !errors.Is(err, install.ErrNotConsumed) {
		t.Fatalf("got %v, want ErrNotConsumed", err)
	}
	if evs := pub.snapshot(); len(evs) != 0 {
		t.Fatalf("a refused install must announce nothing, got %+v", evs)
	}
	// Review M1: what the provider's Install left behind is cleaned up.
	if p.hasArtifact("b1") {
		t.Fatal("an unconsumed install stranded its artifacts (keychain entry / store row)")
	}
	if got := p.uninstalls(); len(got) != 1 || got[0] != "b1" {
		t.Fatalf("cleanup uninstall calls = %v", got)
	}
}

func TestInstall_UnknownKind(t *testing.T) {
	fw, _ := newFramework(t, nil)
	_, err := fw.Install(context.Background(), install.Ref{Kind: install.KindAgentPack, ID: "x"}, install.Inputs{})
	if !errors.Is(err, install.ErrUnknownKind) {
		t.Fatalf("got %v, want ErrUnknownKind", err)
	}
}

func TestInstall_Signature_VerifiedPayloadReachesInstall(t *testing.T) {
	p := newFake(install.KindSkill)
	p.verification = install.Verification{Method: install.VerifySignature, Payload: []byte("payload-bytes"), Signature: "sig"}

	var mu sync.Mutex
	var seen []string
	verify := func(_ context.Context, ref install.Ref, payload []byte, sig string) (bool, string, error) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, ref.ID+"|"+string(payload)+"|"+sig)
		return true, "", nil
	}
	fw, pub := newFramework(t, verify, p)

	res, err := fw.Install(context.Background(), install.Ref{Kind: install.KindSkill, ID: "s1", Version: "1.0.0"}, install.Inputs{})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	mu.Lock()
	if len(seen) != 1 || seen[0] != "s1|payload-bytes|sig" {
		t.Fatalf("verifier saw %v", seen)
	}
	mu.Unlock()
	calls := p.calls()
	if len(calls) != 1 || string(calls[0].Verification.Payload) != "payload-bytes" || !calls[0].Verification.Verified {
		t.Fatalf("Install must receive the verified bytes: %+v", calls)
	}
	if res.Verification.Payload != nil {
		t.Fatal("result must not carry a second copy of the payload")
	}
	if ev := pub.snapshot()[0].ev; !ev.Verified || ev.VerifyMethod != install.VerifySignature {
		t.Fatalf("event verification = %+v", ev)
	}
}

// Register C-2 today: no per-device key exists, so the production verifier
// reports verified=false with a reason. The install proceeds and the
// event says it was unverified — the gap is recorded, not hidden.
func TestInstall_Signature_NoKey_ProceedsUnverifiedWithReason(t *testing.T) {
	p := newFake(install.KindSkill)
	p.verification = install.Verification{Method: install.VerifySignature, Payload: []byte("p"), Signature: "s"}
	verify := func(context.Context, install.Ref, []byte, string) (bool, string, error) {
		return false, "no per-device catalog signing key (register C-2)", nil
	}
	fw, pub := newFramework(t, verify, p)

	if _, err := fw.Install(context.Background(), install.Ref{Kind: install.KindSkill, ID: "s1"}, install.Inputs{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	ev := pub.snapshot()[0].ev
	if ev.Verified || !strings.Contains(ev.VerifyReason, "C-2") {
		t.Fatalf("event must record the unverified install and why: %+v", ev)
	}
}

func TestInstall_Signature_MismatchRefusesBeforeInstall(t *testing.T) {
	p := newFake(install.KindSkill)
	p.verification = install.Verification{Method: install.VerifySignature, Payload: []byte("p"), Signature: "bad"}
	verify := func(context.Context, install.Ref, []byte, string) (bool, string, error) {
		return false, "", errors.New("signature mismatch")
	}
	fw, pub := newFramework(t, verify, p)

	_, err := fw.Install(context.Background(), install.Ref{Kind: install.KindSkill, ID: "s1"}, install.Inputs{})
	if !errors.Is(err, install.ErrVerificationFailed) {
		t.Fatalf("got %v, want ErrVerificationFailed", err)
	}
	if len(p.calls()) != 0 || len(pub.snapshot()) != 0 {
		t.Fatal("a rejected payload must never reach the provider or announce anything")
	}
}

func TestInstall_Signature_NoVerifierFailsClosed(t *testing.T) {
	p := newFake(install.KindSkill)
	p.verification = install.Verification{Method: install.VerifySignature, Payload: []byte("p")}
	fw, _ := newFramework(t, nil, p)

	_, err := fw.Install(context.Background(), install.Ref{Kind: install.KindSkill, ID: "s1"}, install.Inputs{})
	if !errors.Is(err, install.ErrUnverifiable) {
		t.Fatalf("got %v, want ErrUnverifiable", err)
	}
	if len(p.calls()) != 0 {
		t.Fatal("unverifiable payload reached Install")
	}
}

func TestInstall_RequirementsUnmet(t *testing.T) {
	p := newFake(install.KindMCPRecipe)
	p.requirements = []install.Requirement{
		{Kind: install.RequirementKey, Name: "API_KEY", Display: "API key", Required: true},
		{Kind: install.RequirementDirectory, Name: "allowed_directories", Required: true},
		{Kind: install.RequirementKey, Name: "STORED", Required: true, Satisfied: true},
		{Kind: install.RequirementConsent, Name: "warning", Required: true}, // flow-owned, not enforced
		{Kind: install.RequirementKey, Name: "OPTIONAL"},
	}
	fw, _ := newFramework(t, nil, p)
	ref := install.Ref{Kind: install.KindMCPRecipe, ID: "r1"}

	_, err := fw.Install(context.Background(), ref, install.Inputs{})
	if !errors.Is(err, install.ErrRequirementsUnmet) {
		t.Fatalf("got %v, want ErrRequirementsUnmet", err)
	}
	if !strings.Contains(err.Error(), "API key") || !strings.Contains(err.Error(), "allowed_directories") {
		t.Fatalf("error must name the missing inputs: %v", err)
	}
	if len(p.calls()) != 0 {
		t.Fatal("Install ran with unmet requirements")
	}

	in := install.Inputs{
		Secrets: map[string]string{"API_KEY": "sk-secret-value"},
		Config:  map[string]any{"allowed_directories": []any{"/tmp"}},
	}
	if _, err := fw.Install(context.Background(), ref, in); err != nil {
		t.Fatalf("Install with inputs: %v", err)
	}
}

func TestEvents_NeverCarrySecrets(t *testing.T) {
	p := newFake(install.KindMCPRecipe)
	fw, pub := newFramework(t, nil, p)
	in := install.Inputs{Secrets: map[string]string{"API_KEY": "sk-very-secret"}}
	if _, err := fw.Install(context.Background(), install.Ref{Kind: install.KindMCPRecipe, ID: "r1"}, in); err != nil {
		t.Fatalf("Install: %v", err)
	}
	for _, e := range pub.snapshot() {
		b, _ := json.Marshal(e.ev)
		if strings.Contains(string(b), "sk-very-secret") {
			t.Fatalf("event leaked a secret: %s", b)
		}
	}
}

func TestUninstall_ConsumerConfirmed_EmitsUninstalled(t *testing.T) {
	p := newFake(install.KindWorkflow)
	p.add(install.Item{ID: "w1", Name: "W1"})
	fw, pub := newFramework(t, nil, p)
	ctx := context.Background()
	if _, err := fw.Install(ctx, install.Ref{Kind: install.KindWorkflow, ID: "w1"}, install.Inputs{}); err != nil {
		t.Fatal(err)
	}
	if err := fw.Uninstall(ctx, install.KindWorkflow, "w1"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	evs := pub.snapshot()
	if len(evs) != 2 || evs[1].topic != install.TopicCapabilityUninstalled || evs[1].ev.Installed || evs[1].ev.Via != "uninstall" {
		t.Fatalf("events = %+v", evs)
	}
}

func TestUninstall_ReadOnlyRefused(t *testing.T) {
	p := newFake(install.KindMCPRecipe)
	p.add(install.Item{ID: "org1", ReadOnly: true, ReadOnlyReason: "Provisioned by your org"})
	p.consumer["org1"] = ""
	fw, pub := newFramework(t, nil, p)

	err := fw.Uninstall(context.Background(), install.KindMCPRecipe, "org1")
	if !errors.Is(err, install.ErrReadOnly) || !strings.Contains(err.Error(), "Provisioned by your org") {
		t.Fatalf("got %v, want ErrReadOnly with the reason", err)
	}
	if len(pub.snapshot()) != 0 {
		t.Fatal("refused uninstall announced")
	}
}

func TestUninstall_ConsumerStillLists_ErrStillConsumed(t *testing.T) {
	p := newFake(install.KindSkill)
	p.add(install.Item{ID: "s1", Name: "S1"})
	p.consumer["s1"] = "1"
	p.consumerSticky = true
	fw, pub := newFramework(t, nil, p)

	if err := fw.Uninstall(context.Background(), install.KindSkill, "s1"); !errors.Is(err, install.ErrStillConsumed) {
		t.Fatalf("got %v, want ErrStillConsumed", err)
	}
	if len(pub.snapshot()) != 0 {
		t.Fatal("unconfirmed uninstall announced")
	}
}

func TestList_MergesFiltersAndReportsUnavailable(t *testing.T) {
	mcp := newFake(install.KindMCPRecipe)
	mcp.add(install.Item{ID: "brave", Name: "Brave Search", Source: install.SourceRegistry, Keywords: []string{"websearch"}})
	mcp.add(install.Item{ID: "fs", Name: "Filesystem", Source: install.SourceBuiltin})
	skill := newFake(install.KindSkill)
	skill.add(install.Item{ID: "s1", Name: "Standup", Source: install.SourceOrgCatalog})
	skill.unavailable = []install.Unavailable{{Source: install.SourceOrgCatalog, Reason: "signed_out", Message: "Sign in"}}
	broken := newFake(install.KindWorkflow)
	broken.listErr = errors.New("store closed")
	fw, _ := newFramework(t, nil, mcp, skill, broken)
	ctx := context.Background()

	all, err := fw.List(ctx, install.Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all.Items) != 3 {
		t.Fatalf("items = %+v", all.Items)
	}
	reasons := map[string]bool{}
	for _, u := range all.Unavailable {
		reasons[string(u.Kind)+":"+u.Reason] = true
	}
	if !reasons["skill:signed_out"] || !reasons["workflow:error"] {
		t.Fatalf("unavailable = %+v (a failing provider must become a reason row)", all.Unavailable)
	}

	q, _ := fw.List(ctx, install.Filter{Query: "WEBSEARCH"})
	if len(q.Items) != 1 || q.Items[0].ID != "brave" {
		t.Fatalf("keyword query = %+v", q.Items)
	}
	bySource, _ := fw.List(ctx, install.Filter{Source: install.SourceOrgCatalog})
	if len(bySource.Items) != 1 || bySource.Items[0].Kind != install.KindSkill {
		t.Fatalf("source filter = %+v", bySource.Items)
	}
	byKind, _ := fw.List(ctx, install.Filter{Kind: install.KindMCPRecipe})
	if len(byKind.Items) != 2 || len(byKind.Unavailable) != 0 {
		t.Fatalf("kind filter = %+v", byKind)
	}
	if _, err := fw.List(ctx, install.Filter{Kind: install.KindAgentPack}); !errors.Is(err, install.ErrUnknownKind) {
		t.Fatalf("unregistered kind filter: got %v", err)
	}
}

func TestUpdate_InstallsResolvedRef(t *testing.T) {
	p := newFake(install.KindSkill)
	p.updateRef = install.Ref{ID: "s1", Version: "2.0.0"}
	fw, pub := newFramework(t, nil, p)

	res, err := fw.Update(context.Background(), install.KindSkill, "s1")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if res.State.Version != "2.0.0" {
		t.Fatalf("state = %+v", res.State)
	}
	if ev := pub.snapshot()[0].ev; ev.Via != "update" || ev.Version != "2.0.0" {
		t.Fatalf("event = %+v", ev)
	}

	p.updateErr = install.ErrNoUpdate
	if _, err := fw.Update(context.Background(), install.KindSkill, "s1"); !errors.Is(err, install.ErrNoUpdate) {
		t.Fatalf("got %v, want ErrNoUpdate", err)
	}
}

func TestObserve_AnnouncesOnlyWhatTheConsumerConfirms(t *testing.T) {
	p := newFake(install.KindMCPRecipe)
	fw, pub := newFramework(t, nil, p)
	ctx := context.Background()

	if st, err := fw.Observe(ctx, install.KindMCPRecipe, "r1", false); err != nil || st.Installed {
		t.Fatalf("Observe before install = %+v, %v", st, err)
	}
	if len(pub.snapshot()) != 0 {
		t.Fatal("Observe announced an install the consumer does not list")
	}
	before, _ := fw.State(ctx, install.KindMCPRecipe, "r1")
	p.mu.Lock()
	p.consumer["r1"] = ""
	p.mu.Unlock()
	if _, err := fw.Observe(ctx, install.KindMCPRecipe, "r1", before.Installed); err != nil {
		t.Fatal(err)
	}
	evs := pub.snapshot()
	if len(evs) != 1 || evs[0].ev.Via != "flow" || !evs[0].ev.Installed {
		t.Fatalf("events = %+v", evs)
	}
}

// Re-review blocker: re-installing (updating) a capability that already
// works, with one transient consumer read failing afterwards, must report
// ErrNotConsumed WITHOUT removing the working capability.
func TestInstall_ReinstallWithTransientReadFailureDoesNotUninstall(t *testing.T) {
	p := newFake(install.KindMCPRecipe)
	p.consumer["r1"] = "1"
	p.artifacts["r1"] = true
	p.stateErrAfterInstall = true
	fw, pub := newFramework(t, nil, p)
	_, err := fw.Install(context.Background(), install.Ref{Kind: install.KindMCPRecipe, ID: "r1", Version: "2"}, install.Inputs{})
	if !errors.Is(err, install.ErrNotConsumed) {
		t.Fatalf("got %v, want ErrNotConsumed", err)
	}
	if got := p.uninstalls(); len(got) != 0 {
		t.Fatalf("a re-install with a transient read failure uninstalled the working capability: %v", got)
	}
	p.mu.Lock()
	_, still := p.consumer["r1"]
	p.mu.Unlock()
	if !still || !p.hasArtifact("r1") {
		t.Fatal("the working capability is gone")
	}
	if len(pub.snapshot()) != 0 {
		t.Fatal("announced")
	}
}

// Review: re-authenticating an already-enabled recipe is not a new install.
func TestObserve_ReauthOfInstalledRecipeEmitsNothing(t *testing.T) {
	p := newFake(install.KindMCPRecipe)
	p.consumer["r1"] = ""
	fw, pub := newFramework(t, nil, p)
	ctx := context.Background()
	before, err := fw.State(ctx, install.KindMCPRecipe, "r1")
	if err != nil || !before.Installed {
		t.Fatalf("State = %+v, %v", before, err)
	}
	if _, err := fw.Observe(ctx, install.KindMCPRecipe, "r1", before.Installed); err != nil {
		t.Fatal(err)
	}
	if evs := pub.snapshot(); len(evs) != 0 {
		t.Fatalf("a re-auth emitted a second capability:installed: %+v", evs)
	}
}

// Review L1: if the item cannot be described, uninstall refuses (fail
// closed) instead of skipping the org-managed check.
func TestUninstall_DetailErrorFailsClosed(t *testing.T) {
	p := newFake(install.KindMCPRecipe)
	p.consumer["r1"] = ""
	p.detailErr = errors.New("catalog unreadable")
	fw, pub := newFramework(t, nil, p)
	err := fw.Uninstall(context.Background(), install.KindMCPRecipe, "r1")
	if err == nil || !strings.Contains(err.Error(), "catalog unreadable") {
		t.Fatalf("got %v, want the Detail error", err)
	}
	if len(p.uninstalls()) != 0 || len(pub.snapshot()) != 0 {
		t.Fatal("uninstall proceeded past an unreadable Detail")
	}
}

func TestFramework_ConcurrentInstalls(t *testing.T) {
	p := newFake(install.KindWorkflow)
	fw, pub := newFramework(t, nil, p)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := string(rune('a' + i))
			if _, err := fw.Install(context.Background(), install.Ref{Kind: install.KindWorkflow, ID: id}, install.Inputs{}); err != nil {
				t.Errorf("Install %s: %v", id, err)
			}
		}(i)
	}
	wg.Wait()
	if n := len(pub.snapshot()); n != 16 {
		t.Fatalf("events = %d, want 16", n)
	}
}
