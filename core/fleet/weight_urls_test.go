package fleet

import (
	"testing"
)

func TestSetAndCurrentWeightURLs(t *testing.T) {
	dataDir := t.TempDir()

	// Reset in-memory state for test isolation.
	weightURLState.mu.Lock()
	weightURLState.urls = nil
	weightURLState.mu.Unlock()

	urls := []string{"https://cdn.example.com/w1.bin", "https://cdn.example.com/w2.bin"}
	SetWeightURLs(dataDir, urls)

	got := CurrentWeightURLs(dataDir)
	if len(got) != 2 {
		t.Fatalf("CurrentWeightURLs: len=%d, want 2", len(got))
	}
	if got[0] != urls[0] || got[1] != urls[1] {
		t.Errorf("got %v, want %v", got, urls)
	}
}

func TestCurrentWeightURLs_PersistReload(t *testing.T) {
	dataDir := t.TempDir()

	// Reset in-memory state.
	weightURLState.mu.Lock()
	weightURLState.urls = nil
	weightURLState.mu.Unlock()

	urls := []string{"https://example.com/model.bin"}
	SetWeightURLs(dataDir, urls)

	// Clear in-memory state to simulate restart.
	weightURLState.mu.Lock()
	weightURLState.urls = nil
	weightURLState.mu.Unlock()

	// Should load from disk.
	got := CurrentWeightURLs(dataDir)
	if len(got) != 1 || got[0] != urls[0] {
		t.Errorf("after reload got %v, want %v", got, urls)
	}
}

func TestSetWeightURLs_EmptyDataDir(t *testing.T) {
	// Empty dataDir should not panic or error.
	SetWeightURLs("", []string{"https://example.com/w.bin"})
}

func TestCurrentWeightURLs_EmptyDataDir(t *testing.T) {
	weightURLState.mu.Lock()
	weightURLState.urls = nil
	weightURLState.mu.Unlock()

	got := CurrentWeightURLs("")
	if got != nil {
		t.Errorf("empty dataDir: got %v, want nil", got)
	}
}
