package fleet

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
)

// toolExposureVectorBundle is the conformance vector the fleet side
// mirrors (brief: fleet-tool-exposure-brief-2026-10-09.md §4).
func toolExposureVectorBundle() *Bundle {
	return &Bundle{
		BundleID: 5,
		KeyID:    "k",
		IssuedAt: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC),
		MandatedItems: []BundleMandatedItem{{
			CatalogID: "c", Kind: "skill", Version: "1", Payload: json.RawMessage(`{}`),
		}},
		ToolExposure: &BundleToolExposure{
			Servers: map[string]BundleToolExposureServer{
				"outlook": {Tier: "summary", Pinned: false, Tools: map[string]BundleToolExposureTool{
					"send-mail": {Tier: "off", Pinned: true},
				}},
				"filesystem": {Tier: "full", Pinned: true},
				"kenaz": {Tools: map[string]BundleToolExposureTool{
					"bash": {Tier: "off", Pinned: true},
				}},
			},
			BudgetTokens: 16000,
			HotSetExtra:  []string{"github__search_code", "fetch__fetch"},
		},
	}
}

const toolExposureVectorBytes = `{"bundle_id":5,"key_id":"k","issued_at":"2026-10-09T00:00:00Z","mcp_allowlist":null,` +
	`"mandated_items":[{"catalog_id":"c","kind":"skill","version":"1","payload":{}}],` +
	`"tool_exposure":{"servers":{` +
	`"filesystem":{"tier":"full","pinned":true},` +
	`"kenaz":{"pinned":false,"tools":{"bash":{"tier":"off","pinned":true}}},` +
	`"outlook":{"tier":"summary","pinned":false,"tools":{"send-mail":{"tier":"off","pinned":true}}}},` +
	`"budget_tokens":16000,"hot_set_extra":["github__search_code","fetch__fetch"]}}`

// TestSigningPayload_ToolExposure_ByteOrder pins the signed bytes: the
// slot directly after mandated_items, sorted map keys, "pinned" always
// present, server "tier" omitted when empty, hot_set_extra in received
// order.
func TestSigningPayload_ToolExposure_ByteOrder(t *testing.T) {
	got, err := toolExposureVectorBundle().signingPayload()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != toolExposureVectorBytes {
		t.Fatalf("tool_exposure signing bytes drifted:\n got %s\nwant %s", got, toolExposureVectorBytes)
	}

	b := toolExposureVectorBundle()
	b.ProvisionedMCP = []ProvisionedMCP{{RecipeID: "slack"}}
	got, _ = b.signingPayload()
	if i, j := strings.Index(string(got), `"tool_exposure"`), strings.Index(string(got), `"provisioned_mcp"`); i < 0 || j < i {
		t.Fatalf("tool_exposure must precede provisioned_mcp: %s", got)
	}

	b.ToolExposure = nil
	got, _ = b.signingPayload()
	if strings.Contains(string(got), "tool_exposure") {
		t.Fatalf("nil tool_exposure must be omitted, got %s", got)
	}
}

func TestSigningPayload_ToolExposure_StableKeyOrder(t *testing.T) {
	b1 := toolExposureVectorBundle()
	b2 := toolExposureVectorBundle()
	servers := map[string]BundleToolExposureServer{}
	for _, k := range []string{"kenaz", "outlook", "filesystem"} {
		servers[k] = b1.ToolExposure.Servers[k]
	}
	b2.ToolExposure.Servers = servers
	p1, err := b1.signingPayload()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		p2, err := b2.signingPayload()
		if err != nil {
			t.Fatal(err)
		}
		if string(p1) != string(p2) {
			t.Fatalf("signing payload not stable across map construction order (run %d):\n p1=%s\n p2=%s", i, p1, p2)
		}
	}
}

func TestVerify_ToolExposure_WireRoundTripAndTamper(t *testing.T) {
	pub, priv := newTestKeyPair(t)
	b := toolExposureVectorBundle()
	b.KeyID = ""
	signBundle(t, priv, b)

	var wire Bundle
	if err := json.Unmarshal([]byte(mustJSON(t, b)), &wire); err != nil {
		t.Fatal(err)
	}
	if err := Verify(&wire, pub, 0); err != nil {
		t.Fatalf("wire round trip failed verification: %v", err)
	}
	if !reflect.DeepEqual(wire.ToolExposure, b.ToolExposure) {
		t.Fatalf("section did not round-trip:\n got %+v\nwant %+v", wire.ToolExposure, b.ToolExposure)
	}

	wire.ToolExposure.Servers["filesystem"] = BundleToolExposureServer{Tier: "off", Pinned: true}
	if err := Verify(&wire, pub, 0); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("tampered tool_exposure verified (err=%v) — the section is not signed", err)
	}
}

