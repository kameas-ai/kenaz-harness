package db

import (
	"errors"
	"testing"
)

func TestParseSemVer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want SemVer
	}{
		{"3.51.0", SemVer{3, 51, 0}},
		{"v3.45.1", SemVer{3, 45, 1}},
		{"3.52.0-rc1", SemVer{3, 52, 0}},
	}
	for _, c := range cases {
		got, err := ParseSemVer(c.in)
		if err != nil {
			t.Errorf("%q: unexpected err %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q -> %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestCheckSQLiteVersion_OK(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"3.51.0", "3.51.1", "3.52.0", "4.0.0"} {
		if err := CheckSQLiteVersion(v); err != nil {
			t.Errorf("%s: unexpected err %v", v, err)
		}
	}
}

func TestCheckSQLiteVersion_TooOld(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"3.50.9", "3.45.0", "2.0.0"} {
		err := CheckSQLiteVersion(v)
		if !errors.Is(err, ErrSQLiteVersionTooOld) {
			t.Errorf("%s: got %v, want ErrSQLiteVersionTooOld", v, err)
		}
	}
}

func TestCheckSQLiteVersion_BadInput(t *testing.T) {
	t.Parallel()
	err := CheckSQLiteVersion("not-a-version")
	if err == nil {
		t.Fatal("expected parse error")
	}
	if errors.Is(err, ErrSQLiteVersionTooOld) {
		t.Error("parse error must not be ErrSQLiteVersionTooOld")
	}
}

func TestSemVer_LessThan(t *testing.T) {
	t.Parallel()
	a := SemVer{3, 50, 9}
	b := SemVer{3, 51, 0}
	if !a.LessThan(b) {
		t.Error("3.50.9 should be < 3.51.0")
	}
	if b.LessThan(a) {
		t.Error("3.51.0 should not be < 3.50.9")
	}
	if a.LessThan(a) {
		t.Error("equal should not be <")
	}
}
