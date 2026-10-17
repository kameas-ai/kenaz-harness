package main

// The type-aware pass (round 2 of the #424 review).
//
// Round 1 was a purely syntactic AST walk. Review found five shapes it
// could not see, all closed here with go/types and a small intraprocedural
// taint analysis:
//
//  1. a struct whose fields (or json tags) are secret-like, logged whole —
//     fmt.Sprintf("%+v", c), %v, %#v, or as a slog value "c", c;
//  2. an error built from a tainted value — errors.New(key),
//     fmt.Errorf("…%s", key) — then logged;
//  3. taint through assignment inside one function — s := string(buf);
//     log(s) / x := key; log(x) — through :=, = and var declarations;
//  4. a key literal split with + (constant folding before shape-matching);
//  5. a method value — f := l.Info; f("k", key).
//
// Round 3 (#424 follow-up): a credref.Cred — the resolved-credential
// handle — rendered whole (a log argument, %v into a logged string, a
// struct/slice/map holding one) or via its String/GoString method is a
// violation even though Cred now redacts under every verb. The rule is
// "never log a credential"; the redaction is the backstop for a miss,
// not a licence. Its metadata (ReferenceID()) is fine.
//
// The analysis is intraprocedural on purpose: a value laundered through a
// helper function's return is caught only because any call with a tainted
// argument is itself treated as tainted (conservative), not because the
// helper is followed.

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

type analyzer struct {
	root     string
	fset     *token.FileSet
	info     *types.Info
	patterns []*regexp.Regexp

	tainted   map[any]string
	sinkFuncs map[any]bool
	allow     map[int]bool
	rel       string
	hits      []string
}

func analyze(root string, files []string, patterns []*regexp.Regexp) ([]string, error) {
	inScope := map[string]bool{}
	dirs := map[string]bool{}
	for _, f := range files {
		inScope[filepath.Join(root, f)] = true
		dirs["./"+filepath.ToSlash(filepath.Dir(f))] = true
	}
	var patternsToLoad []string
	for d := range dirs {
		patternsToLoad = append(patternsToLoad, d)
	}
	sort.Strings(patternsToLoad)
	fset := token.NewFileSet()
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports,
		Dir:  root,
		Fset: fset,
	}
	pkgs, err := packages.Load(cfg, patternsToLoad...)
	if err != nil {
		return nil, fmt.Errorf("load packages: %w", err)
	}
	seen := map[string]bool{}
	var hits []string
	for _, p := range pkgs {
		// Type errors are tolerated: a planted probe (or a broken edit)
		// must still be scanned. Missing type information degrades to the
		// syntactic rules, never to a pass.
		for _, f := range p.Syntax {
			name := fset.Position(f.Pos()).Filename
			if !inScope[name] || seen[name] {
				continue
			}
			seen[name] = true
			rel, _ := filepath.Rel(root, name)
			a := &analyzer{root: root, fset: fset, info: p.TypesInfo, patterns: patterns, rel: filepath.ToSlash(rel)}
			a.file(f)
			hits = append(hits, a.hits...)
		}
	}
	// A file no loaded package contains (another GOOS's build tag) is still
	// scanned — syntactically, without type information — so a build tag
	// cannot hide a violation.
	for f := range inScope {
		if seen[f] {
			continue
		}
		af, perr := parser.ParseFile(fset, f, nil, parser.ParseComments)
		if perr != nil {
			return nil, fmt.Errorf("%s: %w", f, perr)
		}
		rel, _ := filepath.Rel(root, f)
		a := &analyzer{root: root, fset: fset, patterns: patterns, rel: filepath.ToSlash(rel)}
		a.file(af)
		hits = append(hits, a.hits...)
	}
	sort.Strings(hits)
	return hits, nil
}

func (a *analyzer) file(f *ast.File) {
	a.allow = map[int]bool{}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.Contains(c.Text, "cred-log-allow:") {
				a.allow[a.fset.Position(c.Pos()).Line] = true
			}
		}
	}
	for _, d := range f.Decls {
		a.tainted = map[any]string{}
		a.sinkFuncs = map[any]bool{}
		a.propagate(d)
		ast.Inspect(d, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok && a.isSink(call) {
				a.checkSink(call)
			}
			return true
		})
	}
}

// key identifies a variable: its types.Object when type information is
// available, else its name (a planted probe with type errors still gets
// assignment tracking).
func (a *analyzer) key(id *ast.Ident) any {
	if a.info != nil {
		if o := a.info.ObjectOf(id); o != nil {
			return o
		}
	}
	return "name:" + id.Name
}

