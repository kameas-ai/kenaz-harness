package serve

// TestServedCSP_MatchesViteConfig enforces the invariant this file's own
// servedCSP comment (server.go:95-96) and frontend/vite.config.ts's own
// SERVED_CSP comment (:13) both assert in prose and nothing previously
// enforced: "must be kept in sync". entry-points-and-crash-reporting-
// 01PMZD13 UNIT-5, N-3. Before this test, the identifiers servedCSP /
// SERVED_CSP appeared at six sites total across the two files — none of
// them a test — so the two policies could drift silently. This makes one
// of the two mutual comments machine-checked; run from core/serve so
// pr.yml's existing `go test ./core/...` picks it up with no workflow
// change.
//
// Parses frontend/vite.config.ts's SERVED_CSP + THEME_SCRIPT_HASH
// declarations with targeted string extraction rather than a real
// TypeScript parser (no such dependency exists in this Go module) and
// asserts the reconstructed string equals servedCSP byte-for-byte.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var quotedLiteralRe = regexp.MustCompile("`([^`]*)`|\"([^\"]*)\"")

// extractConstBlock returns everything between `const <name> =` and the
// next top-level (i.e. not inside a quoted string) `;` in src — the span
// of a (possibly multi-line, possibly `+`-concatenated) JS/TS const
// declaration. The CSP values themselves contain `;` as a directive
// separator (`default-src 'none'; connect-src ...`), so a naive
// strings.Index(rest, ";") truncates mid-string — this scans char by char
// and only treats `;` outside any backtick/double-quote span as the
// statement terminator.
func extractConstBlock(t *testing.T, src, name string) string {
	t.Helper()
	marker := "const " + name + " ="
	start := strings.Index(src, marker)
	if start == -1 {
		t.Fatalf("could not find %q in vite.config.ts", marker)
	}
	rest := src[start+len(marker):]

	var quote byte
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '`', '"', '\'':
			quote = c
		case ';':
			return rest[:i]
		}
	}
	t.Fatalf("could not find terminating top-level ';' for %s in vite.config.ts", name)
	return ""
}

// concatQuotedLiterals joins every backtick- or double-quote-delimited
// string segment found in block, in order — the JS/TS `+`-concatenation
// this repo's CSP constants use, reduced to its string content.
func concatQuotedLiterals(block string) string {
	matches := quotedLiteralRe.FindAllStringSubmatch(block, -1)
	var sb strings.Builder
	for _, m := range matches {
		if m[1] != "" {
			sb.WriteString(m[1])
		} else {
			sb.WriteString(m[2])
		}
	}
	return sb.String()
}

// extractGoConstConcat reads a Go `const <name> = "..." + "..." + "..."`
// declaration (each segment on its own line, all but the last ending in
// ` +`) and returns the concatenated string content. Unlike TS/JS, Go
// source has no explicit statement-terminating `;` to search for
// (automatic semicolon insertion at newlines) — extractConstBlock's
// approach does not apply here, which is why this is a separate function
// rather than a shared one.
func extractGoConstConcat(t *testing.T, src, name string) string {
	t.Helper()
	marker := "const " + name + " = "
	start := strings.Index(src, marker)
	if start == -1 {
		t.Fatalf("could not find %q in csp_serve.go", marker)
	}
	rest := src[start+len(marker):]

	lineRe := regexp.MustCompile(`^"((?:[^"\\]|\\.)*)"\s*\+?\s*$`)
	var sb strings.Builder
	for _, line := range strings.Split(rest, "\n") {
		trimmed := strings.TrimSpace(line)
		m := lineRe.FindStringSubmatch(trimmed)
		if m == nil {
			break
		}
		sb.WriteString(m[1])
		if !strings.HasSuffix(trimmed, "+") {
			// Last segment — no trailing `+` continuation.
			break
		}
	}
	return sb.String()
}

func TestServedCSP_MatchesViteConfig(t *testing.T) {
	// core/serve/csp_parity_test.go -> ../../frontend/vite.config.ts
	viteConfigPath := filepath.Join("..", "..", "frontend", "vite.config.ts")
	data, err := os.ReadFile(viteConfigPath) //nolint:gosec // fixed repo-relative path, not user input
	if err != nil {
		t.Fatalf("reading %s: %v", viteConfigPath, err)
	}
	src := string(data)

	themeHashBlock := extractConstBlock(t, src, "THEME_SCRIPT_HASH")
	themeHash := concatQuotedLiterals(themeHashBlock)
	if themeHash == "" {
		t.Fatalf("extracted empty THEME_SCRIPT_HASH from vite.config.ts — parser is broken, not the source file")
	}

	servedBlock := extractConstBlock(t, src, "SERVED_CSP")
	served := concatQuotedLiterals(servedBlock)
	served = strings.ReplaceAll(served, "${THEME_SCRIPT_HASH}", themeHash)

	if served == "" {
		t.Fatalf("extracted empty SERVED_CSP from vite.config.ts — parser is broken, not the source file")
	}

	if served != servedCSP {
		t.Fatalf("core/serve/server.go's servedCSP has drifted from frontend/vite.config.ts's SERVED_CSP.\n"+
			"Both carry a comment saying they must be kept in sync — update BOTH together.\n\n"+
			"vite.config.ts SERVED_CSP (reconstructed):\n  %s\n\n"+
			"server.go servedCSP:\n  %s",
			served, servedCSP)
	}

	// The THIRD definition — core/rpc/csp_serve.go's productionCSP, the
	// Wails-free CSP middleware compiled only under -tags serve
	// (entry-points-and-crash-reporting-01PMZD13 UNIT-8). Read and parsed
	// as plain TEXT rather than imported, deliberately: importing
	// core/rpc from here (even from an external test package) would only
	// compile that file's contents under -tags serve, and no CI step runs
	// `go test -tags serve` — only `go build -tags serve` (UNIT-1's
	// serve-tag build step). A test gated the same way would never
	// actually execute in CI, which is the exact "gate that cannot fail"
	// class this campaign exists to end. Reading the source as text, the
	// same trick already used for vite.config.ts above, lets this
	// assertion run in the ordinary `go test ./core/...` step with no
	// new CI step and no build tag.
	cspServePath := filepath.Join("..", "rpc", "csp_serve.go")
	cspServeData, err := os.ReadFile(cspServePath) //nolint:gosec // fixed repo-relative path, not user input
	if err != nil {
		t.Fatalf("reading %s: %v", cspServePath, err)
	}
	productionCSP := extractGoConstConcat(t, string(cspServeData), "productionCSP")
	if productionCSP == "" {
		t.Fatalf("extracted empty productionCSP from csp_serve.go — parser is broken, not the source file")
	}
	if productionCSP != servedCSP {
		t.Fatalf("core/rpc/csp_serve.go's productionCSP has drifted from core/serve/server.go's servedCSP.\n"+
			"Both are the served-mode CSP policy — keep them identical.\n\n"+
			"csp_serve.go productionCSP:\n  %s\n\n"+
			"server.go servedCSP:\n  %s",
			productionCSP, servedCSP)
	}
}
