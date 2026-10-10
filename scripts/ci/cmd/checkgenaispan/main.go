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
//  6. THE LOCAL STORE (llm-call-telemetry-01LLMTEL01 WP02): every column
//     any non-test Go file under core/ or cmd/ declares for the model_calls
//     table (CREATE TABLE model_calls / ALTER TABLE model_calls ADD COLUMN
//     in a string literal) is either a structural column
//     (genaitrace.RecordStructuralColumns) or bound to an allowlisted key
//     by genaitrace.RecordColumns; no column or mapping names content
//     (a segment content / message* / arguments / result(s) /
//     instructions / text / body / prompt(s) / completion(s) other than
//     a *_tokens count); every mapped column exists in the DDL. The store
//     can hold nothing the span allowlist would refuse.
//  7. SPAN SCOPE (Fleet/counsel D32, 2026-10-10): a key whose Spans list
//     excludes a span is never set on it. gen_ai.response.id (the
//     provider generation id, which the provider's own traces also carry)
//     is declared stored-record only and names no span; in the
//     instrumentation package every attrSet literal names its scope and
//     every literal-key write matches it; no attribute.String("gen_ai.…")
//     bypasses attrSet; and no semconv GenAIResponseID* selector appears
//     anywhere under core/ or cmd/.
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
	pkgDir     = "core/llm/genaitrace"
	attrsFile  = "core/llm/genaitrace/attrs.go"
	recordFile = "core/llm/genaitrace/record.go"
	storeTable = "model_calls"
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
	storeCreate    = regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?model_calls\s*\((.*?)\)\s*;`)
	storeAlter     = regexp.MustCompile(`(?i)ALTER\s+TABLE\s+model_calls\s+ADD\s+(?:COLUMN\s+)?([A-Za-z_][A-Za-z0-9_]*)`)
	storeMention   = regexp.MustCompile(`(?i)(CREATE\s+TABLE\s+(IF\s+NOT\s+EXISTS\s+)?model_calls\b|ALTER\s+TABLE\s+model_calls\b)`)
)

// storeDDL collects every model_calls column declaration found in the tree
// scan: column name -> "file:line" of the literal that declares it.
var storeDDL = map[string]string{}
var storeCreates int

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
	checkSpanScopes(fset, spanNames)
	scanned := scanTree(fset)
	storeCols := checkStore(fset, declared)

	if len(findings) > 0 {
		sort.Strings(findings)
		for _, f := range findings {
			fmt.Fprintln(os.Stderr, "[genai-span-no-content] FAIL:", f)
		}
		os.Exit(1)
	}
	fmt.Printf("[genai-span-no-content] allowlist: %d keys, all bounded, none content; instrumentation package: %d files; scanned %d files under core, cmd — one emitter, no semconv content keys, no side channels, no key off its span scope (gen_ai.response.id: stored record only); local store %s: %d columns, every one structural or an allowlisted key.\n",
		len(declared), pkgFiles, scanned, storeTable, storeCols)
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
					if v.Kind != token.STRING {
						return true
					}
					s, _ := strconv.Unquote(v.Value)
					collectStoreDDL(fset.Position(v.Pos()).String(), s)
					if inPkg {
						return true
					}
					if outsideKey.MatchString(s) {
						fail("%s: model-call span key literal %q outside %s — attributes have one writer (genaitrace.attrSet, which enforces the allowlist)", fset.Position(v.Pos()), s, pkgDir)
					}
				case *ast.SelectorExpr:
					if semconvContent.MatchString(v.Sel.Name) {
						fail("%s: semconv content key %s — model-call spans never carry content", fset.Position(v.Pos()), v.Sel.Name)
					}
					if semconvNeverOnSpan.MatchString(v.Sel.Name) {
						fail("%s: semconv %s — gen_ai.response.id is never a span attribute (Fleet/counsel D32); it reaches only the local model-call store, through genaitrace", fset.Position(v.Pos()), v.Sel.Name)
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

// collectStoreDDL records every model_calls column a string literal
// declares.
func collectStoreDDL(pos, lit string) {
	if !storeMention.MatchString(lit) {
		return
	}
	for _, m := range storeCreate.FindAllStringSubmatch(lit, -1) {
		storeCreates++
		for _, item := range splitTopLevel(m[1]) {
			fields := strings.Fields(item)
			if len(fields) == 0 {
				continue
			}
			switch strings.ToUpper(fields[0]) {
			case "PRIMARY", "UNIQUE", "CHECK", "FOREIGN", "CONSTRAINT":
				continue
			}
			storeDDL[strings.Trim(fields[0], "`\"[]")] = pos
		}
	}
	for _, m := range storeAlter.FindAllStringSubmatch(lit, -1) {
		storeDDL[m[1]] = pos
	}
}

