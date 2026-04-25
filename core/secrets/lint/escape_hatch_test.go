package lint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestNolintDirectiveDetection unit-tests the comment scanner used by
// the analyzer. The full positive/negative behaviour is tested via
// analysistest in lint_test.go; this test exercises the parser
// directly to keep coverage high on hasNolintDirective.
func TestNolintDirectiveDetection(t *testing.T) {
	src := `package x

type T struct {
	APIKey string //nolint:secret-bytes test only — buffer compares
	Token  string //nolint:secret-bytes
	Plain  string
}

func F() {
	_ = "x" //nolint:secret-bytes another reason
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x_test.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	type want struct {
		line          int
		present       bool
		hasJustified  bool
	}
	cases := []want{
		{line: 4, present: true, hasJustified: true},
		{line: 5, present: true, hasJustified: false},
		{line: 6, present: false, hasJustified: false},
	}
	for _, c := range cases {
		// find the field on c.line
		ast.Inspect(f, func(n ast.Node) bool {
			fld, ok := n.(*ast.Field)
			if !ok {
				return true
			}
			if fset.Position(fld.Pos()).Line != c.line {
				return true
			}
			present, j := hasNolintDirective(f, fld.Names[0].Pos(), fset)
			if present != c.present {
				t.Errorf("line %d present=%v want %v", c.line, present, c.present)
			}
			if (j != "") != c.hasJustified {
				t.Errorf("line %d justified=%q want hasJustified=%v", c.line, j, c.hasJustified)
			}
			return true
		})
	}

	// nolintRE smoke
	if !strings.Contains(`//nolint:secret-bytes reason`, "nolint:secret-bytes") {
		t.Fatal("smoke")
	}
}
