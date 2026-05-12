package migrations

import "testing"

func TestVersionBlock_Contains(t *testing.T) {
	t.Parallel()
	b := VersionBlock{Min: 100, Max: 199}
	cases := []struct {
		v    int
		want bool
	}{
		{99, false},
		{100, true},
		{150, true},
		{199, true},
		{200, false},
	}
	for _, c := range cases {
		if got := b.Contains(c.v); got != c.want {
			t.Errorf("Contains(%d) = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestCanonicalBlocks_StorageRange(t *testing.T) {
	t.Parallel()
	b, ok := LookupBlock("storage")
	if !ok {
		t.Fatal("storage block missing")
	}
	if b.Min != 1 || b.Max != 99 {
		t.Errorf("storage block = %+v, want {1,99}", b)
	}
}

func TestCanonicalBlocks_Coverage(t *testing.T) {
	t.Parallel()
	missions := []string{
		"storage", "event-log", "secrets-keychain", "sessions", "scheduler",
		"mcp", "a2a", "signed-cards-trust", "bundle",
		"shared-context-distribution", "memory-rag", "app-layer",
		"user-slash-commands",
	}
	for _, m := range missions {
		if _, ok := LookupBlock(m); !ok {
			t.Errorf("expected block for mission %q", m)
		}
	}
}

func TestCanonicalBlocks_NoOverlapWithinNonSharedRanges(t *testing.T) {
	t.Parallel()
	// Note: a2a and signed-cards-trust intentionally share 600-699;
	// bundle and shared-context-distribution share 700-799. Other
	// missions must not overlap.
	shared := map[string]string{
		"a2a": "signed-cards-trust", "signed-cards-trust": "a2a",
		"bundle": "shared-context-distribution", "shared-context-distribution": "bundle",
	}
	for ma, ba := range CanonicalBlocks {
		for mb, bb := range CanonicalBlocks {
			if ma == mb {
				continue
			}
			if shared[ma] == mb {
				continue
			}
			if ba.Min <= bb.Max && bb.Min <= ba.Max {
				t.Errorf("blocks %s%v and %s%v overlap", ma, ba, mb, bb)
			}
		}
	}
}