// splitTopLevel splits a column list on commas outside parentheses.
func splitTopLevel(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// contentColumn reports why a store column name names content, or "".
func contentColumn(col string) string {
	c := strings.ToLower(col)
	segs := strings.Split(c, "_")
	counted := strings.HasSuffix(c, "_tokens")
	for _, seg := range segs {
		if contentSegs[seg] || strings.HasPrefix(seg, "message") {
			return "segment " + strconv.Quote(seg) + " names content"
		}
		switch seg {
		case "prompt", "prompts", "completion", "completions":
			if !counted {
				return "segment " + strconv.Quote(seg) + " names content (only a *_tokens count may)"
			}
		}
	}
	return ""
}

// checkStore applies check 6 and returns the number of store columns.
func checkStore(fset *token.FileSet, declared map[string]bool) int {
	f, err := parser.ParseFile(fset, recordFile, nil, 0)
	if err != nil {
		fail("parse %s: %v", recordFile, err)
		return 0
	}
	mapped := map[string]string{} // column -> key
	structural := map[string]bool{}
	foundMap, foundStruct := false, false
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range vs.Names {
			if i >= len(vs.Values) {
				continue
			}
			cl, ok := vs.Values[i].(*ast.CompositeLit)
			if !ok {
				continue
			}
			switch name.Name {
			case "RecordColumns":
				foundMap = true
				for _, el := range cl.Elts {
					ecl, ok := el.(*ast.CompositeLit)
					if !ok {
						fail("%s: RecordColumns element at %s is not a literal — entries must be statically checkable", recordFile, fset.Position(el.Pos()))
						continue
					}
					var col, key string
					for _, e := range ecl.Elts {
						kv, ok := e.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						id, _ := kv.Key.(*ast.Ident)
						bl, _ := kv.Value.(*ast.BasicLit)
						if id == nil || bl == nil || bl.Kind != token.STRING {
							continue
						}
						v, _ := strconv.Unquote(bl.Value)
						switch id.Name {
						case "Column":
							col = v
						case "Key":
							key = v
						}
					}
					if col == "" || key == "" {
						fail("%s: RecordColumns entry at %s needs string-literal Column and Key", recordFile, fset.Position(ecl.Pos()))
						continue
					}
					if _, dup := mapped[col]; dup {
						fail("%s: column %s mapped twice", recordFile, col)
					}
					mapped[col] = key
					if !declared[key] {
						fail("%s: column %s maps to %s, which is not an allowlisted attribute key", recordFile, col, key)
					}
					if why := contentColumn(col); why != "" {
						fail("%s: store column %s is a content column (%s)", recordFile, col, why)
					}
				}
			case "RecordStructuralColumns":
				foundStruct = true
				for _, el := range cl.Elts {
					bl, ok := el.(*ast.BasicLit)
					if !ok || bl.Kind != token.STRING {
						fail("%s: RecordStructuralColumns element at %s is not a string literal", recordFile, fset.Position(el.Pos()))
						continue
					}
					v, _ := strconv.Unquote(bl.Value)
					structural[v] = true
					if why := contentColumn(v); why != "" {
						fail("%s: structural column %s is a content column (%s)", recordFile, v, why)
					}
				}
			}
		}
		return true
	})
	if !foundMap || !foundStruct {
		fail("%s: var RecordColumns / RecordStructuralColumns literals not found — the store mapping moved; update this gate", recordFile)
		return 0
	}
	if storeCreates == 0 || len(storeDDL) == 0 {
		fail("no CREATE TABLE %s found under core, cmd — the store DDL moved or the scan looked at nothing; update this gate", storeTable)
		return 0
	}
	for col, pos := range storeDDL {
		if structural[col] {
			continue
		}
		if _, ok := mapped[col]; !ok {
			fail("%s: %s column %s is neither structural nor bound to an allowlisted key in %s RecordColumns — the local store may hold only what the span allowlist admits", pos, storeTable, col, recordFile)
		}
		if why := contentColumn(col); why != "" {
			fail("%s: %s column %s is a content column (%s)", pos, storeTable, col, why)
		}
	}
	for col := range mapped {
		if _, ok := storeDDL[col]; !ok {
			fail("%s: RecordColumns maps %s, but no %s DDL declares it — stale mapping", recordFile, col, storeTable)
		}
	}
	return len(storeDDL)
}

