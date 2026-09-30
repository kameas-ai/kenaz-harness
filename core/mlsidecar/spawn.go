package mlsidecar

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ProcessSpawner is the production Spawner (laya-advisors-01LAYA001 WP13):
// it launches the verified engine launcher (versions/<v>/kameas-ml/
// kameas-ml) as a DETACHED process in its own process group, so closing
// the harness never takes the shared engine down with it (the engine's
// own lease-based self-termination is what stops it — design §3.5).
//
// Interop values honored here (real-engine facts, WP13 brief item 4):
//   - KENAZ_ML_INSTALL_ROOT is set to Layout.Root, so the engine resolves
//     lease/ and the shutdown token under the harness's install root.
//   - The caller (Manager.spawnLocked) has already created lease/; this
//     type never spawns into a root without one (it re-checks).
//   - stdout/stderr go to <root>/engine.log (append), never to the
//     harness's own stdio.
//
// The child is deliberately started with exec.Command, NOT
// exec.CommandContext: the engine must outlive the ctx of whichever
// advisor call or Settings click triggered the spawn.
type ProcessSpawner struct {
	Layout Layout
	// ExtraEnv is appended after the inherited environment (and after
	// RootEnvVar), for tests and for LAYA_THREADS-style tuning.
	ExtraEnv []string
}

// SpawnEnv returns the environment the engine is launched with: the
// inherited environment, with RootEnvVar forced to root (any inherited
// value is dropped so a stray developer export can never redirect the
// engine away from the verified install).
func SpawnEnv(inherited []string, root string, extra ...string) []string {
	prefix := RootEnvVar + "="
	out := make([]string, 0, len(inherited)+1+len(extra))
	for _, kv := range inherited {
		if len(kv) >= len(prefix) && kv[:len(prefix)] == prefix {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, prefix+root)
	return append(out, extra...)
}

// Spawn implements Spawner.
func (p ProcessSpawner) Spawn(_ context.Context, exePath string) (int, error) {
	if info, err := os.Stat(p.Layout.LeaseDir()); err != nil || !info.IsDir() {
		return 0, fmt.Errorf("mlsidecar: refusing to spawn: lease dir %s does not exist (the client must create it first)", p.Layout.LeaseDir())
	}
	logf, err := os.OpenFile(filepath.Join(p.Layout.Root, "engine.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, fmt.Errorf("mlsidecar: open engine log: %w", err)
	}
	defer logf.Close() // the child holds its own dup of the fd

	cmd := exec.Command(exePath)
	cmd.Dir = filepath.Dir(exePath)
	cmd.Env = SpawnEnv(os.Environ(), p.Layout.Root, p.ExtraEnv...)
	cmd.Stdin = nil
	cmd.Stdout = logf
	cmd.Stderr = logf
	detachProcess(cmd)
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("mlsidecar: start engine %s: %w", exePath, err)
	}
	pid := cmd.Process.Pid
	// Reap the child when it exits (it self-terminates on zero leases) so
	// it never lingers as a zombie of the harness process.
	go func() { _ = cmd.Wait() }()
	return pid, nil
}
