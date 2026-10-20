package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/anthropic"
	"github.com/kameas-ai/kenaz-harness/core/llm/credref"
	"github.com/kameas-ai/kenaz-harness/core/llm/custom"
	"github.com/kameas-ai/kenaz-harness/core/llm/events"
	"github.com/kameas-ai/kenaz-harness/core/llm/ollama"
	"github.com/kameas-ai/kenaz-harness/core/llm/openai"
	"github.com/kameas-ai/kenaz-harness/core/llm/openrouter"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// Secrets a user can put in a custom-openai endpoint configuration: a
// bare-username token in the URL's userinfo, a query-string key, and the
// credential itself (which a misbehaving proxy echoes back in an error
// body).
const (
	redactURLToken = "URLTOKENu5er9q"
	redactQueryKey = "SECRETquery7k2"
	redactCred     = "SECRET2bearer44x"
)

var redactSecrets = []string{redactURLToken, redactQueryKey, redactCred}

// secretEndpoint rewrites an httptest base URL to carry the URL token as
// bare-username userinfo and the query key as ?api_key=.
func secretEndpoint(t *testing.T, base string) string {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.User(redactURLToken)
	u.Path = "/v1"
	u.RawQuery = "api_key=" + redactQueryKey
	return u.String()
}

type matrixStore struct{ m *custom.CapabilityMatrix }

func (s matrixStore) Get(context.Context, string) (*custom.CapabilityMatrix, error) { return s.m, nil }

// streamCustom drives one request through the REAL registry pipeline
// (audit emitter → retry → custom adapter) and returns the error plus
// every emitted entry, JSON-encoded as the event-log sink would persist it.
func streamCustom(t *testing.T, endpoint string, store custom.CapabilityStore, req llm.GenerationRequest) (error, string) {
	t.Helper()
	opts := []custom.Option{}
	if store != nil {
		opts = append(opts, custom.WithCapabilityStore(store))
	}
	return streamVia(t, custom.New(opts...), "m", endpoint, req)
}

// streamVia is streamCustom for any adapter kind.
func streamVia(t *testing.T, adapter llm.ProviderAdapter, model, endpoint string, req llm.GenerationRequest) (error, string) {
	t.Helper()
	const key = "TEST_REG_CUSTOM_REDACT_KEY"
	t.Setenv(key, redactCred)

	r, sink := newReg(t)
	r.resolver = credref.New(secrets.NewMemoryBackend())
	r.RegisterAdapter(adapter)
	prof := llm.ProviderProfile{
		ID: "redact-" + adapter.Kind(), Kind: adapter.Kind(), Model: model,
		Endpoint:        endpoint,
		Cred:            llm.CredentialReference{Kind: "env", Locator: key},
		Retry:           &llm.RetryPolicy{MaxAttempts: 2, BaseMS: 1, MaxMS: 1, Jitter: "full"},
		CapabilityHints: map[llm.Capability]bool{llm.CapToolCalling: true, llm.CapStreaming: true},
	}
	if err := r.LoadProfiles([]llm.ProviderProfile{prof}); err != nil {
		t.Fatal(err)
	}
	req.ProfileID = prof.ID
	req.SessionID = "s"
	stream, err := r.Stream(context.Background(), req)
	if err == nil {
		for range stream.Events() {
		}
		_, err = stream.Final()
	}
	if err == nil {
		t.Fatal("expected the request to fail")
	}
	raw, jerr := json.Marshal(sink.Entries())
	if jerr != nil {
		t.Fatal(jerr)
	}
	var sawError bool
	for _, k := range sink.Kinds() {
		if k == events.KindError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatalf("expected an llm error event; kinds = %v", sink.Kinds())
	}
	return err, string(raw)
}

func assertNoSecrets(t *testing.T, where, text string) {
	t.Helper()
	for _, s := range redactSecrets {
		if strings.Contains(text, s) {
			t.Errorf("%s leaks secret %q:\n%s", where, s, text)
		}
	}
}

// TestCustomEndpoint_DialErrorNeverLeaksEndpointSecrets: a *url.Error from
// http.Client.Do embeds the full request URL (net/http only strips a
// password). It must reach neither the returned error nor the events.
func TestCustomEndpoint_DialErrorNeverLeaksEndpointSecrets(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	endpoint := secretEndpoint(t, srv.URL)
	srv.Close() // connection refused from here on

	err, emitted := streamCustom(t, endpoint, nil, llm.GenerationRequest{})
	assertNoSecrets(t, "returned error", err.Error())
	assertNoSecrets(t, "emitted events", emitted)

	// Identity and retry classification are unchanged.
	var transient *llm.ErrTransient
	if !errors.As(err, &transient) {
		t.Fatalf("want *llm.ErrTransient in chain, got %T: %v", err, err)
	}
	if !strings.Contains(transient.Message, "connect_refused") {
		t.Errorf("transient message should carry the classified cause, got %q", transient.Message)
	}
	if !strings.Contains(transient.Message, "xxxxx@") {
		t.Errorf("transient message should show the masked endpoint, got %q", transient.Message)
	}
}