// ── check 7: span scope (Fleet/counsel D32, 2026-10-10) ─────────────────────

// neverOnSpan are keys that must never be an attribute of ANY span, only
// of the local stored record. gen_ai.response.id is the provider's
// generation id; the provider's own traces carry the same id, so on a
// member-attributed span it would allow a join that needs legal review.
var neverOnSpan = []string{"gen_ai.response.id"}

// attrSetWriters are the attrSet methods that set a key.
var attrSetWriters = map[string]bool{"str": true, "strs": true, "int": true, "posInt": true, "float": true, "bool": true}

// semconvNeverOnSpan matches semconv selectors for a neverOnSpan key
// (semconv.GenAIResponseID / GenAIResponseIDKey), which would set it on a
// span without a string literal the other checks could see.
var semconvNeverOnSpan = regexp.MustCompile(`^GenAIResponseID(Key)?$`)

// keySpanScopes resolves every SpanAttributes entry's Spans list to the
// scope strings it names (span names and/or StoredRecord's value), from
// the attrs.go source. It also returns attrs.go's string constants.
func keySpanScopes(fset *token.FileSet) (map[string][]string, map[string]string) {
	f, err := parser.ParseFile(fset, attrsFile, nil, 0)
	if err != nil {
		fail("parse %s: %v", attrsFile, err)
		return nil, nil
	}
	consts := map[string]string{}          // ident -> string value
	vars := map[string]*ast.CompositeLit{} // ident -> literal
	var lit *ast.CompositeLit
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range vs.Names {
			if i >= len(vs.Values) {
				continue
			}
			switch v := vs.Values[i].(type) {
			case *ast.BasicLit:
				if v.Kind == token.STRING {
					s, _ := strconv.Unquote(v.Value)
					consts[name.Name] = s
				}
			case *ast.CompositeLit:
				vars[name.Name] = v
				if name.Name == "SpanAttributes" {
					lit = v
				}
			}
		}
		return true
	})
	resolveList := func(e ast.Expr) ([]string, bool) {
		cl, ok := e.(*ast.CompositeLit)
		if id, isID := e.(*ast.Ident); isID {
			cl, ok = vars[id.Name]
		}
		if !ok {
			return nil, false
		}
		var out []string
		for _, el := range cl.Elts {
			switch v := el.(type) {
			case *ast.Ident:
				s, ok := consts[v.Name]
				if !ok {
					return nil, false
				}
				out = append(out, s)
			case *ast.BasicLit:
				s, _ := strconv.Unquote(v.Value)
				out = append(out, s)
			default:
				return nil, false
			}
		}
		return out, true
	}
	keySpans := map[string][]string{}
	if lit == nil {
		return keySpans, consts // checkAllowlist reports the missing literal
	}
	for _, el := range lit.Elts {
		cl, ok := el.(*ast.CompositeLit)
		if !ok {
			continue
		}
		var key string
		var spans ast.Expr
		for _, e := range cl.Elts {
			kv, ok := e.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			id, _ := kv.Key.(*ast.Ident)
			if id == nil {
				continue
			}
			switch id.Name {
			case "Key":
				if bl, ok := kv.Value.(*ast.BasicLit); ok {
					key, _ = strconv.Unquote(bl.Value)
				}
			case "Spans":
				spans = kv.Value
			}
		}
		if key == "" {
			continue
		}
		list, ok := resolveList(spans)
		if !ok {
			fail("%s: key %s has a Spans list this gate cannot resolve statically (use a []string{SpanName…, StoredRecord} var or literal)", attrsFile, key)
			continue
		}
		keySpans[key] = list
	}
	return keySpans, consts
}

