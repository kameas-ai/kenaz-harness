package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/credref"
	"github.com/kameas-ai/kenaz-harness/core/llm/custom"
	"github.com/kameas-ai/kenaz-harness/core/llm/envprovider"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

func getenvFrom(kv map[string]string) envprovider.Getenv {
	return func(k string) string { return kv[k] }
}

// The workbench case the operator hit: Kenaz grants ANTHROPIC_API_KEY, the
// unit exports it, and the served harness must boot WITH a usable provider.
func TestHostProviders_SeedsProfileFromGrantedKey(t *testing.T) {
	got := HostProviders(getenvFrom(map[string]string{
		"ANTHROPIC_API_KEY": "sk-not-a-real-key",
	}), nil)

	if len(got) != 1 {
		t.Fatalf("want exactly one host profile, got %d", len(got))
	}
	p := got[0]
	if p.ID != HostProviderProfileID {
		t.Fatalf("profile id = %q, want %q", p.ID, HostProviderProfileID)
	}
	if p.Kind != "anthropic" {
		t.Fatalf("kind = %q, want anthropic", p.Kind)
	}
	if p.Model == "" {
		t.Fatal("profile has no model — StartStream would have nothing to call")
	}
	if p.Cred.Kind != "env" || p.Cred.Locator != "ANTHROPIC_API_KEY" {
		t.Fatalf("credential must be an indirect env reference, got %+v", p.Cred)
	}
	// The whole point of the indirect reference: the profile that gets
	// persisted/serialised anywhere must not carry key material.
	if p.Cred.Locator == "sk-not-a-real-key" {
		t.Fatal("credential VALUE leaked into the profile")
	}
}

// KENAZ_HARNESS_PROVIDER is what Spec 078 delivers alongside the grant.
func TestHostProviders_HonoursExplicitProviderVar(t *testing.T) {
	got := HostProviders(getenvFrom(map[string]string{
		EnvHostProvider:     "openai",
		"OPENAI_API_KEY":    "k",
		"ANTHROPIC_API_KEY": "k",
	}), nil)
	if len(got) != 1 || got[0].Kind != "openai" {
		t.Fatalf("explicit provider var ignored: %+v", got)
	}
}

func TestHostProviders_ModelOverride(t *testing.T) {
	got := HostProviders(getenvFrom(map[string]string{
		"ANTHROPIC_API_KEY": "k",
		EnvHostModel:        "claude-opus-4-1",
	}), nil)
	if len(got) != 1 || got[0].Model != "claude-opus-4-1" {
		t.Fatalf("model override ignored: %+v", got)
	}
}

func TestHostProviders_CredVarOverride(t *testing.T) {
	got := HostProviders(getenvFrom(map[string]string{
		EnvHostProvider: "anthropic",
		EnvHostCredVar:  "WORKBENCH_KEY",
		"WORKBENCH_KEY": "k",
	}), nil)
	if len(got) != 1 || got[0].Cred.Locator != "WORKBENCH_KEY" {
		t.Fatalf("cred var override ignored: %+v", got)
	}
}

// No grant must never be fatal — the workbench still has to boot so it can
// TELL the user what to do.
func TestHostProviders_NoCredentialYieldsNilNotPanic(t *testing.T) {
	if got := HostProviders(getenvFrom(nil), nil); got != nil {
		t.Fatalf("want nil with no credential, got %+v", got)
	}
}

// An empty value is the same as absent — an EnvGrant the operator declared
// but left blank must not produce a provider that fails on first use.
func TestHostProviders_EmptyValueIsUnconfigured(t *testing.T) {
	if got := HostProviders(getenvFrom(map[string]string{"ANTHROPIC_API_KEY": ""}), nil); got != nil {
		t.Fatalf("want nil for an empty grant, got %+v", got)
	}
}

// The served harness and the in-VM agent executor must never disagree about
// which provider a given environment implies.
func TestHostProviders_AgreesWithAgentExecDetectionTable(t *testing.T) {
	for _, d := range envprovider.Detections {
		d := d
		t.Run(d.Kind, func(t *testing.T) {
			got := HostProviders(getenvFrom(map[string]string{d.CredEnv: "k"}), nil)
			if len(got) != 1 {
				t.Fatalf("kind %q: want one profile, got %d", d.Kind, len(got))
			}
			if got[0].Kind != d.Kind {
				t.Fatalf("kind %q resolved as %q", d.Kind, got[0].Kind)
			}
			if got[0].Model != envprovider.DefaultModelFor(d.Kind) {
				t.Fatalf("kind %q: model %q != shared default %q",
					d.Kind, got[0].Model, envprovider.DefaultModelFor(d.Kind))
			}
		})
	}
}

