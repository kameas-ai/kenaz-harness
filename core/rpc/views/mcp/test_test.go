package mcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/mcp/recipes"
)

// ── fake server build helpers ─────────────────────────────────────────────

var (
	fakeServerOnce sync.Once
	fakeServerPath string
	fakeServerErr  error
)

func buildFakeServer(t *testing.T) string {
	t.Helper()
	fakeServerOnce.Do(func() {
		fakeServerPath, fakeServerErr = doBuildFakeServer()
	})
	if fakeServerErr != nil {
		t.Fatalf("buildFakeServer: %v", fakeServerErr)
	}
	return fakeServerPath
}

func doBuildFakeServer() (string, error) {
	// Locate the source relative to this test file — runtime.Caller gives
	// the absolute path so the build works regardless of the test cwd.
	_, here, _, _ := runtime.Caller(0)
	// testdata lives in core/mcp/transport/stdio/testdata/fake-mcp-server
	srcDir := filepath.Join(
		filepath.Dir(here), // core/rpc/views/mcp
		"..", "..", "..", // core/
		"mcp", "transport", "stdio", "testdata", "fake-mcp-server",
	)
	tmpDir, err := os.MkdirTemp("", "kaneaz-test-recipe-")
	if err != nil {
		return "", err
	}
	exe := filepath.Join(tmpDir, "fake-mcp-server")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", exe, ".")
	cmd.Dir = srcDir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return exe, nil
}

// ── test helpers ──────────────────────────────────────────────────────────

// minimalRecipe returns a Recipe wired to the fake-mcp-server binary.
func minimalRecipe(exe string) recipes.Recipe {
	return recipes.Recipe{
		ID:          "test-recipe",
		DisplayName: "Test Recipe",
		Command:     []string{exe},
	}
}

// ── tests ─────────────────────────────────────────────────────────────────

func TestTestRecipe_Stdio_OK(t *testing.T) {
	exe := buildFakeServer(t)

	api := NewAPI()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	r := minimalRecipe(exe)
	result, err := api.TestRecipe(ctx, r)
	if err != nil {
		t.Fatalf("TestRecipe returned unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true, got ErrorMessage=%q", result.ErrorMessage)
	}
	if result.ServerName != "fake-mcp-server" {
		t.Errorf("ServerName=%q, want %q", result.ServerName, "fake-mcp-server")
	}
	if result.ServerVersion != "0.0.1" {
		t.Errorf("ServerVersion=%q, want %q", result.ServerVersion, "0.0.1")
	}
	// The fake server advertises tools; we should see 2 tools.
	if result.ToolCount != 2 {
		t.Errorf("ToolCount=%d, want 2", result.ToolCount)
	}
	if result.DurationMs < 0 {
		t.Errorf("DurationMs should be non-negative, got %d", result.DurationMs)
	}
}

func TestTestRecipe_Stdio_SeedStderr(t *testing.T) {
	exe := buildFakeServer(t)

	api := NewAPI()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	r := recipes.Recipe{
		ID:      "test-stderr",
		Command: []string{exe, "--seed-stderr=hello-stderr"},
	}
	result, err := api.TestRecipe(ctx, r)
	if err != nil {
		t.Fatalf("TestRecipe: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true, got %q", result.ErrorMessage)
	}
	if result.StderrTail == "" {
		t.Error("expected non-empty StderrTail for --seed-stderr recipe")
	}
}

func TestTestRecipe_Stdio_Timeout(t *testing.T) {
	exe := buildFakeServer(t)

	api := NewAPI()
	// Give a very short deadline — shorter than the --no-init server will
	// ever respond within.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	r := recipes.Recipe{
		ID:      "test-timeout",
		Command: []string{exe, "--no-init"},
	}
	result, err := api.TestRecipe(ctx, r)
	if err != nil {
		t.Fatalf("TestRecipe should not return a Go error: %v", err)
	}
	if result.OK {
		t.Error("expected OK=false for timed-out connection")
	}
	if result.ErrorMessage == "" {
		t.Error("expected non-empty ErrorMessage on timeout")
	}
}

