package refs_test

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/credstore/refs"
)

func TestSanitizer_SingleFingerprint(t *testing.T) {
	s := refs.NewSanitizer()
	s.Add([]byte("MYTOKEN"), "user:foo")

	content := []byte("The response is MYTOKEN and more text")
	result := s.Sanitize(content)
	if bytes.Contains(result, []byte("MYTOKEN")) {
		t.Errorf("plaintext not redacted: %s", result)
	}
	if !bytes.Contains(result, []byte("[redacted: user:foo]")) {
		t.Errorf("placeholder missing: %s", result)
	}
}

func TestSanitizer_MultiFingerprint(t *testing.T) {
	s := refs.NewSanitizer()
	s.Add([]byte("FOOTOKEN"), "user:foo")
	s.Add([]byte("BARTOKEN"), "user:bar")

	content := []byte("Bearer FOOTOKEN, X-Bar: BARTOKEN")
	result := s.Sanitize(content)

	if bytes.Contains(result, []byte("FOOTOKEN")) {
		t.Errorf("FOOTOKEN not redacted: %s", result)
	}
	if bytes.Contains(result, []byte("BARTOKEN")) {
		t.Errorf("BARTOKEN not redacted: %s", result)
	}
	if !bytes.Contains(result, []byte("[redacted: user:foo]")) {
		t.Errorf("user:foo placeholder missing: %s", result)
	}
	if !bytes.Contains(result, []byte("[redacted: user:bar]")) {
		t.Errorf("user:bar placeholder missing: %s", result)
	}
}

func TestSanitizer_NoMatch(t *testing.T) {
	s := refs.NewSanitizer()
	s.Add([]byte("MYTOKEN"), "user:foo")
	content := []byte("no secret here")
	result := s.Sanitize(content)
	if !bytes.Equal(result, content) {
		t.Errorf("content changed when no match: %q", result)
	}
}

func TestSanitizer_EmptyNoAllocation(t *testing.T) {
	s := refs.NewSanitizer()
	content := []byte("no secrets here")
	result := s.Sanitize(content)
	if !bytes.Equal(result, content) {
		t.Errorf("empty sanitizer should return input unchanged")
	}
}

func TestSanitizer_ClearDropsFingerprints(t *testing.T) {
	s := refs.NewSanitizer()
	s.Add([]byte("SECRET"), "user:tok")
	if s.Len() != 1 {
		t.Fatalf("expected 1 entry before clear, got %d", s.Len())
	}
	s.Clear()
	if s.Len() != 0 {
		t.Fatalf("expected 0 entries after clear, got %d", s.Len())
	}
	// After clear, the content should not be redacted.
	result := s.Sanitize([]byte("contains SECRET here"))
	if !bytes.Contains(result, []byte("SECRET")) {
		t.Error("sanitizer redacted after Clear — fingerprint not dropped")
	}
}

func TestSanitizer_CrossTurnClear(t *testing.T) {
	s := refs.NewSanitizer()
	// Turn 1: add fingerprint.
	s.Add([]byte("TURN1TOKEN"), "user:t1")
	// End of turn 1.
	s.Clear()
	// Turn 2: should not see turn 1's fingerprint.
	result := s.Sanitize([]byte("something TURN1TOKEN here"))
	if !bytes.Contains(result, []byte("TURN1TOKEN")) {
		t.Error("turn 1 fingerprint leaked into turn 2 after Clear")
	}
}

// TestSanitizer_Idempotent verifies that adding the same plaintext twice
// is a no-op (one entry, not two).
func TestSanitizer_Idempotent(t *testing.T) {
	s := refs.NewSanitizer()
	s.Add([]byte("DUPTOKEN"), "user:dup")
	s.Add([]byte("DUPTOKEN"), "user:dup")
	if s.Len() != 1 {
		t.Errorf("expected 1 entry after duplicate Add, got %d", s.Len())
	}
}

// TestSanitizer_RaceSafety verifies concurrent Sanitize calls with a
// fixed set of fingerprints do not race.
func TestSanitizer_RaceSafety(t *testing.T) {
	s := refs.NewSanitizer()
	for i := 0; i < 5; i++ {
		s.Add([]byte("token"+string(rune('A'+i))), "user:"+string(rune('a'+i)))
	}
	var wg sync.WaitGroup
	content := []byte("response with tokenA and tokenC here")
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Sanitize(content)
		}()
	}
	wg.Wait()
}

func TestSanitizer_SanitizeString(t *testing.T) {
	s := refs.NewSanitizer()
	s.Add([]byte("STRINGSECRET"), "user:str")
	result := s.SanitizeString("the secret is STRINGSECRET and more")
	if strings.Contains(result, "STRINGSECRET") {
		t.Errorf("string not sanitized: %q", result)
	}
	if !strings.Contains(result, "[redacted: user:str]") {
		t.Errorf("placeholder missing: %q", result)
	}
}

// TestSanitizer_NilSafe verifies that SanitizeString on a nil sanitizer
// returns the input unchanged (context path where no sanitizer is wired).
func TestSanitizer_NilSafe(t *testing.T) {
	var s *refs.Sanitizer
	result := s.SanitizeString("input")
	if result != "input" {
		t.Errorf("nil sanitizer returned %q", result)
	}
}
