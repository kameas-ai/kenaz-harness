// Command checkdestructivemigrations is the Go half of
// scripts/ci/check-destructive-migration-coverage.sh (I14,
// upgrade-path-coverage-01PMUG01 WP03, spec.md FR-2).
//
// A migration is "destructive" if the SQL its Up function executes
// contains DROP TABLE, DROP INDEX, DROP TRIGGER, DROP COLUMN,
// ALTER TABLE ... RENAME, or DELETE FROM outside a scratch/backup
// table. Every destructive migration must have a populated-table test
// (a test file whose source references the migration's ID string) or a
// dated entry in scripts/ci/allowlists/i14-destructive-migration-
// coverage.txt.
//
// WHY GO, NOT GREP. Deciding "which SQL text belongs to migration X's
// Up function, not its Down function or an unrelated string" requires
// knowing Go syntax boundaries — a FuncLit's source span, plus
// whatever package-level consts/vars it references (this codebase's
// migrations sometimes hold their DDL in a var referenced by
// identifier — e.g. sessions/0335's sqlDropFTSTriggers []string, or a
// UpSource built from a `+`-concatenation of several sub-consts, e.g.
// sqlSearchFTSToolRowsSchema) — which grep cannot resolve.
// golang.org/x/tools is not needed (no type information required, just
// syntax), so this uses only go/parser + go/ast, keeping the
// self-hosted-runner footprint to `go build`.
//
// SCOPE. Only migrations.Migration composite literals assigned to a
// struct field named "Up" are scanned; "Down" bodies are explicitly
// out of scope (spec §3 Non-goals: Registry.Rollback has no production
// caller — runner.go:153, adapters.go:172, storage.go:214 are its only
// non-test references).
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var destructivePattern = regexp.MustCompile(
	`(?i)DROP\s+TABLE|DROP\s+INDEX|DROP\s+TRIGGER|DROP\s+COLUMN|ALTER\s+TABLE\s+\S+\s+RENAME`,
)

// deleteFromPattern is checked separately so a DELETE FROM targeting a
// scratch/backup table (this codebase's own convention — see
// artifactVersionsBackup0332 / artifactVersionsBackup0327) can be
// exempted, per spec's rule: "DELETE FROM outside an IF EXISTS on a
// scratch table."
var deleteFromPattern = regexp.MustCompile(`(?i)DELETE\s+FROM\s+` + "`" + `?"?'?(\w+)`)

type finding struct {
	id      string
	file    string
	reasons []string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "checkdestructivemigrations:", err)
		os.Exit(2)
	}
}

func run() error {
	if len(os.Args) < 4 {
		return fmt.Errorf("usage: checkdestructivemigrations <scan-root> <test-root> <allowlist-file>")
	}
	root := os.Args[1]
	testRoot := os.Args[2]
	allowPath := os.Args[3]

	findings, err := scan(root)
	if err != nil {
		return err
	}

	tested, err := idsReferencedByTests(testRoot)
	if err != nil {
		return err
	}

	allowed, err := readAllowlist(allowPath)
	if err != nil {
		return err
	}

	var violations []finding
	for _, f := range findings {
		if tested[f.id] {
			continue // covered by a populated-table test
		}
		if !allowed[f.id] {
			violations = append(violations, f)
		}
	}
	sort.Slice(violations, func(i, j int) bool { return violations[i].id < violations[j].id })

	if len(violations) > 0 {
		fmt.Println("DESTRUCTIVE MIGRATIONS WITHOUT COVERAGE:")
		for _, v := range violations {
			fmt.Printf("  %s (%s): %s\n", v.id, v.file, strings.Join(v.reasons, ", "))
		}
		fmt.Printf("\n%d destructive migration(s) need a populated-table test or a dated entry in %s\n",
			len(violations), allowPath)
		os.Exit(1)
	}

	fmt.Printf("clean — %d destructive migration(s) all covered.\n", len(findings))
	return nil
}

// scan walks root for non-test .go files, parses each, and returns one
// finding per destructive migration.Migration{...} literal found.
func scan(root string) ([]finding, error) {
	var out []finding
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		findings, findErr := scanFile(path)
		if findErr != nil {
			return fmt.Errorf("parse %s: %w", path, findErr)
		}
		out = append(out, findings...)
		return nil
	})
	return out, err
}

