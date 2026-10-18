package stdio

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
)

// ml-producer-01MLPRD01 WP04, spec §8.4 (amended by §12 A-4): an MCP stdio
// server the POOL spawns — the production path, Pool.Open → Connection.Open
// → childEnv — carries KENAZ_ACTOR=agent and never KENAZ_SESSION, on both
// the inherited and the isolated env path. The server is the real fake MCP
// server behind a shell shim that dumps its environment first, so the
// handshake completes and the pool reports the server up.
// (env_agent_marker_test.go covers childEnv alone.)
func TestPoolOpen_SpawnedServerCarriesAgentActor(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	bin := buildFakeServer(t)
	t.Setenv("KENAZ_ACTOR", "person") // inherited by the harness: must not survive
	t.Setenv("KENAZ_SESSION", "inherited-session")
	for _, isolate := range []bool{false, true} {
		dump := filepath.Join(t.TempDir(), "env.txt")
		p := NewPool(PoolOptions{})
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := p.Open(ctx, []coremcp.ServerSpec{{
			Name: "zzmarker", Transport: "stdio", IsolateEnv: isolate,
			Command: []string{sh, "-c", `env > "$0"; exec "$1"`, dump, bin},
		}})
		cancel()
		_ = p.Close(context.Background())
		if err != nil {
			t.Fatalf("isolate=%v: Open: %v", isolate, err)
		}
		b, err := os.ReadFile(dump)
		if err != nil {
			t.Fatalf("isolate=%v: env dump: %v", isolate, err)
		}
		var actors []string
		for _, line := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(line, "KENAZ_ACTOR="); ok {
				actors = append(actors, v)
			}
			if strings.HasPrefix(line, "KENAZ_SESSION=") {
				t.Errorf("isolate=%v: pool-spawned server got %q; the stdio pool is global", isolate, line)
			}
		}
		if len(actors) != 1 || actors[0] != "agent" {
			t.Errorf("isolate=%v: KENAZ_ACTOR values = %v, want exactly [agent]", isolate, actors)
		}
	}
}