// TestCustomEndpoint_ErrorBodyEchoNeverLeaksCredential: a non-2xx whose
// body echoes the request's Authorization header — both as a raw text
// body and inside the OpenAI JSON error envelope.
func TestCustomEndpoint_ErrorBodyEchoNeverLeaksCredential(t *testing.T) {
	echo := "Authorization: Bearer " + redactCred + " url=/v1?api_key=" + redactQueryKey + " user=" + redactURLToken
	for name, body := range map[string]string{
		"raw":  echo,
		"json": `{"error":{"type":"invalid_request_error","message":"bad request; you sent ` + echo + `"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()

			err, emitted := streamCustom(t, secretEndpoint(t, srv.URL), nil, llm.GenerationRequest{})
			assertNoSecrets(t, "returned error", err.Error())
			assertNoSecrets(t, "emitted events", emitted)
			var inv *llm.ErrInvalidRequest
			if !errors.As(err, &inv) || inv.Status != http.StatusBadRequest {
				t.Fatalf("want *llm.ErrInvalidRequest status 400, got %T: %v", err, err)
			}
		})
	}
}

// TestCustomEndpoint_MissingCapabilityNeverLeaksEndpointSecrets: the
// probed-matrix gate used to quote the raw endpoint in its error.
func TestCustomEndpoint_MissingCapabilityNeverLeaksEndpointSecrets(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	endpoint := secretEndpoint(t, srv.URL)
	m := custom.NewCapabilityMatrix(endpoint)
	m.ToolCalling = custom.CapabilityValueFalse

	req := llm.GenerationRequest{Tools: []llm.ToolSpec{{Name: "t", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	err, emitted := streamCustom(t, endpoint, matrixStore{m}, req)
	var mc *custom.ErrCustomEndpointMissingCapability
	if !errors.As(err, &mc) {
		t.Fatalf("want *custom.ErrCustomEndpointMissingCapability, got %T: %v", err, err)
	}
	assertNoSecrets(t, "returned error", err.Error())
	assertNoSecrets(t, "emitted events", emitted)
}

// echoBodies are error bodies a proxy/debug server in front of an
// endpoint returns, echoing the request's credential and URL secrets.
func echoBodies() map[string]string {
	echo := "Authorization: Bearer " + redactCred + " url=/v1?api_key=" + redactQueryKey + " user=" + redactURLToken
	return map[string]string{
		"raw":      echo,
		"json":     `{"error":{"type":"invalid_request_error","message":"bad request; you sent ` + echo + `"}}`,
		"tgi":      `{"error":"you sent ` + echo + `","error_type":"validation"}`,
		"fastapi":  `{"detail":[{"msg":"you sent ` + echo + `"}]}`,
		"html":     `<html><body><h1>400</h1><pre>` + echo + `</pre></body></html>`,
		"apikeyhd": "X-Custom-Key: " + redactCred,
	}
}

// TestEveryAdapter_ErrorBodyEchoNeverLeaksSecrets: the same echo through
// each adapter whose endpoint is user-configurable (prof.Endpoint), via
// the real registry → emitter path.
func TestEveryAdapter_ErrorBodyEchoNeverLeaksSecrets(t *testing.T) {
	adapters := map[string]func() llm.ProviderAdapter{
		"openai":     func() llm.ProviderAdapter { return openai.New() },
		"anthropic":  func() llm.ProviderAdapter { return anthropic.New() },
		"openrouter": func() llm.ProviderAdapter { return openrouter.New() },
		"ollama":     func() llm.ProviderAdapter { return ollama.New() },
		"custom":     func() llm.ProviderAdapter { return custom.New() },
	}
	models := map[string]string{"openai": "gpt-4o-mini", "anthropic": "claude-sonnet-4-5", "openrouter": "openai/gpt-4o-mini", "ollama": "llama3", "custom": "m"}
	for an, mk := range adapters {
		for bn, body := range echoBodies() {
			t.Run(fmt.Sprintf("%s/%s", an, bn), func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(body))
				}))
				defer srv.Close()
				endpoint := secretEndpoint(t, srv.URL)
				err, emitted := streamVia(t, mk(), models[an], endpoint, llm.GenerationRequest{})
				assertNoSecrets(t, "returned error", err.Error())
				assertNoSecrets(t, "emitted events", emitted)
				var inv *llm.ErrInvalidRequest
				if !errors.As(err, &inv) || inv.Status != http.StatusBadRequest {
					t.Fatalf("want *llm.ErrInvalidRequest status 400, got %T: %v", err, err)
				}
			})
		}
	}
}
