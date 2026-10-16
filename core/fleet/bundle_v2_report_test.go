package fleet

// bundle_v2_report_test.go — signed-bundle-bytes-01SBBYT01 WP03:
// unknown-field names and min_client_versions non-compliance (spec §4.5,
// §6 R1, §6a h).

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// (h) min_client_versions above this build → "can't enforce" raised, every
// known field still applied.
func TestConfigPoller_V2_MinClientVersions(t *testing.T) {
	fake := &fakeEnvelopeFleet{envelope: readV2Fixture(t, "h_min_client_versions.json")}
	applier := &fakeApplier{}
	dataDir := t.TempDir()
	p := newV2Poller(t, fake, applier, dataDir)
	p.SetBuildVersion("v0.95.0")
	if err := p.poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	got := applier.snapshot()
	if len(got) != 1 {
		t.Fatalf("applied %d bundles, want 1", len(got))
	}
	b := got[0]
	if b.BundleID != 15 || !reflect.DeepEqual(b.MCPAllowlist, []string{"github"}) ||
		b.ModelPrefs == nil || b.ModelPrefs.AllowUserCredentials == nil || *b.ModelPrefs.AllowUserCredentials ||
		!reflect.DeepEqual(b.ModelPrefs.ProviderAllowlist, []string{"anthropic"}) {
		t.Fatalf("known fields not applied: %+v / %+v", b, b.ModelPrefs)
	}
	st := p.Status()
	want := []UnenforceableField{{Name: "model_prefs.allow_user_credentials", MinVersion: "99.0.0"}}
	if !reflect.DeepEqual(st.UnenforceableFields, want) {
		t.Fatalf("UnenforceableFields = %+v, want %+v (mcp_allowlist's 0.1.0 is met)", st.UnenforceableFields, want)
	}
	if st.UnknownFieldCount != 0 {
		t.Errorf("min_client_versions must not count as unknown: %v", st.UnknownFields)
	}

	// The report survives a relaunch: it is rebuilt from the re-verified cache.
	p2 := newV2Poller(t, fake, &fakeApplier{}, dataDir)
	p2.SetBuildVersion("v0.95.0")
	if st2 := startOffline(t, p2); !reflect.DeepEqual(st2.UnenforceableFields, want) {
		t.Errorf("after restart UnenforceableFields = %+v", st2.UnenforceableFields)
	}
}

// Fixture (b): two unknown settings are counted and named, values never.
func TestConfigPoller_V2_UnknownFieldsReported(t *testing.T) {
	fake := &fakeEnvelopeFleet{envelope: readV2Fixture(t, "b_unknown_fields.json")}
	p := newV2Poller(t, fake, &fakeApplier{}, t.TempDir())
	if err := p.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := p.Status()
	if st.UnknownFieldCount != 2 || !reflect.DeepEqual(st.UnknownFields, []string{"future_section", "model_prefs.future_knob"}) {
		t.Fatalf("status = %d %v", st.UnknownFieldCount, st.UnknownFields)
	}
	raw, _ := json.Marshal(st)
	if strings.Contains(string(raw), `"enabled"`) {
		t.Error("status carries a value from inside an unknown section")
	}
	// A legacy bundle afterwards clears the report (nothing is known about it).
	fake.set(func(f *fakeEnvelopeFleet) { f.oldFleet = true; f.legacy = readV2Fixture(t, "f_legacy.json") })
	if err := p.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := p.Status(); st.UnknownFieldCount != 0 || st.UnknownFields != nil {
		t.Errorf("legacy bundle kept a stale report: %+v", st)
	}
}

func TestUnenforceableFields(t *testing.T) {
	mins := map[string]string{"a": "1.2.0", "b": "v0.9.0", "c": "not-a-version"}
	got := unenforceableFields(mins, "v1.0.0")
	want := []UnenforceableField{{Name: "a", MinVersion: "1.2.0"}, {Name: "c", MinVersion: "not-a-version"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	for _, dev := range []string{"", "dev"} {
		if got := unenforceableFields(mins, dev); got != nil {
			t.Errorf("build %q: got %+v, want nil", dev, got)
		}
	}
}

// Nested sections: unknown keys inside slices, maps of structs and pointer
// structs are named with their path; RawMessage sections and map keys are
// not field names and are never reported.
func TestUnknownBundleFields_Nested(t *testing.T) {
	p := []byte(`{
	  "bundle_id": 1, "issued_at": "2026-10-15T12:00:00Z", "mcp_allowlist": [],
	  "cedar_delta": {"anything": 1},
	  "org_config": {"some_kind": {"whatever": 1}},
	  "mandated_items": [{"catalog_id":"x","kind":"skill","version":"1","payload":{"p":1},"new_item_key":1}],
	  "tool_exposure": {"servers": {"outlook": {"tier":"off","pinned":true,"new_server_key":1,
	      "tools": {"send": {"tier":"off","pinned":false,"new_tool_key":1}}}}, "new_te_key": 2},
	  "provisioned_mcp": [{"recipe_id":"slack","oauth":{"client_id":"c","new_oauth_key":1}}],
	  "MCP_Allowlist": []
	}`)
	got := unknownBundleFields(p)
	want := []string{
		"mandated_items[].new_item_key",
		"provisioned_mcp[].oauth.new_oauth_key",
		"tool_exposure.new_te_key",
		"tool_exposure.servers.*.new_server_key",
		"tool_exposure.servers.*.tools.*.new_tool_key",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

// A payload naming more unknown keys than the cap is truncated.
func TestUnknownBundleFields_Bounded(t *testing.T) {
	m := map[string]any{"bundle_id": 1}
	for i := 0; i < maxReportedUnknownFields+20; i++ {
		m["k"+base64.RawURLEncoding.EncodeToString([]byte{byte(i)})] = i
	}
	raw, _ := json.Marshal(m)
	if n := len(unknownBundleFields(raw)); n != maxReportedUnknownFields {
		t.Errorf("reported %d names, want cap %d", n, maxReportedUnknownFields)
	}
}
