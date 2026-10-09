package settings

// Tool-exposure settings (tool-context-budget-01TCBUD01 WP02): the tier
// layer, schema budget and activation TTL persist through the real
// settings.json file, read back as stored values beside read-only
// effective ones, survive a no-edit read-write round trip unchanged,
// and are refused when invalid on both the targeted and the
// full-record save paths.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
)

func TestToolExposure_DefaultsOnFreshInstall(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err := NewAPI(store).GetToolExposure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := toolexposure.Settings{
		EffectiveSchemaBudgetTokens: toolexposure.DefaultSchemaBudgetTokens,
		EffectiveActivationTTLTurns: toolexposure.DefaultActivationTTLTurns,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fresh install = %+v, want stored 0/0 with effective defaults %+v", got, want)
	}
}

// A surface that reads, edits nothing and writes back must not pin
// today's defaults into the user's file: unset budget and TTL stay
// unset, and the file is byte-identical.
func TestToolExposure_NoEditRoundTripLeavesFileUnchanged(t *testing.T) {
	ctx := context.Background()
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := NewAPI(store)
	if err := api.SetToolExposure(ctx, toolexposure.Settings{
		Exposure: toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{"outlook": {Tier: toolexposure.TierOff}}},
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	got, err := api.GetToolExposure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := api.SetToolExposure(ctx, got); err != nil {
		t.Fatalf("write back unedited: %v", err)
	}
	after, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("no-edit round trip rewrote settings.json:\nbefore %s\nafter  %s", before, after)
	}
	all, _ := store.LoadAll()
	if all.ToolSchemaBudgetTokens != 0 || all.ToolActivationTTLTurns != 0 {
		t.Fatalf("round trip pinned defaults: budget %d, ttl %d", all.ToolSchemaBudgetTokens, all.ToolActivationTTLTurns)
	}
}

func TestToolExposure_FileRoundTripAcrossStores(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	in := toolexposure.Settings{
		Exposure: toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{
			"outlook":    {Tier: toolexposure.TierOff, Tools: map[string]toolexposure.Tier{"send-mail": toolexposure.TierFull}},
			"filesystem": {Tier: toolexposure.TierFull},
		}},
		SchemaBudgetTokens: 9000,
		ActivationTTLTurns: 3,
	}
	if err := NewAPI(store).SetToolExposure(ctx, in); err != nil {
		t.Fatalf("SetToolExposure: %v", err)
	}

	// A fresh store over the same file models an app restart.
	store2, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := NewAPI(store2).GetToolExposure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, in.WithEffective()) || got.EffectiveSchemaBudgetTokens != 9000 {
		t.Fatalf("after reload = %+v, want %+v", got, in.WithEffective())
	}

	raw, err := os.ReadFile(store2.Path())
	if err != nil {
		t.Fatal(err)
	}
	var onDisk map[string]json.RawMessage
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"toolExposure", "toolSchemaBudgetTokens", "toolActivationTtlTurns"} {
		if _, ok := onDisk[k]; !ok {
			t.Errorf("settings.json lacks %q: %s", k, raw)
		}
	}

	// Saving another field through the full-record path keeps them.
	all, err := store2.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	all.Theme = "dark"
	if err := store2.SaveAll(all); err != nil {
		t.Fatal(err)
	}
	if got, _ := NewAPI(store2).GetToolExposure(ctx); !reflect.DeepEqual(got, in.WithEffective()) {
		t.Fatalf("after unrelated SaveAll = %+v, want %+v", got, in.WithEffective())
	}

	// Clearing writes the fields away and reads back the defaults.
	if err := NewAPI(store2).SetToolExposure(ctx, toolexposure.Settings{}); err != nil {
		t.Fatal(err)
	}
	all, _ = store2.LoadAll()
	if all.ToolExposure != nil || all.ToolSchemaBudgetTokens != 0 || all.ToolActivationTTLTurns != 0 {
		t.Fatalf("cleared settings still hold %+v / %d / %d", all.ToolExposure, all.ToolSchemaBudgetTokens, all.ToolActivationTTLTurns)
	}
}

func TestToolExposure_InvalidRefusedOnEverySavePath(t *testing.T) {
	ctx := context.Background()
	bad := []toolexposure.Settings{
		{Exposure: toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{"outlook": {Tier: "hidden"}}}},
		{SchemaBudgetTokens: -5},
		{ActivationTTLTurns: toolexposure.MaxActivationTTLTurns + 1},
	}
	stores := map[string]SettingsStore{"memory": newMemoryStore()}
	fs, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stores["file"] = fs
	for name, store := range stores {
		api := NewAPI(store)
		for i, ts := range bad {
			if err := api.SetToolExposure(ctx, ts); err == nil {
				t.Errorf("%s: SetToolExposure(bad[%d]) accepted", name, i)
			}
			all, _ := store.LoadAll()
			all = all.withToolExposureSettings(ts)
			if err := store.SaveAll(all); err == nil {
				t.Errorf("%s: SaveAll(bad[%d]) accepted", name, i)
			}
		}
		got, err := api.GetToolExposure(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Exposure.IsZero() || got.SchemaBudgetTokens != 0 || got.ActivationTTLTurns != 0 {
			t.Errorf("%s: a refused write leaked: %+v", name, got)
		}
	}
}
