package serve_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/connectors"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/rpc"
	"github.com/kameas-ai/kenaz-harness/core/serve"
)

type noopPool struct{}

func (noopPool) OpenOne(context.Context, coremcp.ServerSpec) error { return nil }

func connectorsTestServer(t *testing.T, sup *connectors.Supervisor) (baseURL string, cancel context.CancelFunc) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	api := rpc.New(nil)
	ctx, ctxCancel := context.WithCancel(context.Background())
	opts := []serve.ServerOption{}
	if sup != nil {
		opts = append(opts, serve.WithConnectors(sup))
	}
	s := serve.New(api, addr, "tok", nil, nil, opts...)
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, cerr := net.Dial("tcp", addr)
		if cerr == nil {
			_ = c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return "http://" + addr, func() {
		ctxCancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("server did not shut down in time")
		}
	}
}

func postConnectorsRPC(t *testing.T, baseURL, method string, out any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"method": method})
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/rpc", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /rpc %s: %v", method, err)
	}
	defer resp.Body.Close()
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode %s: %v", method, err)
	}
	if envelope.Error != "" {
		t.Fatalf("%s error: %s", method, envelope.Error)
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		t.Fatalf("unmarshal %s result: %v", method, err)
	}
}

// TestConnectorsList_NotProvisioned covers the FR-004 legacy-image case:
// no supervisor wired (or an unprovisioned one) must answer
// provisioned:false with an empty list — the UI's "connectors not
// provisioned" state — never an error and never fake data.
func TestConnectorsList_NotProvisioned(t *testing.T) {
	// No supervisor at all.
	baseURL, cancel := connectorsTestServer(t, nil)
	var got serve.ConnectorsListResult
	postConnectorsRPC(t, baseURL, "Connectors_List", &got)
	cancel()
	if got.Provisioned || len(got.Connectors) != 0 {
		t.Errorf("no-supervisor result = %+v, want unprovisioned empty", got)
	}

	// A supervisor that saw no whitelist.
	sup := connectors.NewSupervisor(connectors.SupervisorConfig{
		Provisioning: connectors.Provisioning{Reason: connectors.ReasonNotProvisioned},
	})
	baseURL, cancel = connectorsTestServer(t, sup)
	defer cancel()
	got = serve.ConnectorsListResult{}
	postConnectorsRPC(t, baseURL, "Connectors_List", &got)
	if got.Provisioned || len(got.Connectors) != 0 {
		t.Errorf("unprovisioned result = %+v, want provisioned:false, empty", got)
	}
}

// TestConnectorsListAndStatus_Provisioned covers the provisioned path:
// whitelisted ids appear with catalog metadata and boot outcomes; an id
// the catalog does not know renders enabled:false (unavailable).
func TestConnectorsListAndStatus_Provisioned(t *testing.T) {
	catalog := func() *recipes.Catalog {
		return &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{{
			ID:          "datadog",
			DisplayName: "Datadog",
			Category:    "observability",
			Transport:   "http",
			URL:         "https://api.datadoghq.com/mcp",
		}}}
	}
	sup := connectors.NewSupervisor(connectors.SupervisorConfig{
		Provisioning: connectors.Provisioning{Provisioned: true, IDs: []string{"datadog", "ghost"}},
		Getenv:       func(string) string { return "" },
		Catalog:      catalog,
	})
	sup.SetPool(noopPool{})
	if err := sup.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	baseURL, cancel := connectorsTestServer(t, sup)
	defer cancel()

	var list serve.ConnectorsListResult
	postConnectorsRPC(t, baseURL, "Connectors_List", &list)
	if !list.Provisioned {
		t.Fatal("Provisioned = false, want true")
	}
	if len(list.Connectors) != 2 {
		t.Fatalf("connectors = %d, want 2", len(list.Connectors))
	}
	dd, ghost := list.Connectors[0], list.Connectors[1]
	if dd.ID != "datadog" || !dd.Enabled || dd.DisplayName != "Datadog" ||
		dd.Category != "observability" || dd.Transport != "http" {
		t.Errorf("datadog entry = %+v", dd)
	}
	if ghost.ID != "ghost" || ghost.Enabled {
		t.Errorf("ghost entry = %+v, want enabled:false (unavailable)", ghost)
	}

	var status serve.ConnectorsStatusResult
	postConnectorsRPC(t, baseURL, "Connectors_Status", &status)
	if !status.Provisioned || len(status.Connectors) != 2 {
		t.Fatalf("status = %+v", status)
	}
	if status.Connectors[0].SpawnState != connectors.SpawnStateOK {
		t.Errorf("datadog spawn_state = %q, want ok", status.Connectors[0].SpawnState)
	}
	if status.Connectors[1].Reason != connectors.ReasonUnknownRecipe {
		t.Errorf("ghost reason = %q, want unknown_recipe", status.Connectors[1].Reason)
	}
}
