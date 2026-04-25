// Package lint provides a go/analysis Analyzer that mechanically
// enforces the `[]byte`-only Secret invariant across `core/secrets/...`.
//
// The analyzer is the second of two layers (the first being code
// review) protecting against the kind of `string`-typed credential
// drift that turns FR-013 (zeroize after use) into a polite
// suggestion.
//
// Rules:
//
//   - Any struct field declared in a package whose import path is
//     under `core/secrets/...` whose name matches one of the
//     credential-shaped patterns (Secret, Credential, Key, Token,
//     Password, APIKey, ApiKey, Api_Key) and whose type is `string`
//     is flagged as `secret-bytes` violation.
//
//   - Any function in such a package that returns `string` and whose
//     name matches one of the credential-shaped patterns is flagged.
//
//   - Any conversion `string(secretBytes)` in a non-`_test.go` file
//     under `core/secrets/...` is flagged. This is best-effort static
//     reachability — the analyzer flags the conversion when the
//     source value is named in a way that suggests a secret. The plan
//     §8 R6 documents the residual gap (interfaces, generics).
//
// Escape hatch: a `//nolint:secret-bytes <justification>` directive
// on the same line is honoured ONLY in `_test.go` files and ONLY
// when the justification is non-empty.
//
// Spec mapping: FR-013 (precondition), NFR-003, plan §11.
package lint

import (
	"go/ast"
	"go/token"
	"go/types"
	"regexp"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// Analyzer is the go/analysis.Analyzer that callers register with
// golangci-lint or `singlechecker.Main`. The exported variable is the
// idiomatic shape for go/analysis.
var Analyzer = &analysis.Analyzer{
	Name: "secretbytes",
	Doc: "reports string-typed credential fields/returns and string(secretBytes) conversions" +
		" inside packages under core/secrets/...",
	Run: run,
}

// nameRE matches the field/function names we treat as credential-bearing.
var nameRE = regexp.MustCompile(`(?i)^(secret|credential|password|api[_]?key)$|(?i).*?(secret|credential|password|api[_]?key|token)$|^(?i)token$|^(?i)key$|^(?i).*key$`)

// secretSourceRE matches identifier names suggesting a `[]byte`
// originating from a Secret.Use closure (used by the
// string(secretBytes) check).
var secretSourceRE = regexp.MustCompile(`(?i)(secret|cred|key|token|password|apikey|api_key)`)

// inSecretsTree returns true when pkgPath is inside the harness's
// secrets module subtree. The analyzer only fires there; running it
// over every package would require operator policy decisions out of
// scope for this layer.
func inSecretsTree(pkgPath string) bool {
	// Match any module path that ends with `/core/secrets` or
	// `/core/secrets/...`. The check is anchored on the suffix to
	// keep it portable across module renames.
	if strings.HasSuffix(pkgPath, "/core/secrets") || strings.Contains(pkgPath, "/core/secrets/") {
		return true
	}
	// Also handle the case where the analyzer runs against the
	// secrets module by itself (no parent path component).
	return pkgPath == "core/secrets" || strings.HasPrefix(pkgPath, "core/secrets/")
}

// nolintRE matches the escape-hatch directive shape:
//
//	//nolint:secret-bytes <justification>
var nolintRE = regexp.MustCompile(`//\s*nolint:secret-bytes(?:\s+(.+))?`)

func run(pass *analysis.Pass) (interface{}, error) {
	if !inSecretsTree(pass.Pkg.Path()) {
		return nil, nil
	}

	for _, file := range pass.Files {
		isTestFile := strings.HasSuffix(pass.Fset.Position(file.Pos()).Filename, "_test.go")
		// Walk every node looking for the three rule sites.
		ast.Inspect(file, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.StructType:
				checkStructFields(pass, v, file, isTestFile)
			case *ast.FuncDecl:
				checkFuncReturn(pass, v, file, isTestFile)
			case *ast.CallExpr:
				checkStringConversion(pass, v, file, isTestFile)
			}
			return true
		})
	}
	return nil, nil
}

