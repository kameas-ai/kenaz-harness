package capabilities_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/install"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/capabilities"
	slashview "github.com/kameas-ai/kenaz-harness/core/rpc/views/slashcmd"
	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

// fakeFleetCatalog stands in for the fleet catalog server (the seam core/rpc
// implements over *fleet.Client). Race-safe.
type fakeFleetCatalog struct {
	mu       sync.Mutex
	entries  map[string][]capabilities.CatalogEntry // kind -> entries
	payloads map[string][]byte                      // id@version -> payload
	listErr  error
	reason   string
	fetches  int
}

func newFakeFleetCatalog() *fakeFleetCatalog {
	return &fakeFleetCatalog{entries: map[string][]capabilities.CatalogEntry{}, payloads: map[string][]byte{}}
}

func (f *fakeFleetCatalog) publish(kind string, e capabilities.CatalogEntry, payload []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[kind] = append(f.entries[kind], e)
	f.payloads[e.ID+"@"+e.Version] = payload
}

func (f *fakeFleetCatalog) List(_ context.Context, kind string) ([]capabilities.CatalogEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]capabilities.CatalogEntry(nil), f.entries[kind]...), nil
}

func (f *fakeFleetCatalog) Fetch(_ context.Context, id, version string) ([]byte, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetches++
	p, ok := f.payloads[id+"@"+version]
	if !ok {
		return nil, "", errors.New("fleet/catalog: fetch: status 404")
	}
	return p, "sig-" + id, nil
}

func (f *fakeFleetCatalog) UnavailableReason(error) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reason
}

func (f *fakeFleetCatalog) fetchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fetches
}

// c2Verifier is the production verdict today: no per-device key, so every
// fleet payload installs recorded as unverified with the register C-2
// reason (fleet.CatalogSignatureVerdict("", …)).
func c2Verifier(context.Context, install.Ref, []byte, string) (bool, string, error) {
	return false, "no per-device catalog signing key (register C-2)", nil
}

type skillFixture struct {
	store    *coreslashcmd.SkillStore
	registry *coreslashcmd.Registry
	cat      *fakeFleetCatalog
	fw       *install.Framework
	pub      *recordingPublisher
}