// A workbench granted a host-side llama-server (the local-inference path):
// no key, the endpoint on the vmnet gateway, an explicit model.
func TestHostProviders_CustomOpenAIEndpointWithoutCredential(t *testing.T) {
	got := HostProviders(getenvFrom(map[string]string{
		EnvHostProvider: "custom-openai",
		EnvHostBaseURL:  "http://192.168.64.1:8081/v1",
		EnvHostModel:    "qwen3.8-27b-q4_k_m",
	}), nil)
	if len(got) != 1 {
		t.Fatalf("want exactly one host profile, got %d", len(got))
	}
	p := got[0]
	if p.Kind != "custom-openai" || p.Endpoint != "http://192.168.64.1:8081/v1" || p.Model != "qwen3.8-27b-q4_k_m" {
		t.Fatalf("unexpected profile %+v", p)
	}
	if p.Defaults["auth_scheme"] != "none" {
		t.Fatalf("auth_scheme = %v, want none", p.Defaults["auth_scheme"])
	}
	if p.Cred.Kind != "" {
		t.Fatalf("no credential was granted; profile must carry no reference, got %+v", p.Cred)
	}
}

func TestHostProviders_CustomOpenAIWithoutBaseURLIsUnconfigured(t *testing.T) {
	got := HostProviders(getenvFrom(map[string]string{
		EnvHostProvider: "custom-openai",
		EnvHostModel:    "m",
	}), nil)
	if got != nil {
		t.Fatalf("missing base URL must yield no profile (boot unconfigured), got %+v", got)
	}
}

