package serve_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/envprovider"
	"github.com/kameas-ai/kenaz-harness/core/rpc"
	llmview "github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	"github.com/kameas-ai/kenaz-harness/core/serve"
)

// wireProvider is the subset of the llm view's Provider shape this test
// asserts on. Kept local so the test breaks loudly if the wire contract the
// served frontend reads ever changes shape.
type wireProvider struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Model string `json:"model"`
	Cred  struct {
		Kind    string `json:"kind"`
		Locator string `json:"locator"`
	} `json:"cred"`
	Source string `json:"source"`
}

// newServedHarness starts a served harness whose environment is exactly env,
// mirroring the production boot in main.go's runServeMode.
func newServedHarness(t *testing.T, token string, env map[string]string) (baseURL string, cancel context.CancelFunc) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	get := envprovider.Getenv(func(k string) string { return env[k] })
	api := rpc.New(nil, rpc.WithHostProviders(serve.HostProviders(get, nil)))

	ctx, ctxCancel := context.WithCancel(context.Background())
	s := serve.New(api, addr, token, nil, nil)

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

// listProviders calls the served LLM_ListProviders RPC and returns the rows.
func listProviders(t *testing.T, baseURL, token string) []wireProvider {
	t.Helper()
	resp := authedPost(t, baseURL, token, `{"method":"LLM_ListProviders","params":{}}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, b)
	}
	var envelope struct {
		Result []wireProvider `json:"result"`
		Error  string         `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Error != "" {
		t.Fatalf("LLM_ListProviders returned an error: %s", envelope.Error)
	}
	return envelope.Result
}

// hostRow returns the control-plane-supplied row, if any. Rows from the
// developer's own providers.json may also be present when the tests run on a
// workstation, so this filters by source rather than asserting on length.
func hostRow(rows []wireProvider) *wireProvider {
	for i := range rows {
		if rows[i].Source == "host" {
			return &rows[i]
		}
	}
	return nil
}

// The headline behaviour: a served harness booted with a granted key answers
// LLM_ListProviders with a usable provider, so the frontend's hasAnyProvider
// is true and the Providers screen has something honest to render.
func TestServed_ListProviders_ReportsHostProvider(t *testing.T) {
	baseURL, cancel := newServedHarness(t, "tok", map[string]string{
		"ANTHROPIC_API_KEY": "sk-not-a-real-key",
	})
	defer cancel()

	got := hostRow(listProviders(t, baseURL, "tok"))
	if got == nil {
		t.Fatal("no host-sourced provider in the served list")
	}
	if got.ID != serve.HostProviderProfileID {
		t.Errorf("id = %q, want %q", got.ID, serve.HostProviderProfileID)
	}
	if got.Kind != "anthropic" {
		t.Errorf("kind = %q, want anthropic", got.Kind)
	}
	if got.Model == "" {
		t.Error("model is empty — the row cannot drive a chat request")
	}
	if got.Cred.Kind != "env" || got.Cred.Locator != "ANTHROPIC_API_KEY" {
		t.Errorf("cred = %+v, want an env reference to ANTHROPIC_API_KEY", got.Cred)
	}
}

// The wire payload must carry the credential var NAME and never its bytes.
func TestServed_ListProviders_NeverCarriesCredentialBytes(t *testing.T) {
	const secret = "sk-ant-secret-value-do-not-leak"
	baseURL, cancel := newServedHarness(t, "tok", map[string]string{
		"ANTHROPIC_API_KEY": secret,
	})
	defer cancel()

	resp := authedPost(t, baseURL, "tok", `{"method":"LLM_ListProviders","params":{}}`)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("empty response body")
	}
	if strings.Contains(string(body), secret) {
		t.Fatalf("credential bytes leaked onto the served wire: %s", body)
	}
}

// Without a grant the served harness must still boot and still answer — with
// an empty list, so the frontend can render the actionable empty state rather
// than a spinner or an error.
func TestServed_ListProviders_NoGrantYieldsNoHostRow(t *testing.T) {
	baseURL, cancel := newServedHarness(t, "tok", nil)
	defer cancel()

	if got := hostRow(listProviders(t, baseURL, "tok")); got != nil {
		t.Fatalf("unexpected host provider with no grant: %+v", got)
	}
}

// Write methods stay unported: a served client must not be offered an edit
// path that cannot work.
func TestServed_ProviderWritesRemainUnported(t *testing.T) {
	baseURL, cancel := newServedHarness(t, "tok", map[string]string{
		"ANTHROPIC_API_KEY": "k",
	})
	defer cancel()

	for _, method := range []string{"LLM_AddProvider", "LLM_UpdateProvider", "LLM_RemoveProvider"} {
		resp := authedPost(t, baseURL, "tok", `{"method":"`+method+`","params":{}}`)
		var envelope struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&envelope)
		resp.Body.Close()
		if envelope.Error == "" {
			t.Errorf("%s unexpectedly succeeded in served mode", method)
		}
	}
}

// A host provider is immutable from inside the VM even on the desktop-shaped
// API surface: there is no code path that lets the harness edit what Kenaz
// configured.
func TestHostProvider_IsImmutable(t *testing.T) {
	api := rpc.New(nil, rpc.WithHostProviders([]corellm.ProviderProfile{{
		ID:    serve.HostProviderProfileID,
		Kind:  "anthropic",
		Model: "claude-sonnet-4-5",
		Cred:  corellm.CredentialReference{Kind: "env", Locator: "ANTHROPIC_API_KEY"},
	}}))
	ctx := context.Background()
	conn := api.LLMConnector()

	err := conn.RemoveProvider(ctx, serve.HostProviderProfileID)
	if err == nil {
		t.Fatal("RemoveProvider on a host provider unexpectedly succeeded")
	}
	// Assert the SPECIFIC guard, not merely "some error": a nil personal
	// store would also fail here and would hide a missing guard.
	if !errors.Is(err, llmview.ErrHostProviderImmutable) {
		t.Fatalf("want ErrHostProviderImmutable, got %v", err)
	}
	if !strings.Contains(err.Error(), "Kenaz") {
		t.Errorf("error must tell the user where the provider IS editable, got %q", err)
	}
}