// propagate runs taint (and method-value sink) propagation over one
// declaration to a fixed point.
func (a *analyzer) propagate(d ast.Node) {
	for round := 0; round < 6; round++ {
		changed := false
		mark := func(lhs ast.Expr, rhs ast.Expr) {
			id, ok := lhs.(*ast.Ident)
			if !ok || id.Name == "_" {
				return
			}
			k := a.key(id)
			if sel, ok := rhs.(*ast.SelectorExpr); ok && levelMethods[sel.Sel.Name] && !a.sinkFuncs[k] {
				a.sinkFuncs[k] = true
				changed = true
			}
			if _, done := a.tainted[k]; done {
				return
			}
			if r := a.taint(rhs, false); r != "" {
				a.tainted[k] = fmt.Sprintf("%q assigned from %s", id.Name, r)
				changed = true
			}
		}
		ast.Inspect(d, func(n ast.Node) bool {
			switch t := n.(type) {
			case *ast.AssignStmt:
				if len(t.Lhs) == len(t.Rhs) {
					for i := range t.Lhs {
						mark(t.Lhs[i], t.Rhs[i])
					}
				} else if len(t.Rhs) == 1 {
					// v, err := f(key): the text-typed results of a
					// tainted call (an error result is not tainted by a
					// helper; see callTaint).
					for _, l := range t.Lhs {
						if a.textTyped(l) {
							mark(l, t.Rhs[0])
						}
					}
				}
			case *ast.ValueSpec:
				if len(t.Names) == len(t.Values) {
					for i := range t.Names {
						mark(t.Names[i], t.Values[i])
					}
				} else if len(t.Values) == 1 {
					for _, nm := range t.Names {
						mark(nm, t.Values[0])
					}
				}
			}
			return true
		})
		if !changed {
			return
		}
	}
}

func (a *analyzer) isSink(c *ast.CallExpr) bool {
	if id, ok := c.Fun.(*ast.Ident); ok {
		return a.sinkFuncs[a.key(id)]
	}
	if !isSink(c) {
		return false
	}
	// With type information, a level method (Info/Warn/Error/Debug…) is a
	// log sink only on a logger — not on an audit emitter or a result type
	// that happens to have an Error method.
	if sel, ok := c.Fun.(*ast.SelectorExpr); ok && levelMethods[sel.Sel.Name] && a.info != nil {
		if _, pkgFn := pkgSinks[exprString(sel.X)]; !pkgFn {
			if tv, ok := a.info.Types[sel.X]; ok && tv.Type != nil {
				return loggerType(tv.Type)
			}
		}
	}
	return true
}

// loggerType reports whether t is (a pointer to) a logger: slog.Logger,
// log.Logger, or any named type whose name or package says log.
func loggerType(t types.Type) bool {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := t.(*types.Named)
	if !ok {
		// An interface value or other unnamed type: be conservative.
		return true
	}
	obj := n.Obj()
	name := strings.ToLower(obj.Name())
	path := ""
	if obj.Pkg() != nil {
		path = strings.ToLower(obj.Pkg().Path())
	}
	return strings.Contains(name, "log") || strings.HasSuffix(path, "/slog") || path == "log/slog" || path == "log" || strings.Contains(path, "logging")
}

func (a *analyzer) checkSink(call *ast.CallExpr) {
	start, end := a.fset.Position(call.Pos()).Line, a.fset.Position(call.End()).Line
	for l := start; l <= end; l++ {
		if a.allow[l] {
			return
		}
	}
	var why []string
	for _, arg := range call.Args {
		ast.Inspect(arg, func(n ast.Node) bool {
			if _, ok := n.(*ast.FuncLit); ok {
				return false
			}
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if s, err := strconv.Unquote(lit.Value); err == nil && forbiddenKeys[strings.ToLower(s)] {
					why = append(why, fmt.Sprintf("forbidden attribute key %q", s))
				}
			}
			return true
		})
		if r := a.taint(arg, true); r != "" {
			why = append(why, r)
		}
	}
	if len(why) == 0 {
		return
	}
	a.hits = append(a.hits, fmt.Sprintf("%s:%d: %s: %s", a.rel, start, strings.Join(dedupe(why), "; "), truncate(exprString(call), 160)))
}

