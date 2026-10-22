package tooldisplay

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSummary_ShowsNonSecretScalars(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		tool string
		args map[string]any
		want string
	}{
		{"no args", "kenaz__glob", nil, "kenaz__glob()"},
		{"path", "kenaz__read_file", map[string]any{"path": "/workspace/src"}, "kenaz__read_file(path=/workspace/src)"},
		{"sorted mixed scalars", "kenaz__grep",
			map[string]any{"pattern": "TODO", "max": float64(20), "ignore_case": true, "glob": nil},
			"kenaz__grep(glob=null, ignore_case=true, max=20, pattern=TODO)"},
		{"float", "t", map[string]any{"ratio": 0.25}, "t(ratio=0.25)"},
		{"bash command", "kenaz__bash", map[string]any{"command": "go test ./core/..."},
			"kenaz__bash(command=go test ./core/...)"},
		{"git sha stays visible", "t", map[string]any{"rev": "9fceb02d0ae598e95dc970b74767f19372d61af8"},
			"t(rev=9fceb02d0ae598e95dc970b74767f19372d61af8)"},
		{"count named tokens is not a secret", "t", map[string]any{"max_tokens": float64(512)}, "t(max_tokens=512)"},
		{"author is not auth", "t", map[string]any{"author": "Nick"}, "t(author=Nick)"},
		{"url without credentials", "kenaz__web_fetch", map[string]any{"url": "https://example.com/a?b=c"},
			"kenaz__web_fetch(url=https://example.com/a?b=c)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Summary(tc.tool, tc.args); got != tc.want {
				t.Errorf("Summary = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSummary_RedactsSecretNames(t *testing.T) {
	t.Parallel()
	names := []string{
		"token", "api_key", "apiKey", "X-Api-Key", "secret", "client_secret",
		"password", "PASSWD", "passphrase", "auth", "Authorization", "credential",
		"credentials", "cookie", "Set-Cookie", "session", "session_id", "sessionToken",
		"bearer", "private_key", "privateKey", "access_token", "refresh-token", "key",
	}
	const value = "plain-looking-value"
	for _, n := range names {
		got := Summary("t", map[string]any{n: value})
		if strings.Contains(got, value) {
			t.Errorf("name %q: value shown: %q", n, got)
		}
		if want := "t(" + n + "=" + Redacted + ")"; got != want {
			t.Errorf("name %q: Summary = %q, want %q", n, got, want)
		}
	}
	// A secret-named key redacts the whole value, whatever its shape.
	got := Summary("t", map[string]any{"credentials": map[string]any{"user": "nick", "note": "x"}})
	if strings.Contains(got, "nick") {
		t.Errorf("object under a secret-named key leaked: %q", got)
	}
}

// Provider-shaped fixtures are split with + so push protection does not
// read these fake values as leaked credentials.
func TestSummary_RedactsSecretLookingValues(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		"openai":       "sk-" + "proj-abcdefghijklmnopqrstuvwxyz012345",
		"anthropic":    "sk-" + "ant-api03-abcdefghijklmnopqrstuvwx",
		"stripe":       "sk_" + "live_51H8xQOhSUPERSECRETvalue",
		"github":       "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789",
		"github fine":  "github_" + "pat_11ABCDEFG0abcdefghijklmnop",
		"slack":        "xox" + "b-1234567890-abcdefghij",
		"aws":          "AKIAIOSFODNN7EXAMPLE",
		"pem":          "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjE=",
		"jwt":          "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
		"bearer cmd":   `curl -H "Authorization: Bearer abcdefghijklmnop0123456789" https://x`,
		"assignment":   "export DB_PASSWORD=hunter2hunter2",
		"url userinfo": "postgres://admin:s3cr3tpass@db.internal:5432/app",
		"high entropy": "Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MGFiY2RlZkdISUpLTE1O",
		"embedded":     "deploy with key AKIAIOSFODNN7EXAMPLE now",
	}
	for name, v := range values {
		got := Summary("t", map[string]any{"text": v})
		if got != "t(text="+Redacted+")" {
			t.Errorf("%s: Summary = %q, want the value redacted", name, got)
		}
	}
}

func TestSummary_NestedObjectsAndArrays(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"headers": map[string]any{
			"Accept":        "application/json",
			"Authorization": "whatever",
		},
		"files": []any{"a.go", "b.go"},
		"deep":  map[string]any{"l1": map[string]any{"l2": map[string]any{"l3": "hidden-by-depth"}}},
		"list":  []any{map[string]any{"api_key": "plain", "name": "ok"}},
	}
	got := Summary("t", args)
	want := "t(deep={l1={l2={…}}}, files=[a.go, b.go], headers={Accept=application/json, Authorization=<redacted>}, " +
		"list=[{api_key=<redacted>, name=ok}])"
	if got != want {
		t.Errorf("Summary =\n %q\nwant\n %q", got, want)
	}
	if strings.Contains(got, "hidden-by-depth") || strings.Contains(got, "plain") {
		t.Errorf("leaked a nested value: %q", got)
	}
}