func scanFile(path string) ([]finding, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return nil, err
	}

	topLevel := topLevelExprs(file)
	resolveCache := map[string]string{}

	var out []finding
	ast.Inspect(file, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		var idExpr, upExpr, upSourceExpr ast.Expr
		hasID, hasUp := false, false
		for _, elt := range cl.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			switch key.Name {
			case "ID":
				hasID = true
				idExpr = kv.Value
			case "Up":
				hasUp = true
				upExpr = kv.Value
			case "UpSource":
				upSourceExpr = kv.Value
			}
		}
		if !hasID || !hasUp {
			return true
		}
		id := resolveExprString(idExpr, topLevel, resolveCache, 0)
		if id == "" {
			return true // not identifiable; skip rather than false-positive
		}

		var combined strings.Builder
		combined.WriteString(sourceSpan(fset, src, upExpr))
		combined.WriteString("\n")
		combined.WriteString(resolveExprString(upSourceExpr, topLevel, resolveCache, 0))
		combined.WriteString("\n")
		// Every bare identifier referenced inside the Up closure that
		// resolves to a top-level const/var (string, []string, or a
		// +-concatenation of either) contributes its resolved text too
		// — this is what catches sessions/0335's sqlDropFTSTriggers
		// (a []string var) and sqlSearchFTSToolRowsSchema (a
		// concatenation expression), neither of which is literal text
		// inside the Up closure itself.
		ast.Inspect(upExpr, func(n2 ast.Node) bool {
			id2, ok := n2.(*ast.Ident)
			if !ok {
				return true
			}
			if _, known := topLevel[id2.Name]; known {
				combined.WriteString(resolveExprString(id2, topLevel, resolveCache, 0))
				combined.WriteString("\n")
			}
			return true
		})

		reasons := destructiveReasons(combined.String())
		if len(reasons) == 0 {
			return true
		}
		out = append(out, finding{id: id, file: path, reasons: reasons})
		return true
	})
	return out, nil
}

func destructiveReasons(text string) []string {
	var reasons []string
	seen := map[string]bool{}
	for _, m := range destructivePattern.FindAllString(text, -1) {
		fields := strings.Fields(m)
		key := strings.ToUpper(strings.Join(fields[:min(2, len(fields))], " "))
		if !seen[key] {
			seen[key] = true
			reasons = append(reasons, key)
		}
	}
	for _, m := range deleteFromPattern.FindAllStringSubmatch(text, -1) {
		table := m[1]
		if strings.Contains(strings.ToLower(table), "backup") {
			continue // scratch table — exempt per spec's rule
		}
		key := "DELETE FROM " + table
		if !seen[key] {
			seen[key] = true
			reasons = append(reasons, key)
		}
	}
	return reasons
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// topLevelExprs maps every top-level const/var name in the file to its
// (unresolved) value expression, for on-demand recursive resolution.
func topLevelExprs(file *ast.File) map[string]ast.Expr {
	out := map[string]ast.Expr{}
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || (gd.Tok != token.CONST && gd.Tok != token.VAR) {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != len(vs.Values) {
				continue
			}
			for i, name := range vs.Names {
				out[name.Name] = vs.Values[i]
			}
		}
	}
	return out
}

// resolveExprString resolves expr to its best-effort string content:
// string literals verbatim, []string composite literals joined by
// newline, +-concatenations of either, and identifiers resolved
// through topLevel (memoised, depth-capped against accidental cycles).
func resolveExprString(expr ast.Expr, topLevel map[string]ast.Expr, cache map[string]string, depth int) string {
	if expr == nil || depth > 8 {
		return ""
	}
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return ""
		}
		if strings.HasPrefix(e.Value, "`") {
			return strings.Trim(e.Value, "`")
		}
		if s, err := strconv.Unquote(e.Value); err == nil {
			return s
		}
		return ""
	case *ast.Ident:
		if v, ok := cache[e.Name]; ok {
			return v
		}
		target, ok := topLevel[e.Name]
		if !ok {
			return ""
		}
		cache[e.Name] = "" // break cycles
		v := resolveExprString(target, topLevel, cache, depth+1)
		cache[e.Name] = v
		return v
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return ""
		}
		return resolveExprString(e.X, topLevel, cache, depth+1) + resolveExprString(e.Y, topLevel, cache, depth+1)
	case *ast.CompositeLit:
		var b strings.Builder
		for _, el := range e.Elts {
			b.WriteString(resolveExprString(el, topLevel, cache, depth+1))
			b.WriteString("\n")
		}
		return b.String()
	case *ast.ParenExpr:
		return resolveExprString(e.X, topLevel, cache, depth+1)
	default:
		return ""
	}
}

func sourceSpan(fset *token.FileSet, src []byte, expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	start := fset.Position(expr.Pos()).Offset
	end := fset.Position(expr.End()).Offset
	if start < 0 || end > len(src) || start > end {
		return ""
	}
	return string(src[start:end])
}

// idsReferencedByTests returns the set of migration ID strings
// ("sessions/0327-source-model-output", ...) that appear literally in
// any _test.go file under testRoot — the definition of "has a
// populated-table test" this gate uses. Deliberately a substring
// search over raw file bytes (not AST-scoped) since a test's rewind
// SQL, its own doc comment, and its assertions all legitimately
// reference the ID in different syntactic positions.
func idsReferencedByTests(testRoot string) (map[string]bool, error) {
	out := map[string]bool{}
	walkErr := filepath.Walk(testRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		text := string(data)
		for _, id := range knownMigrationIDPattern.FindAllString(text, -1) {
			out[id] = true
		}
		return nil
	})
	return out, walkErr
}

// knownMigrationIDPattern matches this repo's migration ID shape:
// "<mission>/<version>-<slug>", e.g. "sessions/0327-source-model-output"
// or "units/1103-conflict-edge".
var knownMigrationIDPattern = regexp.MustCompile(`[a-z][a-z-]*/[0-9]{3,4}-[a-z0-9-]+`)

func readAllowlist(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read allowlist %s: %w", path, err)
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	return out, nil
}
