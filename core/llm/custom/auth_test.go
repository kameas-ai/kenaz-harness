package custom

import (
	"net/http"
	"testing"
)

func newTestReq(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequest("POST", "http://localhost/v1/chat/completions", nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	req.Header = make(http.Header)
	return req
}

// TestApplyAuthBearer verifies bearer scheme sets Authorization header.
func TestApplyAuthBearer(t *testing.T) {
	req := newTestReq(t)
	cred := []byte("sk-testkey")
	if err := ApplyAuth(req, AuthSchemeBearer, cred, ""); err != nil {
		t.Fatalf("ApplyAuth: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer sk-testkey" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer sk-testkey")
	}
}

// TestApplyAuthAPIKeyHeader verifies api-key-header scheme uses X-API-Key default.
func TestApplyAuthAPIKeyHeader(t *testing.T) {
	req := newTestReq(t)
	if err := ApplyAuth(req, AuthSchemeAPIKeyHeader, []byte("mykey"), ""); err != nil {
		t.Fatalf("ApplyAuth: %v", err)
	}
	if got := req.Header.Get("X-API-Key"); got != "mykey" {
		t.Errorf("X-API-Key = %q, want %q", got, "mykey")
	}
}

// TestApplyAuthAPIKeyHeaderCustomName verifies explicit header name.
func TestApplyAuthAPIKeyHeaderCustomName(t *testing.T) {
	req := newTestReq(t)
	if err := ApplyAuth(req, AuthSchemeAPIKeyHeader, []byte("mykey"), "X-Custom-Auth"); err != nil {
		t.Fatalf("ApplyAuth: %v", err)
	}
	if got := req.Header.Get("X-Custom-Auth"); got != "mykey" {
		t.Errorf("X-Custom-Auth = %q, want %q", got, "mykey")
	}
}

// TestApplyAuthCustom verifies custom scheme requires authHeader.
func TestApplyAuthCustom(t *testing.T) {
	req := newTestReq(t)
	if err := ApplyAuth(req, AuthSchemeCustom, []byte("tok"), "X-My-Token"); err != nil {
		t.Fatalf("ApplyAuth: %v", err)
	}
	if got := req.Header.Get("X-My-Token"); got != "tok" {
		t.Errorf("X-My-Token = %q, want tok", got)
	}
}

// TestApplyAuthCustomMissingHeader verifies ErrInvalidConfig when authHeader is empty.
func TestApplyAuthCustomMissingHeader(t *testing.T) {
	req := newTestReq(t)
	err := ApplyAuth(req, AuthSchemeCustom, []byte("tok"), "")
	if err == nil {
		t.Fatal("expected error for custom scheme with empty header, got nil")
	}
}

// TestApplyAuthNoneOK verifies no-auth succeeds without credentials.
func TestApplyAuthNoneOK(t *testing.T) {
	req := newTestReq(t)
	if err := ApplyAuth(req, AuthSchemeNone, nil, ""); err != nil {
		t.Fatalf("ApplyAuth none: %v", err)
	}
	// No Authorization header should be set.
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want empty", got)
	}
}

// TestApplyAuthEmptyCredBearer verifies ErrInvalidConfig for empty key.
func TestApplyAuthEmptyCredBearer(t *testing.T) {
	req := newTestReq(t)
	err := ApplyAuth(req, AuthSchemeBearer, nil, "")
	if err == nil {
		t.Fatal("expected ErrInvalidConfig for empty bearer cred, got nil")
	}
	var ic *ErrInvalidConfig
	if !isInvalidConfig(err, &ic) {
		t.Errorf("expected *ErrInvalidConfig, got %T: %v", err, err)
	}
}

// TestApplyAuthEmptyCredAPIKey verifies ErrInvalidConfig for empty key.
func TestApplyAuthEmptyCredAPIKey(t *testing.T) {
	req := newTestReq(t)
	err := ApplyAuth(req, AuthSchemeAPIKeyHeader, []byte{}, "X-API-Key")
	if err == nil {
		t.Fatal("expected ErrInvalidConfig for empty api-key-header cred, got nil")
	}
}

// TestApplyAuthNoneWithCred verifies none scheme does NOT error even with a non-nil cred.
func TestApplyAuthNoneWithCred(t *testing.T) {
	req := newTestReq(t)
	// Caller should check ErrKeyAgainstNone separately; ApplyAuth itself does not error.
	if err := ApplyAuth(req, AuthSchemeNone, []byte("some-key"), ""); err != nil {
		t.Fatalf("ApplyAuth none with cred: %v", err)
	}
}

// TestZeroOverwritesInPlace verifies Zero zeroes every byte.
func TestZeroOverwritesInPlace(t *testing.T) {
	buf := []byte("supersecret12345")
	Zero(buf)
	for i, b := range buf {
		if b != 0 {
			t.Errorf("buf[%d] = %d, want 0", i, b)
		}
	}
}

// isInvalidConfig is a helper that sets target to err if err matches *ErrInvalidConfig.
func isInvalidConfig(err error, target **ErrInvalidConfig) bool {
	ic, ok := err.(*ErrInvalidConfig)
	if ok {
		*target = ic
	}
	return ok
}
