package stdio

import (
	"strings"
	"testing"
)

// TestChildEnv_Isolated asserts the spec 091 D6 spawn-side guarantee: an
// IsolateEnv spawn's child environment contains ONLY the minimal base plus
// the spec's own entries — nothing else from the harness process env, in
// particular no sibling connector's credential grant.
func TestChildEnv_Isolated(t *testing.T) {
	// Plant a sibling connector's secret in the process env; it must not
	// reach an isolated child.
	t.Setenv("MCP_DATADOG__DD_API_KEY", "sibling-secret")
	t.Setenv("HOME", "/home/kenaz")

	env := childEnv(SpawnSpec{
		IsolateEnv: true,
		Env:        map[string]string{"GDRIVE_TOKEN": "own-secret"},
	})

	got := map[string]string{}
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			t.Fatalf("malformed env entry %q", kv)
		}
		if _, dup := got[k]; dup {
			t.Errorf("duplicate env key %q", k)
		}
		got[k] = v
	}

	if got["GDRIVE_TOKEN"] != "own-secret" {
		t.Error("own entry missing from isolated env")
	}
	if _, leaked := got["MCP_DATADOG__DD_API_KEY"]; leaked {
		t.Error("sibling connector secret leaked into isolated child env")
	}
	if got["HOME"] != "/home/kenaz" {
		t.Error("minimal base (HOME) missing from isolated env")
	}
	allowed := map[string]bool{}
	for _, k := range isolatedBaseEnvKeys {
		allowed[k] = true
	}
	for k := range got {
		if !allowed[k] && k != "GDRIVE_TOKEN" {
			t.Errorf("unexpected inherited key %q in isolated env", k)
		}
	}
}

// TestChildEnv_IsolatedOverridesAndClears pins the collision + empty-value
// semantics: spec.Env wins over the base, and an empty value withholds the
// key entirely.
func TestChildEnv_IsolatedOverridesAndClears(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("TERM", "xterm")

	env := childEnv(SpawnSpec{
		IsolateEnv: true,
		Env:        map[string]string{"PATH": "/opt/bin", "TERM": ""},
	})
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "PATH=/opt/bin") || strings.Contains(joined, "PATH=/usr/bin") {
		t.Errorf("spec.Env PATH override not applied: %q", joined)
	}
	if strings.Contains(joined, "TERM=") {
		t.Errorf("empty spec.Env value did not withhold TERM: %q", joined)
	}
}

// TestChildEnv_DefaultInherits pins the host-mode default: without
// IsolateEnv the child inherits the process env (merged) — unchanged
// behaviour.
func TestChildEnv_DefaultInherits(t *testing.T) {
	t.Setenv("SOME_AMBIENT_VAR", "ambient")
	env := childEnv(SpawnSpec{Env: map[string]string{"EXTRA": "1"}})
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "SOME_AMBIENT_VAR=ambient") {
		t.Error("default spawn no longer inherits the process env")
	}
	if !strings.Contains(joined, "EXTRA=1") {
		t.Error("default spawn dropped spec.Env entry")
	}
}
