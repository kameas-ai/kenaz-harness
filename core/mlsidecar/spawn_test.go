//go:build unix

package mlsidecar

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSpawnEnv_ForcesInstallRoot(t *testing.T) {
	env := SpawnEnv([]string{"A=1", RootEnvVar + "=/stray", "B=2"}, "/the/root", "LAYA_THREADS=2")
	var roots []string
	for _, kv := range env {
		if strings.HasPrefix(kv, RootEnvVar+"=") {
			roots = append(roots, kv)
		}
	}
	if len(roots) != 1 || roots[0] != RootEnvVar+"=/the/root" {
		t.Fatalf("root entries = %v, want exactly the forced one (inherited value must be dropped)", roots)
	}
	if env[len(env)-1] != "LAYA_THREADS=2" {
		t.Errorf("extra env not appended last: %v", env)
	}
}

func TestProcessSpawner_RefusesWithoutLeaseDir(t *testing.T) {
	l := NewLayout(t.TempDir())
	if _, err := (ProcessSpawner{Layout: l}).Spawn(context.Background(), "/bin/true", 0); err == nil {
		t.Fatal("spawner must refuse to start an engine into a root with no lease/ dir")
	}
}

// TestProcessSpawner_RealProcessSeesRootAndLeaseDir launches a tiny shell
// script (not a sidecar — no Python, no network) through the PRODUCTION
// spawner and checks, from inside the child, the two interop facts the
// real engine depends on: KENAZ_ML_INSTALL_ROOT is the layout root, and
// lease/ already exists when the process starts.
func TestProcessSpawner_RealProcessSeesRootAndLeaseDir(t *testing.T) {
	root := t.TempDir()
	l := NewLayout(root)
	if err := os.MkdirAll(l.LeaseDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "child.out")
	script := filepath.Join(root, "engine.sh")
	body := "#!/bin/sh\n" +
		"echo \"root=$" + RootEnvVar + "\" > \"" + out + ".tmp\"\n" +
		"if [ -d \"$" + RootEnvVar + "/lease\" ]; then echo leasedir=yes >> \"" + out + ".tmp\"; else echo leasedir=no >> \"" + out + ".tmp\"; fi\n" +
		"echo \"args=$*\" >> \"" + out + ".tmp\"\n" +
		"mv \"" + out + ".tmp\" \"" + out + "\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	port := CandidatePorts(EnginePort(EngineEnvDev))[1] // a fallback lane port, passed per spawn
	pid, err := (ProcessSpawner{Layout: l}).Spawn(context.Background(), script, port)
	if err != nil || pid <= 0 {
		t.Fatalf("Spawn: pid=%d err=%v", pid, err)
	}
	var got []byte
	for i := 0; i < 200; i++ {
		if b, rerr := os.ReadFile(out); rerr == nil {
			got = b
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !strings.Contains(string(got), "root="+root) || !strings.Contains(string(got), "leasedir=yes") {
		t.Fatalf("child saw %q, want root=%s and leasedir=yes", got, root)
	}
	if want := "args=serve --port " + strconv.Itoa(port); !strings.Contains(string(got), want) {
		t.Fatalf("child saw %q, want %q (the Manager-chosen lane port, passed explicitly)", got, want)
	}
	if _, err := os.Stat(filepath.Join(root, "engine.log")); err != nil {
		t.Errorf("engine.log not created: %v", err)
	}
}
