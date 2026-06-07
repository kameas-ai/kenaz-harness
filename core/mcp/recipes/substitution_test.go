package recipes_test

import (
	"bytes"
	"log"
	"reflect"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

func TestSubstitute_ScalarSingleVar(t *testing.T) {
	got := recipes.Substitute(
		[]string{"--root=${DATA_DIR}"},
		map[string]string{"DATA_DIR": "/var/harness"},
		nil,
	)
	want := []string{"--root=/var/harness"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSubstitute_ScalarMultipleVarsInOneString(t *testing.T) {
	got := recipes.Substitute(
		[]string{"${A}-${B}-${A}"},
		map[string]string{"A": "x", "B": "y"},
		nil,
	)
	want := []string{"x-y-x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSubstitute_ListExpandsToMultipleArgs(t *testing.T) {
	got := recipes.Substitute(
		[]string{"npx", "-y", "server-fs", "${ALLOWED_DIRS}"},
		nil,
		map[string][]string{"ALLOWED_DIRS": {"/tmp/a", "/tmp/b", "/tmp/c"}},
	)
	want := []string{"npx", "-y", "server-fs", "/tmp/a", "/tmp/b", "/tmp/c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSubstitute_MixedScalarAndList(t *testing.T) {
	got := recipes.Substitute(
		[]string{"--data=${DATA_DIR}", "${ALLOWED_DIRS}"},
		map[string]string{"DATA_DIR": "/var/harness"},
		map[string][]string{"ALLOWED_DIRS": {"/tmp/a", "/tmp/b"}},
	)
	want := []string{"--data=/var/harness", "/tmp/a", "/tmp/b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSubstitute_UnknownVarLeavesLiteralAndWarns(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	got := recipes.Substitute(
		[]string{"${UNKNOWN}/etc"},
		map[string]string{},
		nil,
	)
	want := []string{"${UNKNOWN}/etc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if !strings.Contains(buf.String(), "${UNKNOWN}") {
		t.Errorf("warn log missing token name; got: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "warn") {
		t.Errorf("warn log missing 'warn' marker; got: %q", buf.String())
	}
}

func TestSubstitute_EmptyListAppendsZero(t *testing.T) {
	got := recipes.Substitute(
		[]string{"npx", "${ALLOWED_DIRS}"},
		nil,
		map[string][]string{"ALLOWED_DIRS": {}},
	)
	want := []string{"npx"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSubstitute_NilTemplate(t *testing.T) {
	got := recipes.Substitute(nil, nil, nil)
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestSubstitute_TemplateNotMutated(t *testing.T) {
	tmpl := []string{"${A}"}
	_ = recipes.Substitute(tmpl, map[string]string{"A": "value"}, nil)
	if tmpl[0] != "${A}" {
		t.Errorf("input template was mutated: %v", tmpl)
	}
}

func TestSubstitute_PartialListTokenIsScalar(t *testing.T) {
	// "/${ALLOWED_DIRS}" is NOT a whole-arg ${VAR}, so it falls
	// through to the scalar path. listVars values are space-joined
	// for the scalar fallback (the documented escape hatch).
	got := recipes.Substitute(
		[]string{"prefix-${ALLOWED_DIRS}"},
		nil,
		map[string][]string{"ALLOWED_DIRS": {"/a", "/b"}},
	)
	want := []string{"prefix-/a /b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSubstitute_ListVarTakesPrecedenceForWholeToken(t *testing.T) {
	got := recipes.Substitute(
		[]string{"${X}"},
		map[string]string{"X": "scalar"},
		map[string][]string{"X": {"list-a", "list-b"}},
	)
	want := []string{"list-a", "list-b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