// ProbeHostProviders records the three-step probe verdict on the profile's
// CapabilityHints, reading the credential (if any) from the env and never
// touching conventional-kind profiles.
func TestProbeHostProviders_RecordsMatrixAsHints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Stream bool              `json:"stream"`
			Tools  []json.RawMessage `json:"tools"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Tools) > 0 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"noop","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
			return
		}
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n")
			_, _ = io.WriteString(w, "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer srv.Close()

	env := map[string]string{
		EnvHostProvider: "custom-openai",
		EnvHostBaseURL:  srv.URL + "/v1",
		EnvHostModel:    "m",
	}
	profiles := HostProviders(getenvFrom(env), nil)
	other := corellm.ProviderProfile{ID: "anth", Kind: "anthropic", Model: "x"}
	profiles = append(profiles, other)

	got := ProbeHostProviders(context.Background(), nil, profiles, nil)
	if len(got) != 2 {
		t.Fatalf("want 2 profiles back, got %d", len(got))
	}
	h := got[0].CapabilityHints
	if !h[corellm.CapStreaming] || !h[corellm.CapToolCalling] || !h[corellm.CapUsageReporting] {
		t.Fatalf("expected all three hints true from the fake endpoint, got %+v", h)
	}
	if got[1].CapabilityHints != nil {
		t.Fatalf("conventional kinds must be untouched, got %+v", got[1].CapabilityHints)
	}
}

func TestProbeHostProviders_UnreachableEndpointLeavesHintsUnset(t *testing.T) {
	env := map[string]string{
		EnvHostProvider: "custom-openai",
		EnvHostBaseURL:  "http://127.0.0.1:1/v1",
		EnvHostModel:    "m",
	}
	got := ProbeHostProviders(context.Background(), nil, HostProviders(getenvFrom(env), nil), nil)
	if len(got) != 1 || got[0].CapabilityHints != nil {
		t.Fatalf("unreachable endpoint must leave hints unset (conservative), got %+v", got)
	}
}

// The boot log names the granted endpoint; it must never carry a key an
// operator embedded in the URL's userinfo.
func TestLogSafeEndpoint_MasksUserinfo(t *testing.T) {
	const key = "not-a-real-key-0123456789"
	for in, want := range map[string]string{
		"http://192.168.64.1:8081/v1":      "http://192.168.64.1:8081/v1",
		"https://user:" + key + "@host/v1": "https://xxxxx@host/v1",
		"https://" + key + "@host/v1":      "https://xxxxx@host/v1",
		"https://host/v1?api_key=" + key:   "https://host/v1",
		"https://host/v1?#frag" + key:      "https://host/v1",
		"http://[::1:8081/v1":              "<unparseable>",
	} {
		got := corellm.LogSafeEndpoint(in)
		if got != want {
			t.Errorf("corellm.LogSafeEndpoint(%q) = %q, want %q", in, got, want)
		}
	}
}

// A bearer grant: the probe resolves the env reference through credref
// (the registry's own path) and the key reaches the endpoint as a bearer
// header. A missing key var degrades to "no hints", never a panic or an
// unauthenticated probe.
func TestProbeHostProviders_BearerCredentialResolvesThroughCredref(t *testing.T) {
	const credVar = "KENAZ_TEST_HOST_PROBE_KEY"
	const key = "probe-test-not-a-real-key"
	t.Setenv(credVar, key)

	var mu sync.Mutex
	var sawAuth string
	auth := func() string { mu.Lock(); defer mu.Unlock(); return sawAuth }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sawAuth = r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	env := map[string]string{
		EnvHostProvider: "custom-openai",
		EnvHostBaseURL:  srv.URL + "/v1",
		EnvHostModel:    "m",
		EnvHostCredVar:  credVar,
		credVar:         key,
	}
	profiles := HostProviders(getenvFrom(env), nil)
	if len(profiles) != 1 || profiles[0].Cred.Locator != credVar {
		t.Fatalf("expected one bearer host profile, got %+v", profiles)
	}
	got := ProbeHostProviders(context.Background(), credref.New(secrets.NewMemoryBackend()), profiles, nil)
	if a := auth(); !strings.HasPrefix(a, "Bearer ") || strings.TrimPrefix(a, "Bearer ") != key {
		t.Fatalf("probe did not send the resolved key as a bearer token (got %d chars)", len(a))
	}
	if !got[0].CapabilityHints[corellm.CapStreaming] {
		t.Fatalf("expected streaming hint from the fake endpoint, got %+v", got[0].CapabilityHints)
	}

	// Unresolvable: the var named by the profile is unset in the process.
	t.Setenv(credVar, "")
	mu.Lock()
	sawAuth = ""
	mu.Unlock()
	again := ProbeHostProviders(context.Background(), credref.New(secrets.NewMemoryBackend()), HostProviders(getenvFrom(env), nil), nil)
	if again[0].CapabilityHints != nil || auth() != "" {
		t.Fatalf("an unresolvable credential must skip the probe, got hints=%+v (auth header sent: %t)", again[0].CapabilityHints, auth() != "")
	}
}

// No secret in the granted base URL (bare-username token, query-string
// key) or in a credential a misbehaving endpoint echoes back on a 401 may
// reach the boot log — on the auth-failure path or the dial-failure path.
func TestProbeHostProviders_NeverLogsEndpointSecrets(t *testing.T) {
	const credVar = "KENAZ_TEST_HOST_PROBE_BEARER"
	t.Setenv(credVar, "SECRET2")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A proxy that echoes the request's Authorization header in its
		// 401 body — the prober reads up to 1 KiB of it.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"bad key; you sent Authorization: `+r.Header.Get("Authorization")+`"}}`)
	}))
	defer srv.Close()

	for name, base := range map[string]string{
		"401 echoing Authorization": strings.Replace(srv.URL, "http://", "http://SECRETTOKEN@", 1) + "/v1?api_key=SECRET",
		"failing dial":              "http://SECRETTOKEN@127.0.0.1:1/v1?api_key=SECRET",
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			env := map[string]string{
				EnvHostProvider: "custom-openai",
				EnvHostBaseURL:  base,
				EnvHostModel:    "m",
				EnvHostCredVar:  credVar,
				credVar:         "SECRET2",
			}
			profiles := HostProviders(getenvFrom(env), log)
			if len(profiles) != 1 {
				t.Fatalf("expected one host profile, got %+v (log: %s)", profiles, buf.String())
			}
			got := ProbeHostProviders(context.Background(), credref.New(secrets.NewMemoryBackend()), profiles, log)
			if got[0].CapabilityHints != nil {
				t.Fatalf("a failed probe must leave hints unset, got %+v", got[0].CapabilityHints)
			}
			out := buf.String()
			if !strings.Contains(out, "probe_failed") {
				t.Fatalf("expected a probe_failed line, got: %s", out)
			}
			if strings.Contains(out, "SECRET") {
				t.Fatalf("boot log leaked a secret marker:\n%s", out)
			}
		})
	}
}

func TestProbeFailureReason_IsClassified(t *testing.T) {
	if got := probeFailureReason(&custom.ProbeAuthError{Status: 401, Message: "SECRET"}); got != "http_401" {
		t.Errorf("auth error → %q, want http_401", got)
	}
	if got := probeFailureReason(context.DeadlineExceeded); got != "timeout" {
		t.Errorf("deadline → %q, want timeout", got)
	}
	if got := probeFailureReason(errors.New("SECRET")); got != "probe_error" {
		t.Errorf("opaque error → %q, want probe_error", got)
	}
}
