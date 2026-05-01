package openai

import (
	"net/http"
	"testing"
)

// TestNew_DefaultUsesKeepaliveTransport asserts that the
// production-default Adapter constructed with no options carries the
// shared httpx keepalive transport on its HTTP client. The default
// http.Client.Transport is nil; any non-nil value here means our
// custom transport landed.
func TestNew_DefaultUsesKeepaliveTransport(t *testing.T) {
	a := New()
	if a.httpc == nil {
		t.Fatal("New() left httpc nil")
	}
	if a.httpc.Transport == nil {
		t.Fatal("New() default httpc.Transport is nil; expected the keepalive transport from core/llm/httpx")
	}
}

// TestWithHTTPClient_OverridesKeepaliveDefault guarantees the
// WithHTTPClient option still wins.
func TestWithHTTPClient_OverridesKeepaliveDefault(t *testing.T) {
	custom := &http.Client{}
	a := New(WithHTTPClient(custom))
	if a.httpc != custom {
		t.Fatal("WithHTTPClient did not override the default client")
	}
	if a.httpc.Transport != nil {
		t.Fatal("WithHTTPClient client should be used as-is; non-nil Transport indicates the default leaked through")
	}
}
