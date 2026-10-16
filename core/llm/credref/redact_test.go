package credref

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// plantedCred resolves a Cred holding a recognisable key assembled at
// runtime (no key literal lives in the repo) and returns the forms of it
// that must never appear in rendered output: the raw key, its hex
// encodings, and its unique random tail.
func plantedCred(t *testing.T) (*Cred, []string) {
	t.Helper()
	tail := make([]byte, 12)
	if _, err := rand.Read(tail); err != nil {
		t.Fatal(err)
	}
	marker := "PLANTED" + strings.ToUpper(hex.EncodeToString(tail))
	key := "sk" + "-" + "ant" + "-" + marker
	const env = "TEST_CREDREF_REDACT_KEY"
	t.Setenv(env, key)
	c, err := New(secrets.NewMemoryBackend()).Resolve(context.Background(), "p-redact", llm.CredentialReference{Kind: "env", Locator: env})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Destroy)
	if string(c.Bytes()) != key {
		t.Fatal("planted key did not round-trip through Resolve")
	}
	return c, []string{
		key, marker,
		hex.EncodeToString([]byte(key)), strings.ToUpper(hex.EncodeToString([]byte(key))),
		fmt.Sprint([]byte(key)), // the %v rendering of a []byte field
	}
}

func assertNoKey(t *testing.T, what, got string, forbidden []string) {
	t.Helper()
	for _, f := range forbidden {
		if strings.Contains(got, f) {
			t.Errorf("%s leaked key material (%d-byte form):\n%s", what, len(f), got)
			return
		}
	}
	if strings.Contains(got, "PLANTED") {
		t.Errorf("%s leaked the planted marker:\n%s", what, got)
	}
}

func TestCred_FormatVerbsRedact(t *testing.T) {
	c, forbidden := plantedCred(t)
	verbs := []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d"}
	for _, v := range verbs {
		for _, arg := range []struct {
			name string
			val  any
		}{{"pointer", c}, {"value", *c}} {
			got := fmt.Sprintf(v, arg.val)
			assertNoKey(t, fmt.Sprintf("Sprintf(%q, %s)", v, arg.name), got, forbidden)
			if !strings.Contains(got, RedactedForm) {
				t.Errorf("Sprintf(%q, %s) = %q, want the redacted form", v, arg.name, got)
			}
		}
	}
	// Through interfaces, so the test also compiles (and fails) against
	// a Cred with no rendering methods.
	if s, ok := any(c).(fmt.Stringer); !ok || s.String() != RedactedForm {
		t.Error("*Cred must implement fmt.Stringer with the redacted form")
	}
	if s, ok := any(*c).(fmt.GoStringer); !ok || s.GoString() != RedactedForm {
		t.Error("Cred must implement fmt.GoStringer with the redacted form")
	}
	if _, ok := any(*c).(slog.LogValuer); !ok {
		t.Error("Cred must implement slog.LogValuer")
	}
	assertNoKey(t, "Sprint", fmt.Sprint(c, *c), forbidden)
	assertNoKey(t, "Sprintln", fmt.Sprintln(c, *c), forbidden)
}

func TestCred_EmbeddedInStructRedacts(t *testing.T) {
	c, forbidden := plantedCred(t)
	type exported struct {
		Name string
		C    Cred
		P    *Cred
	}
	type unexported struct { // fmt cannot call methods on unexported fields
		name string
		c    Cred
		p    *Cred
	}
	type embedded struct {
		Cred
		ID string
	}
	vals := map[string]any{
		"exported":       exported{Name: "x", C: *c, P: c},
		"exported-ptr":   &exported{Name: "x", C: *c, P: c},
		"unexported":     unexported{name: "x", c: *c, p: c},
		"unexported-ptr": &unexported{name: "x", c: *c, p: c},
		"embedded":       embedded{Cred: *c, ID: "x"},
		"slice":          []*Cred{c, c},
		"map":            map[string]Cred{"k": *c},
	}
	for name, v := range vals {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
			assertNoKey(t, fmt.Sprintf("Sprintf(%q, %s)", verb, name), fmt.Sprintf(verb, v), forbidden)
		}
	}
}

func TestCred_SlogHandlersRedact(t *testing.T) {
	c, forbidden := plantedCred(t)
	for _, h := range []struct {
		name string
		mk   func(*bytes.Buffer) slog.Handler
	}{
		{"text", func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) }},
		{"json", func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) }},
	} {
		var buf bytes.Buffer
		l := slog.New(h.mk(&buf))
		l.Info("cred", "c", c, "v", *c)
		l.Info("cred", slog.Any("c", c), slog.Any("v", *c))
		l.With("c", c).Info("with")
		l.Info("grouped", slog.Group("g", "c", c))
		l.Info("embedded", "s", struct {
			Name string
			C    *Cred
		}{"x", c})
		out := buf.String()
		assertNoKey(t, h.name+" handler", out, forbidden)
		if strings.Count(out, RedactedForm) < 6 {
			t.Errorf("%s handler: want the redacted form for each Cred attribute, got:\n%s", h.name, out)
		}
	}
}

func TestCred_ZeroAndNilRedact(t *testing.T) {
	var nilCred *Cred
	var zero Cred
	for _, got := range []string{fmt.Sprintf("%v", nilCred), fmt.Sprintf("%+v", zero), fmt.Sprint(&zero)} {
		if strings.Contains(got, "PLANTED") {
			t.Fatalf("unexpected %q", got)
		}
	}
	if zero.Bytes() != nil || zero.ReferenceID() != "" || zero.Use(func([]byte) error { return nil }) == nil {
		t.Fatal("zero Cred must hold nothing and refuse Use")
	}
	zero.Destroy()
	nilCred.Destroy()
}
