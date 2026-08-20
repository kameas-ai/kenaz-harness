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
}
