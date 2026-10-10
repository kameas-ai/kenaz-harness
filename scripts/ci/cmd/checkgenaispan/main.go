// Command checkgenaispan is the structural half of
// scripts/ci/check-genai-span-no-content.sh (llm-call-telemetry-01LLMTEL01
// WP01): model-call spans never carry content.
//
// Checks, all from the repo root:
//
//  1. ALLOWLIST (core/llm/genaitrace/attrs.go, var SpanAttributes): every
//     declared key is in the gen_ai.* / kenaz.* / error.type namespaces, is
//     not a content key (gen_ai.prompt*, gen_ai.completion*, or a segment
//     content / message* / arguments / result(s) / instructions / text /
//     body), has a known Type, and — for string and string[] — a bounded
//     Class (id, model, token, enum, hash); an enum carries its values.
//     There is no free-text class: an unknown Class identifier fails.
//  2. KEY LITERALS IN THE INSTRUMENTATION PACKAGE (non-test files of
//     core/llm/genaitrace): every "gen_ai.…" / "kenaz.…" string literal
//     outside the SpanAttributes literal is a declared key or a span name.
//  3. NO SECOND EMITTER: outside core/llm/genaitrace (and its read-only
//     test kit, genaitrace/spantest), no non-test Go file
//     under core/ or cmd/ contains a "gen_ai.…" or "kenaz.{llm,provider,
//     session,chat}.…" string literal — model-call attributes have one
//     writer, the allowlist-enforcing one.
//  4. NO SEMCONV CONTENT KEYS anywhere under core/ or cmd/: no selector
//     GenAI*{Messages,Instructions,Prompt,Completion,Arguments,Result,
//     Content}* (gen_ai.input.messages, gen_ai.system_instructions, …).
//  5. NO SIDE CHANNELS in core/llm/genaitrace: no AddEvent, RecordError or
//     AddLink call, and SetStatus only with an empty description.
//
// Exit 1 with one line per finding; exit 0 prints a summary.
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

const (
	pkgDir    = "core/llm/genaitrace"
	attrsFile = "core/llm/genaitrace/attrs.go"
)

var (
	allowedTypes   = map[string]bool{"TypeString": true, "TypeInt": true, "TypeFloat": true, "TypeBool": true, "TypeStringSlice": true}
	stringTypes    = map[string]bool{"TypeString": true, "TypeStringSlice": true}
	boundedClasses = map[string]bool{"ClassID": true, "ClassModel": true, "ClassToken": true, "ClassEnum": true, "ClassHash": true}
	contentSegs    = map[string]bool{
		"content": true, "arguments": true, "result": true, "results": true,
		"instructions": true, "system_instructions": true, "text": true, "body": true,
	}
	semconvContent = regexp.MustCompile(`^GenAI\w*(Messages|Instructions|Prompt|Completion|Arguments|Result|Content)\w*$`)
	outsideKey     = regexp.MustCompile(`^(gen_ai\.|kenaz\.(llm|provider|session|chat)\.)`)
)

var findings []string

func fail(format string, args ...any) { findings = append(findings, fmt.Sprintf(format, args...)) }

// contentKey reports why key is a content key, or "".
func contentKey(key string) string {
	k := strings.ToLower(key)
	if strings.HasPrefix(k, "gen_ai.prompt") || strings.HasPrefix(k, "gen_ai.completion") {
		return "gen_ai.prompt* / gen_ai.completion* is content"
	}
	for _, seg := range strings.Split(k, ".") {
		if contentSegs[seg] || strings.HasPrefix(seg, "message") {
			return "segment " + strconv.Quote(seg) + " names content"
		}
	}
	return ""
}

