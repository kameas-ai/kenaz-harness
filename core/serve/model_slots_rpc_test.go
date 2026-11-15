package serve_test

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/llm/envprovider"
	"github.com/kameas-ai/kenaz-harness/core/rpc"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/serve"
)

// B11: the served Settings page's model-slot read — a workbench with empty
// slots shows derived values, and kenaz-meta.env host values win over them.
func TestServed_ModelSlots_DerivedAndHost(t *testing.T) {
	env := map[string]string{
		"ANTHROPIC_API_KEY":         "sk-ant-test-not-a-real-key-000000",
		"KENAZ_HARNESS_MODELS":      "claude-sonnet-4-5,claude-opus-5-5,claude-opus-4-5,claude-haiku-4-5",
		"KENAZ_HARNESS_ROLE_MODELS": `{"advisor":"claude-opus-5-5"}`,
	}
	get := envprovider.Getenv(func(k string) string { return env[k] })
	store, err := settings.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := rpc.New(nil,
		rpc.WithSettingsStore(store),
		rpc.WithHostProviders(serve.HostProviders(get, nil)),
		rpc.WithHostModelDefaults(serve.HostModelDefaults(get, nil)))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := serve.New(api, addr, "tok", nil, nil)
	go func() { _ = s.Serve(ctx) }()
	for i := 0; i < 150; i++ {
		if c, derr := net.Dial("tcp", addr); derr == nil {
			_ = c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	resp := authedPost(t, "http://"+addr, "tok", `{"method":"Settings_ModelSlots","params":{}}`)
	defer resp.Body.Close()
	var envelope struct {
		Result settings.ModelSlotsView `json:"result"`
		Error  string                  `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error != "" {
		t.Fatalf("error: %s", envelope.Error)
	}
	got := map[string]string{}
	src := map[string]string{}
	for _, e := range envelope.Result.Slots {
		got[string(e.Slot)] = e.ModelID
		src[string(e.Slot)] = string(e.Source)
	}
	if envelope.Result.ProviderID != serve.HostProviderProfileID {
		t.Fatalf("provider = %q", envelope.Result.ProviderID)
	}
	if src["subagent_default"] != "derived" || got["subagent_default"] == "" {
		t.Errorf("subagent_default = %s (%s)", got["subagent_default"], src["subagent_default"])
	}
	if src["compaction"] != "derived" || got["compaction"] != "claude-haiku-4-5" {
		t.Errorf("compaction = %s (%s)", got["compaction"], src["compaction"])
	}
	if src["advisor"] != "host" || got["advisor"] != "claude-opus-5-5" {
		t.Errorf("advisor = %s (%s)", got["advisor"], src["advisor"])
	}
}