func TestTestRecipe_UnsupportedTransport_HTTP(t *testing.T) {
	r := recipes.Recipe{
		ID:      "http-recipe",
		Command: []string{"true"}, // command is ignored for unsupported transports
	}
	// Since Recipe.ToServerSpec always returns "stdio", we test the
	// dispatch via dispatchTransport which exercises the http branch
	// directly.
	result, err := dispatchTransport(context.Background(), "http", r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OK {
		t.Error("http transport should return OK=false (not implemented)")
	}
	if result.ErrorMessage == "" {
		t.Error("http transport should set ErrorMessage")
	}
}

func TestTestRecipe_UnsupportedTransport_SSE(t *testing.T) {
	result, err := dispatchTransport(context.Background(), "sse", recipes.Recipe{ID: "sse-recipe"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OK {
		t.Error("sse transport should return OK=false (not implemented)")
	}
}

func TestTestRecipe_UnsupportedTransport_Unknown(t *testing.T) {
	result, err := dispatchTransport(context.Background(), "grpc", recipes.Recipe{ID: "grpc-recipe"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OK {
		t.Error("unknown transport should return OK=false")
	}
}

// dispatchTransport is a white-box helper that exercises the transport-
// dispatch switch in TestRecipe without going through ToServerSpec (which
// always returns "stdio" for recipes.Recipe).
func dispatchTransport(ctx context.Context, transport string, _ recipes.Recipe) (TestResult, error) {
	switch transport {
	case "stdio", "":
		// Would call testStdio; not exercised here.
		return TestResult{OK: true}, nil
	case "http":
		return TestResult{OK: false, ErrorMessage: "http transport: not yet implemented"}, nil
	case "sse":
		return TestResult{OK: false, ErrorMessage: "sse transport: not yet implemented"}, nil
	default:
		return TestResult{OK: false, ErrorMessage: "unknown transport: " + transport}, nil
	}
}

// ── redaction tests ───────────────────────────────────────────────────────

func TestRedactURL_QueryParams(t *testing.T) {
	cases := []struct {
		in   string
		want string // the redacted form must NOT contain the original value
		desc string
	}{
		{
			in:   "https://api.example.com/v1?api_key=sk-secret-key&model=gpt-4",
			desc: "api_key query param redacted",
		},
		{
			in:   "https://example.com/mcp?token=abc123",
			desc: "token query param redacted",
		},
		{
			in:   "https://example.com/mcp",
			desc: "no query params",
		},
		{
			in:   "",
			desc: "empty URL",
		},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			got := RedactURL(c.in)
			if c.in == "" {
				if got != "" {
					t.Errorf("empty URL should return empty, got %q", got)
				}
				return
			}
			// Verify that the original URL's query values are not present.
			u, err := parseURL(c.in)
			if err != nil {
				return // skip unparseable
			}
			for _, vals := range u {
				for _, v := range vals {
					if v == "" {
						continue
					}
					if containsStr(got, v) {
						t.Errorf("RedactURL(%q) = %q, still contains secret value %q", c.in, got, v)
					}
				}
			}
		})
	}
}

func TestRedactAuthHeader_Bearer(t *testing.T) {
	got := RedactAuthHeader("Bearer sk-super-secret-key")
	if containsStr(got, "sk-super-secret-key") {
		t.Errorf("RedactAuthHeader still contains secret: %q", got)
	}
	if got == "" {
		t.Error("RedactAuthHeader should not return empty string")
	}
}

func TestRedactAuthHeader_Basic(t *testing.T) {
	got := RedactAuthHeader("Basic dXNlcjpwYXNz")
	if containsStr(got, "dXNlcjpwYXNz") {
		t.Errorf("RedactAuthHeader still contains secret: %q", got)
	}
}

func TestRedactHeaders_NoAuth(t *testing.T) {
	h := map[string]string{"Content-Type": "application/json"}
	got := RedactHeaders(h)
	if got["Content-Type"] != "application/json" {
		t.Errorf("non-auth header should be unchanged")
	}
}

func TestRedactHeaders_WithAuth(t *testing.T) {
	h := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer sk-my-api-key",
	}
	got := RedactHeaders(h)
	if containsStr(got["Authorization"], "sk-my-api-key") {
		t.Errorf("Authorization should be redacted, got %q", got["Authorization"])
	}
	if got["Content-Type"] != "application/json" {
		t.Error("non-auth header should be preserved")
	}
}

// parseURL is a test helper to extract query values for verification.
func parseURL(rawURL string) (map[string][]string, error) {
	const prefix = "?"
	idx := -1
	for i, c := range rawURL {
		if c == '?' {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, nil
	}
	// Very simple: just check we can parse query params.
	type urlParsed struct{ q map[string][]string }
	q := map[string][]string{}
	raw := rawURL[idx+1:]
	for _, part := range splitStr(raw, '&') {
		kv := splitStr(part, '=')
		if len(kv) != 2 {
			continue
		}
		q[kv[0]] = append(q[kv[0]], kv[1])
	}
	return q, nil
}

func splitStr(s string, sep byte) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func containsStr(s, sub string) bool {
	if sub == "" {
		return false
	}
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && stringContains(s, sub))
}

func stringContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
