package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/credref"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// TestRegistry_FetchProviderStats_ResolvesCredentialPerCall
// (llm-call-telemetry-01LLMTEL01 WP02): the stats seam resolves the
// profile's own credential through the configured resolver for the one
// request, aims at the profile's endpoint, and refuses a provider kind that
// offers no stats pull without making a request.
func TestRegistry_FetchProviderStats_ResolvesCredentialPerCall(t *testing.T) {
	const key = "TEST_REG_OPENROUTER_STATS_KEY"
	t.Setenv(key, "sk-or-resolved")
	var gotAuth, gotID string
	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		gotAuth, gotID = r.Header.Get("Authorization"), r.URL.Query().Get("id")
		_, _ = w.Write([]byte(`{"data":{"latency":10,"native_tokens_prompt":5}}`))
	}))
	defer ts.Close()

	r, _ := newReg(t)
	r.resolver = credref.New(secrets.NewMemoryBackend())
	if err := r.LoadProfiles([]llm.ProviderProfile{
		{ID: "or", Kind: "openrouter", Model: "openai/gpt-4o", Endpoint: ts.URL + "/api/v1/chat/completions",
			Cred: llm.CredentialReference{Kind: "env", Locator: key}},
		{ID: "an", Kind: "anthropic", Model: "claude-sonnet-4-5",
			Cred: llm.CredentialReference{Kind: "env", Locator: key}},
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := r.FetchProviderStats(context.Background(), "or", "gen-reg-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer sk-or-resolved" || gotID != "gen-reg-1" || raw.LatencyMS == nil || *raw.LatencyMS != 10 ||
		raw.NativeInputTokens == nil || *raw.NativeInputTokens != 5 {
		t.Fatalf("auth=%q id=%q raw=%+v", gotAuth, gotID, raw)
	}
	if _, err := r.FetchProviderStats(context.Background(), "an", "msg_1"); err == nil {
		t.Fatal("a provider with no stats pull must be refused")
	}
	if _, err := r.FetchProviderStats(context.Background(), "missing", "x"); err == nil {
		t.Fatal("an unknown profile must be refused")
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1 (refusals make no request)", requests)
	}
}