func main() {
	fset := token.NewFileSet()
	declared, spanNames := checkAllowlist(fset)
	pkgFiles := checkPackage(fset, declared, spanNames)
	scanned := scanTree(fset)

	if len(findings) > 0 {
		sort.Strings(findings)
		for _, f := range findings {
			fmt.Fprintln(os.Stderr, "[genai-span-no-content] FAIL:", f)
		}
		os.Exit(1)
	}
	fmt.Printf("[genai-span-no-content] allowlist: %d keys, all bounded, none content; instrumentation package: %d files; scanned %d files under core, cmd — one emitter, no semconv content keys, no side channels.\n",
		len(declared), pkgFiles, scanned)
}

// checkAllowlist parses SpanAttributes and returns its keys plus the span
// name constants.
func checkAllowlist(fset *token.FileSet) (map[string]bool, map[string]bool) {
	f, err := parser.ParseFile(fset, attrsFile, nil, 0)
	if err != nil {
		fail("parse %s: %v", attrsFile, err)
		return nil, nil
	}
	declared := map[string]bool{}
	spanNames := map[string]bool{}
	var lit *ast.CompositeLit
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.ValueSpec:
			for i, name := range v.Names {
				if i >= len(v.Values) {
					continue
				}
				if name.Name == "SpanAttributes" {
					if cl, ok := v.Values[i].(*ast.CompositeLit); ok {
						lit = cl
					}
				}
				if strings.HasPrefix(name.Name, "SpanName") {
					if bl, ok := v.Values[i].(*ast.BasicLit); ok && bl.Kind == token.STRING {
						s, _ := strconv.Unquote(bl.Value)
						spanNames[s] = true
					}
				}
			}
		}
		return true
	})
	if lit == nil {
		fail("%s: var SpanAttributes = []AttrSpec{…} literal not found — the allowlist moved; update this gate", attrsFile)
		return declared, spanNames
	}
	for _, el := range lit.Elts {
		cl, ok := el.(*ast.CompositeLit)
		if !ok {
			fail("%s: SpanAttributes element at %s is not a literal AttrSpec — entries must be statically checkable", attrsFile, fset.Position(el.Pos()))
			continue
		}
		fields := map[string]ast.Expr{}
		for _, e := range cl.Elts {
			if kv, ok := e.(*ast.KeyValueExpr); ok {
				if id, ok := kv.Key.(*ast.Ident); ok {
					fields[id.Name] = kv.Value
				}
			}
		}
		keyLit, ok := fields["Key"].(*ast.BasicLit)
		if !ok || keyLit.Kind != token.STRING {
			fail("%s: entry at %s has no string-literal Key", attrsFile, fset.Position(cl.Pos()))
			continue
		}
		key, _ := strconv.Unquote(keyLit.Value)
		if declared[key] {
			fail("%s: duplicate key %s", attrsFile, key)
		}
		declared[key] = true
		if !strings.HasPrefix(key, "gen_ai.") && !strings.HasPrefix(key, "kenaz.") && key != "error.type" {
			fail("%s: key %s is outside gen_ai.* / kenaz.* / error.type", attrsFile, key)
		}
		if why := contentKey(key); why != "" {
			fail("%s: key %s is a content key (%s) — model-call spans never carry content", attrsFile, key, why)
		}
		typ := identName(fields["Type"])
		if !allowedTypes[typ] {
			fail("%s: key %s has unknown Type %q", attrsFile, key, typ)
		}
		if stringTypes[typ] {
			class := identName(fields["Class"])
			if !boundedClasses[class] {
				fail("%s: string key %s has Class %q — every string attribute must be an id, model, token, enum or hash (there is no free-text class)", attrsFile, key, class)
			}
			if class == "ClassEnum" && fields["Enum"] == nil {
				fail("%s: enum key %s declares no Enum values", attrsFile, key)
			}
		}
	}
	for _, must := range []string{"gen_ai.operation.name", "gen_ai.response.id", "gen_ai.usage.input_tokens"} {
		if !declared[must] {
			fail("%s: allowlist lacks %s — the parse found the wrong literal or the span lost its core keys", attrsFile, must)
		}
	}
	if len(spanNames) == 0 {
		fail("%s: no SpanName* string constants found", attrsFile)
	}
	return declared, spanNames
}

