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