// A key this build does not know inside the section is dropped on decode,
// so the re-marshalled payload differs from what fleet signed and the
// whole bundle is refused. Fleet must therefore only send fields a
// device's build knows (brief §5).
func TestVerify_ToolExposure_UnknownSubFieldRefusesBundle(t *testing.T) {
	pub, priv := newTestKeyPair(t)
	known := strings.Replace(toolExposureVectorBytes, `"key_id":"k",`, "", 1)
	signed := strings.Replace(known, `"budget_tokens":16000`, `"budget_tokens":16000,"future":1`, 1)
	if signed == known || known == toolExposureVectorBytes {
		t.Fatal("fixture: splice failed")
	}
	sig := signRaw(t, priv, []byte(signed))
	var wire Bundle
	raw := strings.TrimSuffix(signed, "}") + `,"signature":"` + sig + `"}`
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		t.Fatal(err)
	}
	if err := Verify(&wire, pub, 0); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("bundle with an unknown tool_exposure key verified (err=%v)", err)
	}
	// Control: the same raw route with the known bytes verifies.
	var ok Bundle
	if err := json.Unmarshal([]byte(strings.TrimSuffix(known, "}")+`,"signature":"`+signRaw(t, priv, []byte(known))+`"}`), &ok); err != nil {
		t.Fatal(err)
	}
	if err := Verify(&ok, pub, 0); err != nil {
		t.Fatalf("control bundle failed verification: %v", err)
	}
}

func signRaw(t *testing.T, priv ed25519.PrivateKey, payload []byte) string {
	t.Helper()
	h := sha256.Sum256(payload)
	return base64.RawStdEncoding.EncodeToString(ed25519.Sign(priv, h[:]))
}

// An unknown tier decodes (tiers are strings on the wire), verifies, and
// is refused entry by entry while the rest of the section applies.
func TestToolExposure_Policy_RefusesEntriesNotBundle(t *testing.T) {
	s := &BundleToolExposure{
		Servers: map[string]BundleToolExposureServer{
			"outlook": {Tier: "readonly", Pinned: true, Tools: map[string]BundleToolExposureTool{
				"send-mail":          {Tier: "off", Pinned: true},
				"outlook__list-mail": {Tier: "off", Pinned: true},
				"x":                  {Tier: "", Pinned: false},
			}},
			"fetch":  {Pinned: true},
			"github": {Tier: "full", Pinned: false},
		},
		BudgetTokens: -1,
		HotSetExtra:  []string{"nosep", "a__b", "a__b"},
	}
	p, errs := s.Policy(11)
	want := toolexposure.OrgPolicy{
		Pins:        toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{"outlook": {Tools: map[string]toolexposure.Tier{"send-mail": toolexposure.TierOff}}}},
		Defaults:    toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{"github": {Tier: toolexposure.TierFull}}},
		HotSetExtra: []string{"a__b"},
		BundleID:    11,
	}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("policy = %+v\nwant     %+v", p, want)
	}
	if len(errs) != 6 {
		t.Fatalf("errs = %v, want 6 (pinned-without-tier, unknown server tier, namespaced tool key, empty tool tier, budget, hot_set_extra name)", errs)
	}
	for _, e := range errs {
		if !errors.Is(e, ErrToolExposureEntryRefused) {
			t.Errorf("%v does not wrap ErrToolExposureEntryRefused", e)
		}
	}

	if p, errs := (*BundleToolExposure)(nil).Policy(3); !p.IsZero() || errs != nil {
		t.Fatalf("absent section = %+v, %v; want the zero policy", p, errs)
	}
	if p, errs := (&BundleToolExposure{}).Policy(3); !p.IsZero() || errs != nil {
		t.Fatalf("empty section = %+v, %v; want the zero policy", p, errs)
	}
}