// checkPackage applies checks 2 and 5 to the instrumentation package.
func checkPackage(fset *token.FileSet, declared, spanNames map[string]bool) int {
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		fail("read %s: %v", pkgDir, err)
		return 0
	}
	n := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(pkgDir, name)
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			fail("parse %s: %v", path, err)
			continue
		}
		n++
		inAllowlist := allowlistRange(f)
		ast.Inspect(f, func(node ast.Node) bool {
			switch v := node.(type) {
			case *ast.BasicLit:
				if v.Kind != token.STRING || (v.Pos() >= inAllowlist[0] && v.Pos() < inAllowlist[1]) {
					return true
				}
				s, _ := strconv.Unquote(v.Value)
				if (strings.HasPrefix(s, "gen_ai.") || strings.HasPrefix(s, "kenaz.")) && !declared[s] && !spanNames[s] {
					fail("%s: literal %q is not a declared attribute key or span name", fset.Position(v.Pos()), s)
				}
			case *ast.CallExpr:
				sel, ok := v.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "AddEvent", "RecordError", "AddLink":
					fail("%s: %s — model-call data travels in allowlisted attributes only (Fleet drops events and links)", fset.Position(v.Pos()), sel.Sel.Name)
				case "SetStatus":
					if len(v.Args) == 2 {
						if bl, ok := v.Args[1].(*ast.BasicLit); !ok || bl.Value != `""` {
							fail("%s: SetStatus with a description — status is code-only", fset.Position(v.Pos()))
						}
					}
				}
			}
			return true
		})
	}
	if n == 0 {
		fail("%s: no non-test Go files — the instrumentation package moved; update this gate", pkgDir)
	}
	return n
}

// allowlistRange returns the [start, end) positions of the SpanAttributes
// literal in f, or an empty range.
func allowlistRange(f *ast.File) [2]token.Pos {
	var r [2]token.Pos
	ast.Inspect(f, func(n ast.Node) bool {
		if vs, ok := n.(*ast.ValueSpec); ok {
			for i, name := range vs.Names {
				if name.Name == "SpanAttributes" && i < len(vs.Values) {
					r = [2]token.Pos{vs.Values[i].Pos(), vs.Values[i].End()}
				}
			}
		}
		return true
	})
	return r
}

// scanTree applies checks 3 and 4 to every non-test Go file under core/
// and cmd/.
func scanTree(fset *token.FileSet) int {
	n := 0
	for _, root := range []string{"core", "cmd"} {
		if _, err := os.Stat(root); err != nil {
			fail("scan root %s missing: %v", root, err)
			continue
		}
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				switch d.Name() {
				case "testdata", "node_modules", "vendor":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return nil
			}
			n++
			// The instrumentation package and its read-only test kit
			// (spantest: expected values, never emitted) are exempt.
			inPkg := strings.HasPrefix(filepath.ToSlash(filepath.Dir(path))+"/", pkgDir+"/")
			ast.Inspect(f, func(node ast.Node) bool {
				switch v := node.(type) {
				case *ast.BasicLit:
					if inPkg || v.Kind != token.STRING {
						return true
					}
					s, _ := strconv.Unquote(v.Value)
					if outsideKey.MatchString(s) {
						fail("%s: model-call span key literal %q outside %s — attributes have one writer (genaitrace.attrSet, which enforces the allowlist)", fset.Position(v.Pos()), s, pkgDir)
					}
				case *ast.SelectorExpr:
					if semconvContent.MatchString(v.Sel.Name) {
						fail("%s: semconv content key %s — model-call spans never carry content", fset.Position(v.Pos()), v.Sel.Name)
					}
				}
				return true
			})
			return nil
		})
	}
	if n < 100 {
		fail("scanned only %d files under core, cmd — the scan looked at nothing", n)
	}
	return n
}

func identName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	}
	return ""
}
