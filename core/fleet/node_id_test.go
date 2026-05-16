package fleet

import (
	"testing"
)

func TestNodeID_Stability(t *testing.T) {
	dir := t.TempDir()

	id1, err := NodeID(dir)
	if err != nil {
		t.Fatalf("NodeID first call: %v", err)
	}
	if id1 == "" {
		t.Fatal("NodeID returned empty string")
	}

	// Second call should return the same value (read from file).
	id2, err := NodeID(dir)
	if err != nil {
		t.Fatalf("NodeID second call: %v", err)
	}
	if id1 != id2 {
		t.Errorf("NodeID not stable: %q vs %q", id1, id2)
	}
}

func TestNodeID_UniquePerDir(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	id1, _ := NodeID(dir1)
	id2, _ := NodeID(dir2)
	if id1 == id2 {
		t.Error("two different data dirs produced identical node IDs")
	}
}

func TestNodeID_EmptyDataDir(t *testing.T) {
	// Empty dataDir → transient (not persisted).
	id, err := NodeID("")
	if err != nil {
		t.Fatalf("NodeID with empty dir: %v", err)
	}
	if id == "" {
		t.Error("NodeID returned empty for empty dir")
	}
}

func TestGenerateNodeID_Format(t *testing.T) {
	id := generateNodeID()
	if len(id) == 0 {
		t.Error("generateNodeID returned empty")
	}
	// Must be Crockford base32 chars.
	for _, c := range id {
		const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
		found := false
		for _, a := range alphabet {
			if c == a {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("invalid Crockford base32 char %q in node_id %q", c, id)
		}
	}
}