// checkSpanScopes is check 7: a key whose Spans list excludes a span is
// never set on that span.
//
//	7a. every neverOnSpan key is declared, and its Spans names no span;
//	7b. in the instrumentation package every attrSet literal names its
//	    scope, and every attrSet write with a literal key (a.str("k", …))
//	    uses a key whose Spans include that attrSet's scope — so a
//	    stored-record-only key is never written into a span's set;
//	7c. the instrumentation package builds no attribute.KeyValue from a
//	    model-call key literal outside attrSet (attribute.String("k", …)),
//	    which would bypass the scope check;
//	7d. (scanTree) no semconv selector for a neverOnSpan key anywhere
//	    under core/ or cmd/.
func checkSpanScopes(fset *token.FileSet, spanNames map[string]bool) {
	keySpans, consts := keySpanScopes(fset)
	if keySpans == nil {
		return
	}
	for _, k := range neverOnSpan {
		spans, ok := keySpans[k]
		if !ok {
			fail("%s: allowlist lacks %s — it must stay declared (stored record only) so the local store can keep it", attrsFile, k)
			continue
		}
		for _, s := range spans {
			if spanNames[s] {
				fail("%s: key %s is carried on span %s — it must never be a span attribute (Fleet/counsel D32: the provider's own traces carry it); declare it Spans: storedOnly", attrsFile, k, s)
			}
		}
	}
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return // checkPackage reports it
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(pkgDir, name)
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			continue
		}
		for _, d := range f.Decls {
			checkScopesIn(fset, d, keySpans, consts, spanNames)
		}
	}
}

// checkScopesIn applies 7b and 7c to one top-level declaration.
func checkScopesIn(fset *token.FileSet, decl ast.Node, keySpans map[string][]string, consts map[string]string, spanNames map[string]bool) {
	scopeOf := map[string]string{} // attrSet variable -> scope value
	attrSetLit := func(e ast.Expr) (*ast.CompositeLit, bool) {
		if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.AND {
			e = u.X
		}
		cl, ok := e.(*ast.CompositeLit)
		if !ok {
			return nil, false
		}
		if id, ok := cl.Type.(*ast.Ident); !ok || id.Name != "attrSet" {
			return nil, false
		}
		return cl, true
	}
	litScope := func(cl *ast.CompositeLit) (string, bool) {
		for _, e := range cl.Elts {
			kv, ok := e.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if id, ok := kv.Key.(*ast.Ident); !ok || id.Name != "scope" {
				continue
			}
			switch v := kv.Value.(type) {
			case *ast.Ident:
				s, ok := consts[v.Name]
				return s, ok
			case *ast.BasicLit:
				s, _ := strconv.Unquote(v.Value)
				return s, true
			}
		}
		return "", false
	}
	bind := func(lhs, rhs ast.Expr) {
		id, ok := lhs.(*ast.Ident)
		if !ok {
			return
		}
		if cl, ok := attrSetLit(rhs); ok {
			if s, ok := litScope(cl); ok {
				scopeOf[id.Name] = s
			}
		}
	}
	ast.Inspect(decl, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CompositeLit:
			if _, ok := attrSetLit(v); ok {
				if _, ok := litScope(v); !ok {
					fail("%s: attrSet literal without a resolvable scope — every attribute set names the span (or StoredRecord) it is for, so a key off its span is refused", fset.Position(v.Pos()))
				}
			}
		case *ast.AssignStmt:
			for i := range v.Lhs {
				if i < len(v.Rhs) {
					bind(v.Lhs[i], v.Rhs[i])
				}
			}
		case *ast.ValueSpec:
			for i := range v.Names {
				if i < len(v.Values) {
					bind(v.Names[i], v.Values[i])
				}
			}
		case *ast.CallExpr:
			sel, ok := v.Fun.(*ast.SelectorExpr)
			if !ok || len(v.Args) == 0 {
				return true
			}
			bl, ok := v.Args[0].(*ast.BasicLit)
			if !ok || bl.Kind != token.STRING {
				return true
			}
			key, _ := strconv.Unquote(bl.Value)
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "attribute" {
				if strings.HasPrefix(key, "gen_ai.") || strings.HasPrefix(key, "kenaz.") || key == "error.type" {
					fail("%s: attribute.%s(%q, …) outside attrSet — model-call attributes are built only by attrSet, which enforces each key's span scope", fset.Position(v.Pos()), sel.Sel.Name, key)
				}
				return true
			}
			recv, ok := sel.X.(*ast.Ident)
			if !ok || !attrSetWriters[sel.Sel.Name] {
				return true
			}
			scope, ok := scopeOf[recv.Name]
			if !ok {
				return true
			}
			spans, declared := keySpans[key]
			if !declared {
				return true // check 2 reports an undeclared literal
			}
			for _, s := range spans {
				if s == scope {
					return true
				}
			}
			what := "scope"
			if spanNames[scope] {
				what = "span"
			}
			fail("%s: key %s set on an attrSet scoped to %s %s, but its Spans (%s) exclude it — a key whose scope excludes a span is never set on that span", fset.Position(v.Pos()), key, what, scope, strings.Join(spans, ", "))
		}
		return true
	})
}