func TestToolExposurePins_PersistReloadAndDisappear(t *testing.T) {
	dir := t.TempDir()
	pins := LoadToolExposurePins(dir)
	if !pins.Policy().IsZero() {
		t.Fatal("fresh store not empty")
	}
	b := toolExposureVectorBundle()
	if refused, err := pins.Apply(b.BundleID, b.ToolExposure); len(refused) != 0 || err != nil {
		t.Fatalf("apply: %v %v", refused, err)
	}
	applied := pins.Policy()
	if applied.SchemaBudgetTokens != 16000 || applied.BundleID != 5 || applied.Pins.TierFor("filesystem", "x") != toolexposure.TierFull {
		t.Fatalf("applied policy = %+v", applied)
	}

	reloaded := LoadToolExposurePins(dir).Policy()
	if !reflect.DeepEqual(reloaded, applied) {
		t.Fatalf("reloaded = %+v\nwant      %+v", reloaded, applied)
	}

	// The next verified bundle no longer carries the field.
	if refused, err := pins.Apply(6, nil); len(refused) != 0 || err != nil {
		t.Fatalf("apply absent: %v %v", refused, err)
	}
	if !pins.Policy().IsZero() {
		t.Fatalf("policy after the field disappeared = %+v, want zero", pins.Policy())
	}
	if _, err := os.Stat(toolExposureStatePath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state file survived the field disappearing: %v", err)
	}
	if !LoadToolExposurePins(dir).Policy().IsZero() {
		t.Fatal("reload after disappearance not empty")
	}

	_, _ = pins.Apply(7, b.ToolExposure)
	if err := saveBundleApplyMeta(dir, bundleApplyMeta{BuildVersion: "v1"}); err != nil {
		t.Fatal(err)
	}
	if err := pins.Clear(); err != nil {
		t.Fatal(err)
	}
	if !pins.Policy().IsZero() || !LoadToolExposurePins(dir).Policy().IsZero() {
		t.Fatal("Clear left a policy behind")
	}
	if m := loadBundleApplyMeta(dir); m.BuildVersion != "" {
		t.Fatalf("Clear kept the apply record (%+v): the next start would 304 with no policy", m)
	}
}

func TestToolExposurePins_PolicyIsACopy(t *testing.T) {
	pins := LoadToolExposurePins("")
	b := toolExposureVectorBundle()
	_, _ = pins.Apply(1, b.ToolExposure)
	p := pins.Policy()
	p.Pins.Servers["filesystem"] = toolexposure.ServerExposure{Tier: toolexposure.TierOff}
	p.HotSetExtra[0] = "x__y"
	if got := pins.Policy(); got.Pins.Servers["filesystem"].Tier != toolexposure.TierFull || got.HotSetExtra[0] != "github__search_code" {
		t.Fatalf("Policy() aliased the store: %+v", got)
	}
}

