package fleet

// enroll_node_id_repair_test.go — Fleet enforces node_id as 1–128 printable
// ASCII with no spaces (400 invalid_request, details.field "node_id"). A
// stored id Fleet refuses is corrupt: enroll re-mints it the way first
// enroll does, persists it, and retries ONCE. A second refusal is
// ErrNodeIDInvalid; no other error re-mints.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// enrollScript answers /api/v1/enroll per call and records the node ids.
type enrollScript struct {
	mu      sync.Mutex
	nodeIDs []string
	answer  func(call int, nodeID string) (int, any)
}

func (e *enrollScript) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v1/enroll" {
		http.NotFound(w, r)
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	id, _ := body["node_id"].(string)
	e.mu.Lock()
	e.nodeIDs = append(e.nodeIDs, id)
	status, reply := e.answer(len(e.nodeIDs), id)
	e.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(reply)
}

func (e *enrollScript) ids() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.nodeIDs...)
}

var nodeIDRefusal = map[string]any{
	"code":    "invalid_request",
	"message": "node_id must be printable ASCII without spaces, at most 128 bytes.",
	"details": map[string]any{"field": "node_id"},
}

func enrollRig(t *testing.T, e *enrollScript) (*Client, string) {
	t.Helper()
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	stubTokens(t, TokenSet{AccessToken: "at-enroll-nid", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	dataDir := t.TempDir()
	c := makeTestClient(t, srv.URL)
	c.dataDir = dataDir
	return c, dataDir
}

func writeCorruptNodeID(t *testing.T, dataDir string) string {
	t.Helper()
	bad := "corrupt id with spaces"
	if err := os.MkdirAll(dataDir+"/fleet", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nodeIDFilePath(dataDir), []byte(bad+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return bad
}

func TestEnroll_NodeIDRefused_RemintsOnceAndPersists(t *testing.T) {
	e := &enrollScript{answer: func(call int, id string) (int, any) {
		if strings.Contains(id, " ") {
			return 400, nodeIDRefusal
		}
		return 200, map[string]any{"user_id": "u1", "org_id": "o1", "role": "org_member"}
	}}
	c, dataDir := enrollRig(t, e)
	bad := writeCorruptNodeID(t, dataDir)
	id, err := c.RefreshIdentity(t.Context(), bad, "darwin", "1.0.0")
	if err != nil || id.UserID != "u1" {
		t.Fatalf("RefreshIdentity = %+v, %v; want a successful enroll after one re-mint", id, err)
	}
	got := e.ids()
	if len(got) != 2 || got[0] != bad || got[1] == bad {
		t.Fatalf("enroll node ids = %q; want the corrupt one, then one fresh one", got)
	}
	if stored := ReadNodeID(dataDir); stored != got[1] {
		t.Fatalf("persisted node id %q, want the re-minted %q", stored, got[1])
	}
	if len(got[1]) != 26 {
		t.Fatalf("re-minted id %q is not the first-enroll shape", got[1])
	}
}

func TestEnroll_NodeIDRefusedTwice_IsPlainErrorAndNoLoop(t *testing.T) {
	e := &enrollScript{answer: func(int, string) (int, any) { return 400, nodeIDRefusal }}
	c, dataDir := enrollRig(t, e)
	bad := writeCorruptNodeID(t, dataDir)
	_, err := c.RefreshIdentity(t.Context(), bad, "darwin", "1.0.0")
	if !errors.Is(err, ErrNodeIDInvalid) {
		t.Fatalf("err = %v, want ErrNodeIDInvalid", err)
	}
	if n := len(e.ids()); n != 2 {
		t.Fatalf("enroll calls = %d, want exactly 2 (one retry)", n)
	}
	if strings.Contains(err.Error(), "invalid_request") {
		t.Fatalf("error %q is not plain words", err)
	}
}

func TestEnroll_OtherErrorsNeverRemint(t *testing.T) {
	for name, reply := range map[string]struct {
		status int
		body   any
	}{
		"invalid_request on another field": {400, map[string]any{"code": "invalid_request", "message": "platform is required.", "details": map[string]any{"field": "platform"}}},
		"payload_too_large":                {413, map[string]any{"code": "payload_too_large"}},
		"not provisioned":                  {403, map[string]any{"code": "user_not_provisioned", "message": "x"}},
	} {
		t.Run(name, func(t *testing.T) {
			e := &enrollScript{answer: func(int, string) (int, any) { return reply.status, reply.body }}
			c, dataDir := enrollRig(t, e)
			bad := writeCorruptNodeID(t, dataDir)
			if _, err := c.RefreshIdentity(t.Context(), bad, "darwin", "1.0.0"); err == nil || errors.Is(err, ErrNodeIDInvalid) {
				t.Fatalf("err = %v", err)
			}
			if n := len(e.ids()); n != 1 {
				t.Fatalf("enroll calls = %d, want 1 (no re-mint)", n)
			}
			if stored := ReadNodeID(dataDir); stored != bad {
				t.Fatalf("node id changed to %q on an unrelated error", stored)
			}
		})
	}
}
