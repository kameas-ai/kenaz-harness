// Command checkfleetcredlogs is the checker behind
// scripts/ci/check-no-credential-in-fleet-logs.sh
// (provider-credentials-client-01PCCLI01 WP04). Read that script's header
// for the defect class; this file is the mechanism.
//
// WHY AN AST PASS, NOT A GREP
// ---------------------------
// The first version was a perl regex over `ident.Info(`. It missed the
// repo's dominant spelling, `logging.L().Info(…)` (a call, not an
// identifier, before the dot), plus logger.With(…), a message built with
// fmt.Sprintf and logged later, and secret-named identifiers it had no
// word for (apiKey, key, pt, token). A reviewer planted ten violations and
// it caught none. Walking the AST gets the receiver shape right for free.
//
// Round 2 (analyze.go) adds go/types and intraprocedural taint: structs
// with secret fields rendered whole, errors built from tainted values,
// taint through assignments, folded constant concatenations and method
// values. See analyze.go's header.
//
// WHAT IS A LOG SINK
// ------------------
//   - <any receiver>.{Debug,Info,Warn,Error}[Context](…) with ≥1 argument
//     (so err.Error() is not one), and <recv>.{Log,LogAttrs,With,WithGroup}
//     when the receiver reads as a logger (logging.L(), log*, l, slog…);
//   - slog.{Debug,Info,Warn,Error}[Context], slog.{Log,LogAttrs,With},
//     and slog attribute constructors (String, Any, Group, …);
//   - log.{Print,Fatal,Panic}[f|ln];
//   - fmt.{Print,Fprint}[f|ln].
//
// fmt.Sprint[f|ln] is not a sink by itself; it is scanned when it appears
// inside a sink's arguments, or when its result is bound to a variable in
// the same function and that variable reaches a sink.
//
// WHAT FAILS INSIDE A SINK'S ARGUMENTS
// ------------------------------------
//
//	(a) a string literal that IS a forbidden attribute key (exact,
//	    case-insensitive): value, plaintext, secret, key, api_key, …;
//	(b) a string literal with a key-shaped value — the regexes are READ
//	    FROM core/fleet/secretshape.go (secretValuePatterns plus
//	    apiKeyShapeRegex) at run time, so the gate and the sync backstop
//	    cannot drift; the checker refuses to run if it finds fewer than
//	    ten patterns there;
//	(c) an identifier or field selector whose name reads as secret
//	    material (plaintext, pt, secret, value, key, apiKey, token, uck,
//	    locator, …; or ending in Key/Token/Secret/Plaintext/Password);
//	(d) string(…) conversion, .Bytes(), .Use(.
//
// A `cred-log-allow: <reason>` comment on any line the call spans opts it
// out.
//
// FLEET_CRED_LOGS_ALL=1 widens the scope to every non-test file under
// core/ — a diagnostic for measuring false positives, never used in CI.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var forbiddenKeys = map[string]bool{
	"value": true, "plaintext": true, "secret": true, "key": true, "api_key": true,
	"apikey": true, "locator": true, "cred_locator": true, "uck": true, "uck_auth": true,
	"uck_auth_hash": true, "ucek": true, "kek": true, "seed": true, "nonce": true,
	"sealed": true, "na": true, "nb": true, "token": true, "password": true,
	"credential": true, "cred": true, "pt": true,
}

// byteishNames are the generic names a decrypted/unwrapped []byte usually
// carries; string(<one of these>) in a log call is flagged even though the
// name alone is not secret-sounding (review case: string(v)).
var byteishNames = map[string]bool{
	"v": true, "b": true, "bs": true, "buf": true, "data": true, "raw": true, "out": true,
	"val": true, "p": true, "pt": true, "payload": true, "body": true, "dec": true,
	"decrypted": true, "opened": true, "plain": true, "plaintext": true, "unwrapped": true,
}