func TestSummary_WidthBound(t *testing.T) {
	t.Parallel()
	arr := make([]any, 12)
	for i := range arr {
		arr[i] = float64(i)
	}
	got := Summary("t", map[string]any{"n": arr})
	if want := "t(n=[0, 1, 2, 3, 4, 5, 6, 7, …+4])"; got != want {
		t.Errorf("Summary = %q, want %q", got, want)
	}
}

func TestSummary_TruncatesAndSingleLines(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("abc ", 100)
	got := Summary("t", map[string]any{"q": long})
	val := strings.TrimSuffix(strings.TrimPrefix(got, "t(q="), ")")
	if !strings.HasSuffix(val, "…") || utf8.RuneCountInString(val) != MaxValueRunes+1 {
		t.Errorf("value not truncated to %d runes + ellipsis: %q", MaxValueRunes, val)
	}

	got = Summary("t", map[string]any{"q": "line one\nline\ttwo\r\n\x1b[31m"})
	if got != "t(q=line one line two [31m)" {
		t.Errorf("not single-lined: %q", got)
	}

	many := map[string]any{}
	for i := 0; i < 40; i++ {
		many[strings.Repeat("k", 3)+string(rune('a'+i%26))+string(rune('a'+i/26))] = strings.Repeat("v", 60)
	}
	got = Summary("t", many)
	if n := utf8.RuneCountInString(got); n > MaxSummaryRunes {
		t.Errorf("summary is %d runes, want <= %d", n, MaxSummaryRunes)
	}
	if !strings.HasSuffix(got, "…)") {
		t.Errorf("capped summary should end with an ellipsis and paren: %q", got)
	}
}

func TestSummary_FailsClosedOnOddValues(t *testing.T) {
	t.Parallel()
	got := Summary("t", map[string]any{
		"raw":    string([]byte{0xff, 0xfe, 0x41}),
		"opaque": struct{ S string }{S: "x"},
	})
	if got != "t(opaque=<redacted>, raw=<redacted>)" {
		t.Errorf("Summary = %q", got)
	}
}

func TestSummary_CredentialFileTargetsHideContent(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"/workspace/.env", "app/.env.local", "/home/u/.ssh/config", "certs/server.pem", "~/.netrc"} {
		got := Summary("kenaz__write_file", map[string]any{"path": p, "content": "NAME=value"})
		want := "kenaz__write_file(content=<redacted>, path=" + p + ")"
		if got != want {
			t.Errorf("path %q: Summary = %q, want %q", p, got, want)
		}
	}
	got := Summary("kenaz__write_file", map[string]any{"path": "src/main.go", "content": "package main"})
	if got != "kenaz__write_file(content=package main, path=src/main.go)" {
		t.Errorf("ordinary file content hidden: %q", got)
	}
}

func TestSummary_ToolOptOutRendersTypesOnly(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"kenaz__list_secrets", "vault__write", "github__create_token", "shell__set_env", "op__store_credential"} {
		got := Summary(tool, map[string]any{"name": "visible?", "n": float64(1)})
		want := tool + "(n=<number>, name=<string>)"
		if got != want {
			t.Errorf("tool %q: Summary = %q, want %q", tool, got, want)
		}
	}
	if ToolOptsOut("kenaz__read_file") || ToolOptsOut("kenaz__bash") || ToolOptsOut("kenaz__web_fetch") {
		t.Error("ordinary builtins must not opt out")
	}
}

func TestSummaryJSON(t *testing.T) {
	t.Parallel()
	got, ok := SummaryJSON("kenaz__read_file", `{"path":"/workspace/src","limit":100,"token":"abc"}`)
	if !ok || got != "kenaz__read_file(limit=100, path=/workspace/src, token=<redacted>)" {
		t.Errorf("SummaryJSON = %q, %v", got, ok)
	}
	// Big integers keep their digits rather than float formatting...
	got, _ = SummaryJSON("t", `{"id":12345678901234567}`)
	if got != "t(id=12345678901234567)" {
		t.Errorf("integer formatting: %q", got)
	}
	// ...but an over-long number is an identifier and is redacted.
	got, _ = SummaryJSON("t", `{"acct":123456789012345678901234}`)
	if got != "t(acct=<redacted>)" {
		t.Errorf("long number: %q", got)
	}
	for _, bad := range []string{"", "null", "[1,2]", "not json"} {
		if _, ok := SummaryJSON("t", bad); ok {
			t.Errorf("SummaryJSON(%q) ok = true, want false", bad)
		}
	}
}

func TestIsSecretName_NotOverbroad(t *testing.T) {
	t.Parallel()
	for _, n := range []string{"path", "file_path", "url", "command", "pattern", "query", "max_tokens", "tokens_used", "author", "keyword", "monkey_id", "limit"} {
		if IsSecretName(n) {
			t.Errorf("IsSecretName(%q) = true, want false", n)
		}
	}
}