// hasNolintDirective scans the comments associated with the position
// (line) for an `//nolint:secret-bytes <justification>` directive. It
// returns (true, "") if the directive is present *without* a non-empty
// justification, and (true, j) when a justification is present.
func hasNolintDirective(file *ast.File, pos token.Pos, fset *token.FileSet) (present bool, justification string) {
	posLine := fset.Position(pos).Line
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			cLine := fset.Position(c.Pos()).Line
			if cLine != posLine {
				continue
			}
			if m := nolintRE.FindStringSubmatch(c.Text); m != nil {
				j := ""
				if len(m) > 1 {
					j = strings.TrimSpace(m[1])
				}
				return true, j
			}
		}
	}
	return false, ""
}

func suppressed(pass *analysis.Pass, pos token.Pos, isTestFile bool, file *ast.File) bool {
	if !isTestFile {
		return false
	}
	present, j := hasNolintDirective(file, pos, pass.Fset)
	return present && j != ""
}

func checkStructFields(pass *analysis.Pass, st *ast.StructType, file *ast.File, isTestFile bool) {
	if st.Fields == nil {
		return
	}
	for _, f := range st.Fields.List {
		ident, ok := f.Type.(*ast.Ident)
		if !ok {
			continue
		}
		if ident.Name != "string" {
			continue
		}
		for _, name := range f.Names {
			if !nameRE.MatchString(name.Name) {
				continue
			}
			if suppressed(pass, name.Pos(), isTestFile, file) {
				continue
			}
			pass.Report(analysis.Diagnostic{
				Pos:     name.Pos(),
				Message: "secret-bytes: credential-shaped field " + name.Name + " is string; use []byte (FR-013, D7)",
			})
		}
	}
}

func checkFuncReturn(pass *analysis.Pass, fd *ast.FuncDecl, file *ast.File, isTestFile bool) {
	if fd.Type == nil || fd.Type.Results == nil {
		return
	}
	if !nameRE.MatchString(fd.Name.Name) {
		return
	}
	for _, r := range fd.Type.Results.List {
		ident, ok := r.Type.(*ast.Ident)
		if !ok {
			continue
		}
		if ident.Name != "string" {
			continue
		}
		if suppressed(pass, fd.Name.Pos(), isTestFile, file) {
			continue
		}
		pass.Report(analysis.Diagnostic{
			Pos:     fd.Name.Pos(),
			Message: "secret-bytes: credential-shaped function " + fd.Name.Name + " returns string; return []byte (FR-013, D7)",
		})
		return
	}
}

func checkStringConversion(pass *analysis.Pass, ce *ast.CallExpr, file *ast.File, isTestFile bool) {
	if isTestFile {
		// string(secretBytes) is permitted in tests because tests
		// frequently need to compare against known cleartext values
		// to verify zeroization etc.
		return
	}
	id, ok := ce.Fun.(*ast.Ident)
	if !ok || id.Name != "string" {
		return
	}
	if len(ce.Args) != 1 {
		return
	}
	// Heuristic: the argument is a `[]byte`-typed identifier whose
	// name suggests a secret. We rely on the type info from go/types
	// when available and fall back to a name-based check.
	if tv, ok := pass.TypesInfo.Types[ce.Args[0]]; ok && tv.Type != nil {
		if !isByteSlice(tv.Type) {
			return
		}
	}
	if !argLooksLikeSecret(ce.Args[0]) {
		return
	}
	if suppressed(pass, ce.Pos(), isTestFile, file) {
		return
	}
	pass.Report(analysis.Diagnostic{
		Pos:     ce.Pos(),
		Message: "secret-bytes: string(...) conversion of credential-shaped []byte; keep secret as []byte (FR-013, D7)",
	})
}

func isByteSlice(t types.Type) bool {
	sl, ok := t.Underlying().(*types.Slice)
	if !ok {
		return false
	}
	b, ok := sl.Elem().Underlying().(*types.Basic)
	if !ok {
		return false
	}
	return b.Kind() == types.Byte || b.Kind() == types.Uint8
}

func argLooksLikeSecret(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.Ident:
		return secretSourceRE.MatchString(v.Name)
	case *ast.SelectorExpr:
		return secretSourceRE.MatchString(v.Sel.Name)
	}
	return false
}
