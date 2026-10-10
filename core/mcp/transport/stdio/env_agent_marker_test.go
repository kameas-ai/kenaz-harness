package stdio

import (
	"os/exec"
	"strings"
	"testing"
)

// ml-producer-01MLPRD01 WP02 (spec §1 rule 3, §12 A-4): every MCP stdio
// child carries KENAZ_ACTOR=agent — on the isolated AND the merged env
// path — and never KENAZ_SESSION (the pool is process-global). Proven by
// actually spawning a child that dumps its environment.
func TestChildEnv_AgentActorMarker_SpawnedChildSeesIt(t *testing.T) {
	envBin, err := exec.LookPath("env")
	if err != nil {
		t.Skip("no env binary")
	}
	t.Setenv("KENAZ_ACTOR", "person") // an inherited value must not survive
	for _, isolate := range []bool{true, false} {
		spec := SpawnSpec{
			IsolateEnv: isolate,
			Env:        map[string]string{"OWN": "1", "KENAZ_ACTOR": "spec-value"},
		}
		cmd := exec.Command(envBin)
		cmd.Env = childEnv(spec)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("isolate=%v: spawn env: %v", isolate, err)
		}
		var actors []string
		for _, line := range strings.Split(string(out), "\n") {
			if v, ok := strings.CutPrefix(line, "KENAZ_ACTOR="); ok {
				actors = append(actors, v)
			}
			if strings.HasPrefix(line, "KENAZ_SESSION=") {
				t.Errorf("isolate=%v: MCP child got KENAZ_SESSION; the stdio pool is global", isolate)
			}
		}
		if len(actors) != 1 || actors[0] != "agent" {
			t.Errorf("isolate=%v: KENAZ_ACTOR values = %v, want exactly [agent]", isolate, actors)
		}
		if !strings.Contains(string(out), "OWN=1") {
			t.Errorf("isolate=%v: spec env entry lost", isolate)
		}
	}
}