var forbiddenIdents = map[string]bool{
	"plaintext": true, "pt": true, "secret": true, "secrets": true, "credbytes": true,
	"cred": true, "creds": true, "value": true, "buf": true, "locator": true, "loc": true,
	"uck": true, "uckauth": true, "ucek": true, "kek": true, "seed": true, "nonce": true,
	"sealed": true, "key": true, "apikey": true, "token": true, "password": true,
	"plain": true, "raw": true, "na": true, "nb": true,
}

var forbiddenSuffixes = []string{"key", "token", "secret", "plaintext", "password", "locator"}

var levelMethods = map[string]bool{
	"Debug": true, "Info": true, "Warn": true, "Error": true,
	"DebugContext": true, "InfoContext": true, "WarnContext": true, "ErrorContext": true,
}

var loggerOnlyMethods = map[string]bool{"Log": true, "LogAttrs": true, "With": true, "WithGroup": true}

var pkgSinks = map[string]map[string]bool{
	"slog": {
		"Debug": true, "Info": true, "Warn": true, "Error": true,
		"DebugContext": true, "InfoContext": true, "WarnContext": true, "ErrorContext": true,
		"Log": true, "LogAttrs": true, "With": true,
		"String": true, "Any": true, "Int": true, "Int64": true, "Uint64": true, "Float64": true,
		"Bool": true, "Time": true, "Duration": true, "Group": true, "Attr": true,
	},
	"log": {
		"Print": true, "Printf": true, "Println": true, "Fatal": true, "Fatalf": true,
		"Fatalln": true, "Panic": true, "Panicf": true, "Panicln": true,
	},
	"fmt": {
		"Print": true, "Printf": true, "Println": true, "Fprint": true, "Fprintf": true, "Fprintln": true,
	},
}

var loggerRecv = regexp.MustCompile(`(?i)(^|\.)(l|log|logger|lg|slog)$|log|logging\.L\(\)`)

const scopeMarker = `\b(FleetCredentialStore|FleetCredentialLocator|FleetNamespaceOrg|FleetNamespaceUser|FleetPrincipal|NewFleetPrincipal|PurgeFleetCredentialsForSignOut|ReconcileFleetCredentialPrincipal)\b`

func main() {
	root := repoRoot()
	patterns, err := loadSecretPatterns(filepath.Join(root, "core/fleet/secretshape.go"))
	if err != nil {
		fail("%v", err)
	}
	files, err := scope(root)
	if err != nil {
		fail("%v", err)
	}
	if len(files) < 2 {
		fail("scope resolved to %d file(s); expected at least core/secrets/fleet_credentials.go and core/llm/credref/credref.go — a gate with nothing in scope passes vacuously", len(files))
	}
	hits, err := analyze(root, files, patterns)
	if err != nil {
		fail("%v", err)
	}
	if len(hits) > 0 {
		for _, h := range hits {
			fmt.Fprintf(os.Stderr, "[fleet-cred-logs] FAIL: %s\n", h)
		}
		fmt.Fprintln(os.Stderr, "[fleet-cred-logs] Fleet credential code must not log key material, locators or secret-typed values.")
		fmt.Fprintln(os.Stderr, "[fleet-cred-logs] Log the credential id only, or annotate the call with 'cred-log-allow: <reason>'.")
		os.Exit(1)
	}
	fmt.Printf("[fleet-cred-logs] clean — %d file(s) in the credential namespaces, %d secret-value patterns from core/fleet/secretshape.go, no credential reaches a log call.\n", len(files), len(patterns))
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "[fleet-cred-logs] FAIL: "+format+"\n", a...)
	os.Exit(1)
}

func repoRoot() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		fail("not inside the repository (git rev-parse failed): %v", err)
	}
	return strings.TrimSpace(string(out))
}