// A corrupt state file leaves no policy and forces the poller to re-apply
// the current bundle once (no 304 on a lost policy).
func TestToolExposurePins_CorruptStateForcesReapply(t *testing.T) {
	dir := t.TempDir()
	if err := saveBundleApplyMeta(dir, bundleApplyMeta{BuildVersion: "v1"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fleet", "tool_exposure_applied.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if p := LoadToolExposurePins(dir).Policy(); !p.IsZero() {
		t.Fatalf("corrupt state produced a policy: %+v", p)
	}
	if m := loadBundleApplyMeta(dir); m.BuildVersion != "" {
		t.Fatalf("apply meta survived (%+v): the poller would 304 a policy it lost", m)
	}
}

// pinsApplier applies only the tool_exposure section through a real
// store, the way compositeConfigApplier does: refusals are not bundle
// errors.
type pinsApplier struct {
	pins *ToolExposurePins
	mu   sync.Mutex
	n    int
}

func (a *pinsApplier) ApplyBundle(_ context.Context, b *Bundle) []error {
	a.mu.Lock()
	a.n++
	a.mu.Unlock()
	if _, err := a.pins.Apply(b.BundleID, b.ToolExposure); err != nil {
		return []error{err}
	}
	return nil
}

func (a *pinsApplier) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.n
}

// Review D1/D2 through the real ConfigPoller: a bundle with one refused
// entry advances the id, applies the valid entries, and is re-applied
// once on the next start (had_refusals) and again after a build change;
// with no refusals it is a 304. Sign-out (Clear) also forces one
// re-apply, so pins come back after the next sign-in without a new
// bundle id.
func TestConfigPoller_ToolExposureRefusalAdvancesAndReappliesOnce(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	setTestSigningKey(t, pub)
	b := &Bundle{BundleID: 1, IssuedAt: time.Now(), ToolExposure: &BundleToolExposure{
		Servers: map[string]BundleToolExposureServer{
			"outlook": {Tier: "off", Pinned: true},
			"github":  {Tier: "readonly", Pinned: true},
		},
	}}
	signBundle(t, priv, b)
	fake := &fakeFleetConfigServer{response: bundleToJSON(t, b)}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	dataDir := t.TempDir()

	run := func(build string, want int) *ToolExposurePins {
		t.Helper()
		pins := LoadToolExposurePins(dataDir)
		a := &pinsApplier{pins: pins}
		p := newPollerForTest(t, srv, a, dataDir)
		p.SetBuildVersion(build)
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		defer cancel()
		p.Start(ctx)
		time.Sleep(150 * time.Millisecond)
		p.Stop()
		if got := a.count(); got != want {
			t.Fatalf("build %q: applied %d times, want %d", build, got, want)
		}
		st := p.Status()
		if st.LastAppliedID != 1 || strings.Contains(st.LastError, "partial apply") {
			t.Fatalf("build %q: status %+v, want id 1 and no error (a refusal is not a bundle error)", build, st)
		}
		if got := pins.Policy().Pins.TierFor("outlook", "x"); got != toolexposure.TierOff {
			t.Fatalf("build %q: valid entry not in force (outlook = %q)", build, got)
		}
		return pins
	}

	run("v1", 1)
	if !loadBundleApplyMeta(dataDir).HadRefusals {
		t.Fatal("had_refusals not recorded for a bundle with a refused tool_exposure entry")
	}
	run("v1", 1) // re-applied once because of the refusal
	run("v2", 1) // and once more after a build change

	// Clean bundle: no refusals → 304 on the next start.
	clean := &Bundle{BundleID: 2, IssuedAt: time.Now(), ToolExposure: &BundleToolExposure{
		Servers: map[string]BundleToolExposureServer{"outlook": {Tier: "off", Pinned: true}},
	}}
	signBundle(t, priv, clean)
	fake.setResponse(bundleToJSON(t, clean))
	runClean := func(want int) *ToolExposurePins {
		t.Helper()
		pins := LoadToolExposurePins(dataDir)
		a := &pinsApplier{pins: pins}
		p := newPollerForTest(t, srv, a, dataDir)
		p.SetBuildVersion("v2")
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		defer cancel()
		p.Start(ctx)
		time.Sleep(150 * time.Millisecond)
		p.Stop()
		if got := a.count(); got != want {
			t.Fatalf("clean bundle: applied %d times, want %d", got, want)
		}
		return pins
	}
	runClean(1)
	pins := runClean(0)

	// Sign-out clears the pins and the apply record: the next start
	// re-applies the same bundle once and the pin is back.
	if err := pins.Clear(); err != nil {
		t.Fatal(err)
	}
	if got := runClean(1).Policy().Pins.TierFor("outlook", "x"); got != toolexposure.TierOff {
		t.Fatalf("pin not restored after sign-out + start: %q", got)
	}
}

// A corrupt state file makes the next poller start re-fetch and re-apply
// the current bundle, so the policy comes back without a new bundle id.
func TestConfigPoller_CorruptToolExposureStateRefetches(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	setTestSigningKey(t, pub)
	b := &Bundle{BundleID: 1, IssuedAt: time.Now(), ToolExposure: &BundleToolExposure{
		Servers: map[string]BundleToolExposureServer{"outlook": {Tier: "off", Pinned: true}},
	}}
	signBundle(t, priv, b)
	srv := httptest.NewServer(&fakeFleetConfigServer{response: bundleToJSON(t, b)})
	defer srv.Close()
	dataDir := t.TempDir()

	start := func() (*ToolExposurePins, int) {
		t.Helper()
		pins := LoadToolExposurePins(dataDir)
		a := &pinsApplier{pins: pins}
		p := newPollerForTest(t, srv, a, dataDir)
		p.SetBuildVersion("v1")
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		defer cancel()
		p.Start(ctx)
		time.Sleep(150 * time.Millisecond)
		p.Stop()
		return pins, a.count()
	}
	if _, n := start(); n != 1 {
		t.Fatalf("first start applied %d times, want 1", n)
	}
	if _, n := start(); n != 0 {
		t.Fatalf("second start applied %d times, want 0 (304)", n)
	}
	if err := os.WriteFile(toolExposureStatePath(dataDir), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	pins, n := start()
	if n != 1 {
		t.Fatalf("start after a corrupt state file applied %d times, want 1 (re-fetch)", n)
	}
	if got := pins.Policy().Pins.TierFor("outlook", "x"); got != toolexposure.TierOff {
		t.Fatalf("policy not restored after the re-fetch: %q", got)
	}
}
