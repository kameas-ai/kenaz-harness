package autonomy

import (
	"encoding/json"
	"testing"
)

func TestFamilySetBasicOps(t *testing.T) {
	var fs FamilySet // nil
	if fs.Has(FamilyRead) {
		t.Error("nil FamilySet.Has(read) = true, want false")
	}

	fs = fs.Add(FamilyRead)
	if !fs.Has(FamilyRead) {
		t.Error("after Add(read), Has(read) = false")
	}
	fs = fs.Add(FamilyWrite)
	if got, want := len(fs), 2; got != want {
		t.Errorf("len after 2 Add = %d, want %d", got, want)
	}
	fs = fs.Remove(FamilyRead)
	if fs.Has(FamilyRead) {
		t.Error("after Remove(read), Has(read) = true")
	}
	if !fs.Has(FamilyWrite) {
		t.Error("Remove unrelated entry should not affect write")
	}

	// Remove on nil should be a no-op
	var nilSet FamilySet
	nilSet.Remove(FamilyRead) // should not panic
}

func TestFamilySetSorted(t *testing.T) {
	fs := NewFamilySet(FamilyNetwork, FamilyRead, FamilyWrite, FamilyShellSafe)
	got := fs.Sorted()
	want := []string{"network", "read", "shell-safe", "write"}
	if len(got) != len(want) {
		t.Fatalf("Sorted len = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("Sorted[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// Empty set should produce an empty slice (not nil) for stable JSON.
	emptySorted := NewFamilySet().Sorted()
	if emptySorted == nil || len(emptySorted) != 0 {
		t.Errorf("empty Sorted = %v, want empty non-nil slice", emptySorted)
	}
}

func TestFamilySetEqualClone(t *testing.T) {
	a := NewFamilySet(FamilyRead, FamilyWrite)
	b := NewFamilySet(FamilyWrite, FamilyRead)
	if !a.Equal(b) {
		t.Error("Equal should be order-independent")
	}
	if a.Equal(NewFamilySet(FamilyRead)) {
		t.Error("Equal should fail on different len")
	}
	if a.Equal(NewFamilySet(FamilyRead, FamilyShellSafe)) {
		t.Error("Equal should fail on different members")
	}

	clone := a.Clone()
	if !clone.Equal(a) {
		t.Error("Clone should equal original")
	}
	clone = clone.Add(FamilyShellSafe)
	if a.Has(FamilyShellSafe) {
		t.Error("mutation on clone leaked into original")
	}

	var nilSet FamilySet
	if nilSet.Clone() != nil {
		t.Error("Clone of nil FamilySet should be nil")
	}
}

func TestFamilySetJSONRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		fs   FamilySet
		want string
	}{
		{"nil", nil, "null"},
		{"empty", NewFamilySet(), "[]"},
		{"sorted", NewFamilySet(FamilyWrite, FamilyRead), `["read","write"]`},
		{"all-four", NewFamilySet(FamilyShellSafe, FamilyNetwork, FamilyRead, FamilyWrite),
			`["network","read","shell-safe","write"]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := json.Marshal(c.fs)
			if err != nil {
				t.Fatalf("Marshal err = %v", err)
			}
			if string(got) != c.want {
				t.Errorf("Marshal = %s, want %s", got, c.want)
			}

			var back FamilySet
			if err := json.Unmarshal(got, &back); err != nil {
				t.Fatalf("Unmarshal err = %v", err)
			}
			if c.fs == nil {
				if back != nil {
					t.Errorf("nil round-trip = %v, want nil", back)
				}
				return
			}
			if !back.Equal(c.fs) {
				t.Errorf("round-trip = %v, want %v", back, c.fs)
			}
		})
	}
}

func TestFamilySetUnmarshalInvalid(t *testing.T) {
	var fs FamilySet
	if err := json.Unmarshal([]byte(`"not-an-array"`), &fs); err == nil {
		t.Error("Unmarshal of non-array should error")
	}
}
