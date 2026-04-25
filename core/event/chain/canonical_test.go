package chain

import (
	"bytes"
	"testing"
)

func TestCanonicalKeyOrder(t *testing.T) {
	a, err := Canonical(map[string]any{"b": 1, "a": 2})
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	b, err := Canonical(map[string]any{"a": 2, "b": 1})
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("key order should not matter: %s vs %s", a, b)
	}
	if string(a) != `{"a":2,"b":1}` {
		t.Fatalf("unexpected canonical form %q", a)
	}
}

func TestCanonicalNumberFormats(t *testing.T) {
	a, _ := Canonical(map[string]any{"x": 1})
	b, _ := Canonical(map[string]any{"x": 1.0})
	c, _ := Canonical(map[string]any{"x": 1e0})
	if !bytes.Equal(a, b) || !bytes.Equal(b, c) {
		t.Fatalf("numbers 1 / 1.0 / 1e0 should canonicalize identically: %s %s %s", a, b, c)
	}
}

func TestCanonicalNested(t *testing.T) {
	v1 := map[string]any{
		"outer": map[string]any{
			"z": []any{3, 2, 1},
			"a": "x",
		},
	}
	v2 := map[string]any{
		"outer": map[string]any{
			"a": "x",
			"z": []any{3, 2, 1},
		},
	}
	a, _ := Canonical(v1)
	b, _ := Canonical(v2)
	if !bytes.Equal(a, b) {
		t.Fatalf("nested key order should not matter: %s vs %s", a, b)
	}
}

func TestCanonicalNullsAndBools(t *testing.T) {
	got, err := Canonical(map[string]any{"a": nil, "b": true, "c": false})
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	if string(got) != `{"a":null,"b":true,"c":false}` {
		t.Fatalf("unexpected: %q", got)
	}
}

func TestCanonicalRejectsUnsupported(t *testing.T) {
	if _, err := Canonical(make(chan int)); err == nil {
		t.Fatalf("chan should fail to marshal canonically")
	}
}

func TestCanonicalDeterministicAcrossInvocations(t *testing.T) {
	v := map[string]any{
		"name":  "alice",
		"age":   42,
		"tags":  []any{"a", "b"},
		"score": 0.75,
	}
	first, _ := Canonical(v)
	for i := 0; i < 50; i++ {
		next, _ := Canonical(v)
		if !bytes.Equal(first, next) {
			t.Fatalf("non-deterministic encoding at iteration %d", i)
		}
	}
}
