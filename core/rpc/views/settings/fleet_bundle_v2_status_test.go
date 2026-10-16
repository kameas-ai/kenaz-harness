package settings

// signed-bundle-bytes-01SBBYT01 WP03 (Settings surface): the v2 decode
// report — unknown setting names and min_client_versions this build cannot
// enforce — reaches FleetConfigPullStatus.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

// TestFleetConfigPullStatus_V2DecodeReport drives a REAL fleet.ConfigPoller
// whose disk cache holds a v2 envelope signed with a pinned key: Start
// re-verifies it (real file, real signature) and the projection carries the
// report. No client → nothing is fetched or applied.
func TestFleetConfigPullStatus_V2DecodeReport(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fleet.SetSigningKeyForTesting(pub))

	payload := []byte(`{"bundle_id":7,"key_id":"` + fleet.SigningKeyID(pub) + `","issued_at":"2026-10-15T12:00:00Z",` +
		`"mcp_allowlist":["github"],"model_prefs":{"provider_allowlist":["anthropic"],"future_knob":true},` +
		`"future_section":{"x":1},"min_client_versions":{"model_prefs.allow_user_credentials":"99.0.0"}}`)
	envelope, err := fleet.SignEnvelopeForTesting(payload, priv, "")
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "fleet"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "fleet", "bundle.json"), envelope, 0o600); err != nil {
		t.Fatal(err)
	}

	state := &fleetState{}
	poller := fleet.NewConfigPoller(nil, dataDir, &compositeConfigApplier{state: state})
	poller.SetBuildVersion("v0.95.0")
	state.configPoller = poller
	api := &API{fleet: state}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	poller.Start(ctx)
	defer poller.Stop()

	cs, err := api.FleetConfigPullStatus(context.Background())
	if err != nil {
		t.Fatalf("FleetConfigPullStatus: %v", err)
	}
	if cs.LastAppliedID != 7 || cs.Source != "cache" {
		t.Fatalf("status = %+v, want bundle 7 restored from cache", cs)
	}
	if cs.UnknownSettingsCount != 2 || !reflect.DeepEqual(cs.UnknownSettings, []string{"future_section", "model_prefs.future_knob"}) {
		t.Errorf("unknown settings = %d %v", cs.UnknownSettingsCount, cs.UnknownSettings)
	}
	want := []FleetUnenforceableSettingView{{Name: "model_prefs.allow_user_credentials", MinVersion: "99.0.0"}}
	if !reflect.DeepEqual(cs.UnenforceableSettings, want) {
		t.Errorf("unenforceable = %+v, want %+v", cs.UnenforceableSettings, want)
	}
	if cs.UpdateRequired {
		t.Error("UpdateRequired set with no 406")
	}
}
