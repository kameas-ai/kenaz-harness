package migrations

import "testing"

func TestCanonicalizeSQL_StripsLineComments(t *testing.T) {
	t.Parallel()
	in := "CREATE TABLE foo (\n  id INT  -- primary key\n);"
	got := CanonicalizeSQL(in)
	if got != "CREATE TABLE foo ( id INT );" {
		t.Errorf("got %q", got)
	}
}

func TestCanonicalizeSQL_StripsBlockComments(t *testing.T) {
	t.Parallel()
	in := "CREATE /* big comment */ TABLE foo /* inline */ (id INT);"
	got := CanonicalizeSQL(in)
	if got != "CREATE TABLE foo (id INT);" {
		t.Errorf("got %q", got)
	}
}

func TestCanonicalizeSQL_NormalizesLineEndings(t *testing.T) {
	t.Parallel()
	in := "CREATE TABLE foo\r\n(id INT);\r"
	got := CanonicalizeSQL(in)
	if got != "CREATE TABLE foo (id INT);" {
		t.Errorf("got %q", got)
	}
}

func TestCanonicalizeSQL_CollapsesWhitespace(t *testing.T) {
	t.Parallel()
	in := "CREATE   TABLE\tfoo\n\n(id  INT);"
	got := CanonicalizeSQL(in)
	if got != "CREATE TABLE foo (id INT);" {
		t.Errorf("got %q", got)
	}
}

func TestHashSQL_StableAcrossFormatting(t *testing.T) {
	t.Parallel()
	a := "CREATE TABLE foo (id INTEGER PRIMARY KEY)"
	b := `
        -- header comment
        CREATE  TABLE   foo
            ( id  INTEGER   PRIMARY  KEY )
    `
	if HashSQL(a) != HashSQL(b) {
		t.Errorf("HashSQL not stable across whitespace/comments: %s vs %s",
			HashSQL(a), HashSQL(b))
	}
}

func TestHashSQL_DistinguishesContent(t *testing.T) {
	t.Parallel()
	a := "CREATE TABLE foo (id INT)"
	b := "CREATE TABLE bar (id INT)"
	if HashSQL(a) == HashSQL(b) {
		t.Error("expected different hashes for different table names")
	}
}