// constString returns e's constant string value — folded by the type
// checker, or by hand over + of literals when type information is missing.
func (a *analyzer) constString(e ast.Expr) (string, bool) {
	if a.info != nil {
		if tv, ok := a.info.Types[e]; ok && tv.Value != nil && tv.Value.Kind() == constant.String {
			return constant.StringVal(tv.Value), true
		}
	}
	switch t := e.(type) {
	case *ast.BasicLit:
		if t.Kind == token.STRING {
			if s, err := strconv.Unquote(t.Value); err == nil {
				return s, true
			}
		}
	case *ast.ParenExpr:
		return a.constString(t.X)
	case *ast.BinaryExpr:
		if t.Op == token.ADD {
			l, ok1 := a.constString(t.X)
			r, ok2 := a.constString(t.Y)
			if ok1 && ok2 {
				return l + r, true
			}
		}
	}
	return "", false
}

func (a *analyzer) keyShaped(s string) bool {
	for _, re := range a.patterns {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// taint reports why e carries secret material, or "". wholeValue says
// whether e is rendered whole at this position (a log argument, or an
// argument of a formatting call): only then does a struct with secret
// fields count, because only then are its fields printed.
func (a *analyzer) taint(e ast.Expr, wholeValue bool) string {
	if e == nil {
		return ""
	}
	if s, ok := a.constString(e); ok {
		if a.keyShaped(s) {
			return "key-shaped literal (core/fleet/secretshape.go pattern)"
		}
		return ""
	}
	if wholeValue {
		if r := a.secretTyped(e); r != "" {
			return r
		}
	}
	switch t := e.(type) {
	case *ast.Ident:
		if secretName(t.Name) {
			return "raw-secret identifier " + t.Name
		}
		if r, ok := a.tainted[a.key(t)]; ok {
			return r
		}
	case *ast.SelectorExpr:
		if secretName(t.Sel.Name) {
			return "secret-named field " + t.Sel.Name
		}
	case *ast.CallExpr:
		return a.callTaint(t)
	case *ast.ParenExpr:
		return a.taint(t.X, wholeValue)
	case *ast.StarExpr:
		return a.taint(t.X, wholeValue)
	case *ast.UnaryExpr:
		return a.taint(t.X, wholeValue)
	case *ast.IndexExpr:
		return a.taint(t.X, wholeValue)
	case *ast.SliceExpr:
		return a.taint(t.X, wholeValue)
	case *ast.TypeAssertExpr:
		return a.taint(t.X, wholeValue)
	case *ast.BinaryExpr:
		if t.Op != token.ADD {
			// A comparison or arithmetic result (key != "", n > 0) is
			// not the value.
			return ""
		}
		if r := a.taint(t.X, wholeValue); r != "" {
			return r
		}
		return a.taint(t.Y, wholeValue)
	case *ast.KeyValueExpr:
		return a.taint(t.Value, wholeValue)
	case *ast.CompositeLit:
		for _, el := range t.Elts {
			if r := a.taint(el, wholeValue); r != "" {
				return r
			}
		}
	}
	return ""
}

// formatters render their arguments into the result: a tainted argument,
// or a secret-field struct rendered by %v, taints the string or error
// they return.
var formatters = map[string]bool{
	"fmt.Sprintf": true, "fmt.Sprint": true, "fmt.Sprintln": true,
	"fmt.Errorf": true, "fmt.Appendf": true, "fmt.Append": true,
	"errors.New": true, "errors.Join": true,
}

// textMethods return their receiver's content as text.
var textMethods = map[string]bool{"Error": true, "String": true, "GoString": true}

func (a *analyzer) callTaint(c *ast.CallExpr) string {
	switch fn := c.Fun.(type) {
	case *ast.Ident:
		switch fn.Name {
		case "len", "cap":
			return "" // a length is not the value
		case "string":
			if len(c.Args) == 1 {
				if byteish(c.Args[0]) {
					return "bytes-to-text conversion string(...)"
				}
				if r := a.taint(c.Args[0], false); r != "" {
					return "bytes-to-text conversion of " + r
				}
			}
			return ""
		}
	case *ast.SelectorExpr:
		switch fn.Sel.Name {
		case "Bytes", "Use":
			return "borrowed secret view ." + fn.Sel.Name + "("
		}
		if textMethods[fn.Sel.Name] && !isPackageIdent(a.info, fn.X) {
			if a.credTyped(fn.X) {
				return fmt.Sprintf("%s() of credential handle %s", fn.Sel.Name, credTypeName)
			}
			if r := a.taint(fn.X, false); r != "" {
				return fmt.Sprintf("%s() of %s", fn.Sel.Name, r)
			}
			return ""
		}
	}
	name := exprString(c.Fun)
	if formatters[name] {
		for _, arg := range c.Args {
			if r := a.taint(arg, true); r != "" {
				return fmt.Sprintf("%s(...) of %s", name, r)
			}
		}
		return ""
	}
	// Any other call (a helper, strings.TrimSpace, hex.EncodeToString) is
	// assumed to pass a tainted string/[]byte argument through to a
	// string/[]byte result — the helper is not followed, so this is
	// conservative. Its error results are not tainted: an error a callee
	// builds is that callee's to keep clean, and is checked where it is
	// built if that file is in scope.
	if !a.textResult(c) {
		return ""
	}
	for _, arg := range c.Args {
		if r := a.taint(arg, false); r != "" {
			return fmt.Sprintf("%s(...) of %s", name, r)
		}
	}
	return ""
}

// textTyped reports whether e's type is string or []byte (true when
// unknown).
func (a *analyzer) textTyped(e ast.Expr) bool {
	if a.info == nil {
		return true
	}
	tv, ok := a.info.Types[e]
	if !ok || tv.Type == nil {
		if id, ok := e.(*ast.Ident); ok {
			if o := a.info.ObjectOf(id); o != nil {
				return isText(o.Type())
			}
		}
		return true
	}
	return isText(tv.Type)
}

func isText(t types.Type) bool {
	switch u := t.Underlying().(type) {
	case *types.Basic:
		return u.Info()&types.IsString != 0
	case *types.Slice:
		if b, ok := u.Elem().Underlying().(*types.Basic); ok {
			return b.Kind() == types.Byte
		}
	}
	return false
}

// textResult reports whether c returns a string or []byte (first result).
// Without type information it assumes so.
func (a *analyzer) textResult(c *ast.CallExpr) bool {
	if a.info == nil {
		return true
	}
	tv, ok := a.info.Types[c]
	if !ok || tv.Type == nil {
		return true
	}
	t := tv.Type
	if tup, ok := t.(*types.Tuple); ok {
		if tup.Len() == 0 {
			return false
		}
		t = tup.At(0).Type()
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		return u.Info()&types.IsString != 0
	case *types.Slice:
		if b, ok := u.Elem().Underlying().(*types.Basic); ok {
			return b.Kind() == types.Byte
		}
	}
	return false
}

func isPackageIdent(info *types.Info, e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	if !ok || info == nil {
		return false
	}
	_, isPkg := info.ObjectOf(id).(*types.PkgName)
	return isPkg
}

// secretTyped reports when e's static type is a struct with a secret-like
// field (by name or json tag) that would be rendered whole by %v / slog.
func (a *analyzer) secretTyped(e ast.Expr) string {
	if a.info == nil {
		return ""
	}
	tv, ok := a.info.Types[e]
	if !ok || tv.Type == nil || tv.IsType() {
		return ""
	}
	if path := credIn(tv.Type, 0, map[types.Type]bool{}); path != "" {
		return fmt.Sprintf("credential handle %s rendered (%s) — log its ReferenceID(), never the handle", credTypeName, path)
	}
	if field := secretField(tv.Type, 0, map[types.Type]bool{}); field != "" {
		return fmt.Sprintf("value of type %s rendered whole (secret field %s)", types.TypeString(tv.Type, shortQualifier), field)
	}
	return ""
}

func shortQualifier(p *types.Package) string { return p.Name() }

// credTypeName / credPkgSuffix identify the resolved-credential handle,
// core/llm/credref.Cred.
const (
	credTypeName  = "credref.Cred"
	credPkgSuffix = "/core/llm/credref"
)

// isCredType reports whether t is credref.Cred itself (not a pointer).
func isCredType(t types.Type) bool {
	n, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := n.Obj()
	return obj.Name() == "Cred" && obj.Pkg() != nil && strings.HasSuffix(obj.Pkg().Path(), credPkgSuffix)
}

// credTyped reports whether e's static type contains a credref.Cred.
func (a *analyzer) credTyped(e ast.Expr) bool {
	if a.info == nil {
		return false
	}
	tv, ok := a.info.Types[e]
	if !ok || tv.Type == nil || tv.IsType() {
		return false
	}
	return credIn(tv.Type, 0, map[types.Type]bool{}) != ""
}

// credIn returns a description of where t holds a credref.Cred — the
// type itself, a pointer to it, or a struct field / element (to a small
// depth) — or "". Unlike secretField, a String/LogValue/Format method on
// the enclosing type does not exempt it: Cred has all three.
func credIn(t types.Type, depth int, seen map[types.Type]bool) string {
	if depth > 3 || seen[t] {
		return ""
	}
	seen[t] = true
	if isCredType(t) {
		return "value"
	}
	switch u := t.Underlying().(type) {
	case *types.Pointer:
		if isCredType(u.Elem()) {
			return "pointer"
		}
		if r := credIn(u.Elem(), depth+1, seen); r != "" {
			return r
		}
	case *types.Slice:
		if r := credIn(u.Elem(), depth+1, seen); r != "" {
			return "element " + r
		}
	case *types.Array:
		if r := credIn(u.Elem(), depth+1, seen); r != "" {
			return "element " + r
		}
	case *types.Map:
		if r := credIn(u.Elem(), depth+1, seen); r != "" {
			return "map value " + r
		}
	case *types.Struct:
		for i := 0; i < u.NumFields(); i++ {
			if r := credIn(u.Field(i).Type(), depth+1, seen); r != "" {
				return "field " + u.Field(i).Name() + " " + r
			}
		}
	}
	return ""
}

// secretField returns the dotted name of the first secret-like field of t,
// or "". A type with a String() or LogValue() method renders through it,
// not field by field, and is not flagged.
func secretField(t types.Type, depth int, seen map[types.Type]bool) string {
	if depth > 2 || seen[t] {
		return ""
	}
	seen[t] = true
	if hasRenderMethod(t) {
		return ""
	}
	u := t
	if p, ok := u.Underlying().(*types.Pointer); ok {
		u = p.Elem()
		if hasRenderMethod(u) {
			return ""
		}
	}
	st, ok := u.Underlying().(*types.Struct)
	if !ok {
		return ""
	}
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		tag := reflect.StructTag(st.Tag(i)).Get("json")
		tagName := strings.Split(tag, ",")[0]
		if secretFieldName(f.Name()) || (tagName != "" && tagName != "-" && secretFieldName(tagName)) {
			return f.Name()
		}
		if sub := secretField(f.Type(), depth+1, seen); sub != "" {
			return f.Name() + "." + sub
		}
	}
	return ""
}

// secretFieldName is the struct-field rule: narrower than secretName,
// because a field name is a type's documentation, not a guess. Exact names
// and suffixes that mean key material; not a bare Key / PublicKey /
// Locator / Cred / Seed, which name references, attribute keys, public
// halves and RNG seeds far more often than secrets in this tree.
func secretFieldName(name string) bool {
	l := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(name))
	switch l {
	case "secret", "secrets", "plaintext", "password", "passwd", "uck", "ucek", "kek", "uckauth":
		return true
	}
	for _, suf := range []string{"token", "secret", "password", "apikey", "privatekey", "privkey", "plaintext", "passphrase"} {
		if strings.HasSuffix(l, suf) {
			return true
		}
	}
	return false
}

func hasRenderMethod(t types.Type) bool {
	for _, typ := range []types.Type{t, types.NewPointer(t)} {
		ms := types.NewMethodSet(typ)
		for i := 0; i < ms.Len(); i++ {
			switch ms.At(i).Obj().Name() {
			case "String", "LogValue", "Format":
				return true
			}
		}
	}
	return false
}

// isSink is the syntactic sink rule (see main.go's header).
func isSink(c *ast.CallExpr) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if id, ok := sel.X.(*ast.Ident); ok {
		if m, ok := pkgSinks[id.Name]; ok {
			if id.Name == "fmt" && strings.HasPrefix(sel.Sel.Name, "Fprint") {
				// Only a write to the process's own streams is a log;
				// Fprintf into a strings.Builder is formatting.
				if len(c.Args) == 0 {
					return false
				}
				w := exprString(c.Args[0])
				return m[sel.Sel.Name] && (w == "os.Stderr" || w == "os.Stdout")
			}
			return m[sel.Sel.Name]
		}
	}
	name := sel.Sel.Name
	if levelMethods[name] {
		return len(c.Args) > 0
	}
	if loggerOnlyMethods[name] {
		return loggerRecv.MatchString(exprString(sel.X))
	}
	return false
}
