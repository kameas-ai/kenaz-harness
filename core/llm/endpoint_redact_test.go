package llm

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"testing"
)

func TestLogSafeEndpoint(t *testing.T) {
	cases := map[string]string{
		"https://TOKEN@host/v1":                 "https://xxxxx@host/v1",
		"http://user:pass@host:8080/v1?x=1":     "http://xxxxx@host:8080/v1",
		"https://host/v1?api_key=K#frag":        "https://host/v1",
		"https://host/v1?":                      "https://host/v1",
		"http://[::1]:namedport/v1?api_key=SEC": "<unparseable>",
	}
	for in, want := range cases {
		if got := LogSafeEndpoint(in); got != want {
			t.Errorf("LogSafeEndpoint(%q) = %q, want %q", in, got, want)
		}
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestRedactURLError_KeepsIdentityDropsURL(t *testing.T) {
	raw := "https://TOKEN@host/v1/chat/completions?api_key=SECRET"
	ue := &url.Error{Op: "Post", URL: raw, Err: context.DeadlineExceeded}
	red := RedactURLError(ue)
	if strings.Contains(red.Error(), "TOKEN") || strings.Contains(red.Error(), "SECRET") {
		t.Fatalf("redacted error leaks: %q", red.Error())
	}
	if want := "Post https://xxxxx@host/v1/chat/completions: timeout"; red.Error() != want {
		t.Errorf("got %q, want %q", red.Error(), want)
	}
	if !errors.Is(red, context.DeadlineExceeded) {
		t.Error("errors.Is(DeadlineExceeded) lost")
	}

	te := NewTransportError("p: ", &url.Error{Op: "Get", URL: raw, Err: timeoutErr{}})
	var ne net.Error
	if !errors.As(te, &ne) || !ne.Timeout() {
		t.Error("net.Error timeout verdict lost through NewTransportError")
	}
	if strings.Contains(te.Error(), "SECRET") || te.Status != 0 {
		t.Errorf("unexpected transport error: %v", te)
	}

	other := errors.New("plain")
	if RedactURLError(other) != other {
		t.Error("non-url errors must pass through unchanged")
	}
}

func TestScrubSecrets(t *testing.T) {
	got := ScrubSecrets("X-Custom-Key: abcd1234 and ab", "abcd1234", "ab")
	if strings.Contains(got, "abcd1234") || !strings.Contains(got, "and ab") {
		t.Errorf("ScrubSecrets = %q", got)
	}
}

func TestLogSafeEndpoint_ParamsAndOpaque(t *testing.T) {
	cases := map[string]string{
		"https://host/v1;key=SECRET/chat;x=y": "https://host/v1/chat",
		"custom:SECRET@x":                     "custom:xxxxx",
		"https://host/openai/v1":              "https://host/openai/v1",
	}
	for in, want := range cases {
		if got := LogSafeEndpoint(in); got != want {
			t.Errorf("LogSafeEndpoint(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEndpointSecrets_RawAndDecodedFormsAndUserinfoFloor(t *testing.T) {
	got := EndpointSecrets("https://user:pa%2Bss12345@host/v1?sig=abc%2Bdef12&api_key=K3y")
	has := map[string]bool{}
	for _, s := range got {
		has[s] = true
	}
	for _, want := range []string{"pa+ss12345", "pa%2Bss12345", "abc%2Bdef12", "abc+def12", "K3y"} {
		if !has[want] {
			t.Errorf("EndpointSecrets missing %q (got %q)", want, got)
		}
	}
	if has["user"] {
		t.Errorf("short userinfo %q must not be an exact-scrub secret", "user")
	}
	msg := ScrubSecrets("signature abc%2Bdef12 (abc+def12) for user", got...)
	if strings.Contains(msg, "abc") || !strings.Contains(msg, "for user") {
		t.Errorf("ScrubSecrets = %q", msg)
	}
}

func TestExtractProviderMessage_Shapes(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"openai":       {`{"error":{"message":"maximum context length is 4096","type":"invalid_request_error"}}`, "invalid_request_error: maximum context length is 4096"},
		"tgi":          {`{"error":"Input validation error: maximum context","error_type":"validation"}`, "Input validation error: maximum context"},
		"vllm-old":     {`{"object":"error","message":"maximum context length is 2048"}`, "maximum context length is 2048"},
		"fastapi":      {`{"detail":"prompt is too long"}`, "prompt is too long"},
		"fastapi-list": {`{"detail":[{"loc":["body"],"msg":"field required"},{"msg":"too long"}]}`, "field required; too long"},
		"plain":        {"  This model's maximum\n context length is 4096 tokens ", "This model's maximum context length is 4096 tokens"},
		"html":         {"<html><head><style>p{}</style></head><body><h1>502 Bad Gateway</h1></body></html>", "502 Bad Gateway"},
	}
	for name, c := range cases {
		if got := ExtractProviderMessage([]byte(c.body)); got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
	long := strings.Repeat("é", 600)
	if got := ExtractProviderMessage([]byte(long)); len(got) > providerExcerptMaxBytes+len("…") {
		t.Errorf("excerpt not bounded: %d bytes", len(got))
	}
}

func TestClassifyStatus_ScrubsEchoedSecrets(t *testing.T) {
	endpoint := "https://LONGTOKEN123@host/v1?api_key=SECRETq"
	secrets := RequestSecrets(endpoint, []byte("CREDsecret9"))
	for _, body := range []string{
		"Authorization: Bearer CREDsecret9 GET /v1?api_key=SECRETq by LONGTOKEN123",
		`{"error":"X-Custom: CREDsecret9 ?api_key=SECRETq LONGTOKEN123"}`,
		`{"detail":[{"msg":"CREDsecret9 SECRETq LONGTOKEN123"}]}`,
	} {
		for _, status := range []int{400, 401, 429, 500} {
			err := ClassifyStatus(status, []byte(body), secrets...)
			for _, s := range []string{"CREDsecret9", "SECRETq", "LONGTOKEN123"} {
				if strings.Contains(err.Error(), s) {
					t.Errorf("status %d body %q: error leaks %q: %v", status, body, s, err)
				}
			}
		}
	}
	// Generic shapes are scrubbed even with no exact secrets.
	if err := ClassifyStatus(400, []byte("Authorization: Bearer abcdefghijkl")); strings.Contains(err.Error(), "abcdefghijkl") {
		t.Errorf("generic Bearer shape survived: %v", err)
	}
}