func skillPayload(t *testing.T, id, trigger string) []byte {
	t.Helper()
	b, err := json.Marshal(coreslashcmd.Skill{ID: id, Trigger: trigger, Kind: coreslashcmd.KindText, Body: "hello " + trigger})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// newSkillFixture wires the REAL consumer: a SkillStore on disk, a real slash
// Registry, and the real slashcmd view (whose SkillInstallPayload calls
// fleet.InstallSkillPayload → slashcmd.LiveRegister). Only the fleet catalog
// server is faked.
func newSkillFixture(t *testing.T) *skillFixture {
	t.Helper()
	store := coreslashcmd.NewSkillStore(t.TempDir())
	registry, err := coreslashcmd.NewRegistry(coreslashcmd.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	slash := slashview.New(registry).WithSkillDeps(slashview.SkillDeps{SkillStore: store})
	cat := newFakeFleetCatalog()
	pub := &recordingPublisher{}
	fw := install.New(pub, c2Verifier)
	if err := fw.Register(install.KindSkill, capabilities.NewSkillProvider(cat, store, slash, slash)); err != nil {
		t.Fatal(err)
	}
	return &skillFixture{store: store, registry: registry, cat: cat, fw: fw, pub: pub}
}

func (f *skillFixture) row(t *testing.T, id string) install.Item {
	t.Helper()
	l, err := f.fw.List(context.Background(), install.Filter{Kind: install.KindSkill})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, it := range l.Items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("no skill row %q in %+v", id, l.Items)
	return install.Item{}
}

// P-3 (skill): install → the slash registry dispatches the trigger and the
// store holds it; uninstall → both drop it; the row's badge is that state.
// Named for scripts/ci/check-install-provider-coverage.sh.
func TestInstallProvider_Skill_ConsumerSeesInstall(t *testing.T) {
	f := newSkillFixture(t)
	ctx := context.Background()
	f.cat.publish("skill", capabilities.CatalogEntry{ID: "cat-standup", Slug: "standup", Version: "1.0.0", Visibility: "org_public"},
		skillPayload(t, "standup", "standup"))

	if f.row(t, "cat-standup").State.Installed {
		t.Fatal("reads installed before install")
	}
	res, err := f.fw.Install(ctx, install.Ref{Kind: install.KindSkill, ID: "cat-standup", Version: "1.0.0"}, install.Inputs{})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res.Verification.Verified || !strings.Contains(res.Verification.Reason, "C-2") {
		t.Fatalf("verification = %+v — the single verifier's C-2 verdict must be recorded", res.Verification)
	}
	if f.cat.fetchCount() != 1 {
		t.Fatalf("payload fetched %d times, want exactly once (Verify), reused by Install", f.cat.fetchCount())
	}
	if _, ok := f.registry.Lookup("standup"); !ok {
		t.Fatal("the slash registry does not dispatch /standup after install")
	}
	stored, err := f.store.List()
	if err != nil || len(stored) != 1 || stored[0].CatalogID != "cat-standup" {
		t.Fatalf("skill store = %+v, %v", stored, err)
	}
	row := f.row(t, "cat-standup")
	if !row.State.Installed || row.State.Version != "1.0.0" || row.Source != install.SourceOrgCatalog {
		t.Fatalf("row = %+v", row)
	}

	if err := f.fw.Uninstall(ctx, install.KindSkill, "cat-standup"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, ok := f.registry.Lookup("standup"); ok {
		t.Fatal("/standup still dispatches after uninstall")
	}
	if f.row(t, "cat-standup").State.Installed {
		t.Fatal("row still paints installed after uninstall")
	}
	topics, evs := f.pub.snapshot()
	if len(topics) != 2 || topics[0] != install.TopicCapabilityInstalled || topics[1] != install.TopicCapabilityUninstalled {
		t.Fatalf("events = %v", topics)
	}
	if evs[0].VerifyMethod != install.VerifySignature || evs[0].Verified {
		t.Fatalf("installed event verification = %+v", evs[0])
	}
}

func TestSkillProvider_UninstallByStoreID(t *testing.T) {
	// Settings › Skills addresses a skill by its store id, not the catalog id.
	f := newSkillFixture(t)
	ctx := context.Background()
	f.cat.publish("skill", capabilities.CatalogEntry{ID: "cat-x", Slug: "x", Version: "1.0.0", Visibility: "team"},
		skillPayload(t, "x-local", "xcmd"))
	if _, err := f.fw.Install(ctx, install.Ref{Kind: install.KindSkill, ID: "cat-x", Version: "1.0.0"}, install.Inputs{}); err != nil {
		t.Fatal(err)
	}
	if err := f.fw.Uninstall(ctx, install.KindSkill, "x-local"); err != nil {
		t.Fatalf("Uninstall(store id): %v", err)
	}
	if _, ok := f.registry.Lookup("xcmd"); ok {
		t.Fatal("still registered")
	}
}

// P-4-shaped (FR-2): an opaque skill payload is a named install error and
// leaves no installed state.
func TestSkillProvider_MalformedPayloadIsNamedAndNotInstalled(t *testing.T) {
	f := newSkillFixture(t)
	f.cat.publish("skill", capabilities.CatalogEntry{ID: "cat-bad", Slug: "bad", Version: "1.0.0"}, []byte("not a skill"))
	_, err := f.fw.Install(context.Background(), install.Ref{Kind: install.KindSkill, ID: "cat-bad", Version: "1.0.0"}, install.Inputs{})
	if err == nil || !strings.Contains(err.Error(), "payload is not in the format") {
		t.Fatalf("got %v, want the named malformed-payload error", err)
	}
	if f.row(t, "cat-bad").State.Installed {
		t.Fatal("a malformed payload left an installed badge")
	}
	if _, evs := f.pub.snapshot(); len(evs) != 0 {
		t.Fatalf("a refused install announced: %+v", evs)
	}
}

// P-5 (skill): signed out, installed skills still list (from the consumer)
// and each fleet source is a reason row.
func TestSkillProvider_SignedOut_ListsInstalledAndReasonRows(t *testing.T) {
	f := newSkillFixture(t)
	ctx := context.Background()
	f.cat.publish("skill", capabilities.CatalogEntry{ID: "cat-a", Slug: "a", Version: "1.0.0", Visibility: "team"},
		skillPayload(t, "a", "acmd"))
	if _, err := f.fw.Install(ctx, install.Ref{Kind: install.KindSkill, ID: "cat-a", Version: "1.0.0"}, install.Inputs{}); err != nil {
		t.Fatal(err)
	}
	f.cat.mu.Lock()
	f.cat.listErr = errors.New("fleet: unauthenticated")
	f.cat.reason = "signed_out"
	f.cat.mu.Unlock()

	l, err := f.fw.List(ctx, install.Filter{Kind: install.KindSkill})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(l.Items) != 1 || l.Items[0].ID != "cat-a" || !l.Items[0].State.Installed {
		t.Fatalf("items = %+v, want the installed skill from the consumer", l.Items)
	}
	sources := map[install.Source]string{}
	for _, u := range l.Unavailable {
		sources[u.Source] = u.Reason
	}
	if sources[install.SourceOrgCatalog] != "signed_out" || sources[install.SourceTeamCatalog] != "signed_out" {
		t.Fatalf("unavailable = %+v, want signed_out rows for both fleet sources", l.Unavailable)
	}
}

func TestSkillProvider_UpdateInstallsNewerVersion(t *testing.T) {
	f := newSkillFixture(t)
	ctx := context.Background()
	f.cat.publish("skill", capabilities.CatalogEntry{ID: "cat-u", Slug: "u", Version: "1.2.9"}, skillPayload(t, "u", "ucmd"))
	if _, err := f.fw.Install(ctx, install.Ref{Kind: install.KindSkill, ID: "cat-u", Version: "1.2.9"}, install.Inputs{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.fw.Update(ctx, install.KindSkill, "cat-u"); !errors.Is(err, install.ErrNoUpdate) {
		t.Fatalf("no newer version: got %v, want ErrNoUpdate", err)
	}
	f.cat.publish("skill", capabilities.CatalogEntry{ID: "cat-u", Slug: "u", Version: "1.2.10"}, skillPayload(t, "u", "ucmd"))
	if row := f.row(t, "cat-u"); !row.State.UpdateAvailable || row.Version != "1.2.10" {
		t.Fatalf("row = %+v, want update available to 1.2.10", row)
	}
	res, err := f.fw.Update(ctx, install.KindSkill, "cat-u")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if res.State.Version != "1.2.10" {
		t.Fatalf("state after update = %+v", res.State)
	}
}

func TestSkillProvider_MandatedSkillIsReadOnly(t *testing.T) {
	f := newSkillFixture(t)
	if err := coreslashcmd.LiveRegister(f.store, f.registry, coreslashcmd.Skill{
		ID: "org-policy", Trigger: "orgpolicy", Kind: coreslashcmd.KindText, Body: "x", Source: coreslashcmd.SkillSourceMandated,
	}); err != nil {
		t.Fatal(err)
	}
	row := f.row(t, "org-policy")
	if !row.ReadOnly || row.Source != install.SourceOrgCatalog || !row.State.Installed {
		t.Fatalf("mandated row = %+v", row)
	}
	if err := f.fw.Uninstall(context.Background(), install.KindSkill, "org-policy"); !errors.Is(err, install.ErrReadOnly) {
		t.Fatalf("got %v, want ErrReadOnly", err)
	}
}