// loadSecretPatterns extracts every regexp.MustCompile literal from
// secretValuePatterns and the apiKeyShapeRegex constant.
func loadSecretPatterns(path string) ([]*regexp.Regexp, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	var out []*regexp.Regexp
	add := func(lit *ast.BasicLit) error {
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			return err
		}
		re, err := regexp.Compile(s)
		if err != nil {
			return err
		}
		out = append(out, re)
		return nil
	}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, sp := range gd.Specs {
			vs, ok := sp.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
				continue
			}
			switch vs.Names[0].Name {
			case "apiKeyShapeRegex":
				if lit, ok := vs.Values[0].(*ast.BasicLit); ok {
					if err := add(lit); err != nil {
						return nil, err
					}
				}
			case "secretValuePatterns":
				var walkErr error
				ast.Inspect(vs.Values[0], func(n ast.Node) bool {
					ce, ok := n.(*ast.CallExpr)
					if !ok || exprString(ce.Fun) != "regexp.MustCompile" || len(ce.Args) != 1 {
						return true
					}
					if lit, ok := ce.Args[0].(*ast.BasicLit); ok && walkErr == nil {
						walkErr = add(lit)
					}
					return true
				})
				if walkErr != nil {
					return nil, walkErr
				}
			}
		}
	}
	if len(out) < 10 {
		return nil, fmt.Errorf("found only %d secret-value patterns in %s (secretValuePatterns / apiKeyShapeRegex renamed?)", len(out), path)
	}
	return out, nil
}

func scope(root string) ([]string, error) {
	marker := regexp.MustCompile(scopeMarker)
	set := map[string]bool{}
	err := filepath.Walk(filepath.Join(root, "core"), func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if name := info.Name(); name == "testdata" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		base := filepath.Base(rel)
		dir := filepath.ToSlash(filepath.Dir(rel))
		switch {
		case dir == "core/secrets" && strings.HasPrefix(base, "fleet_credentials"),
			dir == "core/llm/credref",
			dir == "core/fleet" && strings.HasPrefix(base, "credential"),
			strings.HasPrefix(dir, "core/fleet/credentials"):
			set[rel] = true
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if marker.Match(b) || os.Getenv("FLEET_CRED_LOGS_ALL") == "1" {
			set[rel] = true
		}
		return nil
	})
	var out []string
	for f := range set {
		out = append(out, f)
	}
	sort.Strings(out)
	return out, err
}

// byteish reports whether string(e) plausibly renders secret bytes: a
// generic byte-buffer name, a secret-named value, or a .Bytes() view. A
// conversion of a named enum (string(kind), string(RungNone)) is not.
func byteish(e ast.Expr) bool {
	switch t := e.(type) {
	case *ast.Ident:
		return byteishNames[strings.ToLower(t.Name)] || secretName(t.Name)
	case *ast.SelectorExpr:
		return secretName(t.Sel.Name)
	case *ast.CallExpr:
		if sel, ok := t.Fun.(*ast.SelectorExpr); ok {
			return sel.Sel.Name == "Bytes"
		}
	case *ast.ParenExpr:
		return byteish(t.X)
	}
	return false
}

func secretName(name string) bool {
	l := strings.ToLower(name)
	if forbiddenIdents[l] {
		return true
	}
	for _, s := range forbiddenSuffixes {
		if strings.HasSuffix(l, s) && len(l) > len(s) {
			return true
		}
	}
	return false
}

func exprString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return exprString(t.X) + "." + t.Sel.Name
	case *ast.CallExpr:
		args := make([]string, 0, len(t.Args))
		for _, a := range t.Args {
			args = append(args, exprString(a))
		}
		return exprString(t.Fun) + "(" + strings.Join(args, ", ") + ")"
	case *ast.BasicLit:
		if t.Kind == token.STRING {
			return `"…"` // never echo a literal: it may be the secret
		}
		return t.Value
	case *ast.StarExpr:
		return "*" + exprString(t.X)
	case *ast.UnaryExpr:
		return t.Op.String() + exprString(t.X)
	case *ast.IndexExpr:
		return exprString(t.X) + "[" + exprString(t.Index) + "]"
	case *ast.ParenExpr:
		return "(" + exprString(t.X) + ")"
	}
	return "…"
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
