package bash_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/tools/bash"
)

// ml-producer-01MLPRD01 WP02 (spec §1 rule 3, §12 A-4): every process the
// agent spawns through kenaz__bash — foreground and background — carries
// the EnvProvider's markers on top of the inherited environment.

type ctxKey struct{}

func markerProvider(ctx context.Context) []string {
	sid, _ := ctx.Value(ctxKey{}).(string)
	return []string{"KENAZ_ACTOR=agent", "KENAZ_SESSION=hash-of-" + sid}
}

func TestEnvProvider_ForegroundCarriesMarkers(t *testing.T) {
	t.Setenv("KENAZ_ENV_PROVIDER_AMBIENT", "inherited")
	t.Setenv("KENAZ_ACTOR", "person") // the provider must win
	tool := bash.New(bash.Options{
		SandboxRoot: t.TempDir(),
		Allowlist:   []string{"env"},
		EnvProvider: markerProvider,
	})
	ctx := context.WithValue(context.Background(), ctxKey{}, "s1")
	raw, err := tool.Call(ctx, json.RawMessage(`{"command":"env"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var res struct {
		Stdout   string `json:"stdout"`
		ExitCode int    `json:"exit_code"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.ExitCode != 0 {
		t.Fatalf("result %s (err %v)", raw, err)
	}
	env := envLines(res.Stdout)
	if env["KENAZ_ACTOR"] != "agent" || env["KENAZ_SESSION"] != "hash-of-s1" {
		t.Fatalf("markers missing from foreground env: ACTOR=%q SESSION=%q", env["KENAZ_ACTOR"], env["KENAZ_SESSION"])
	}
	if env["KENAZ_ENV_PROVIDER_AMBIENT"] != "inherited" {
		t.Error("the process env was not inherited alongside the markers")
	}
}

func TestEnvProvider_NilInheritsUnchanged(t *testing.T) {
	t.Setenv("KENAZ_ENV_PROVIDER_AMBIENT", "inherited")
	tool := bash.New(bash.Options{SandboxRoot: t.TempDir(), Allowlist: []string{"env"}})
	raw, err := tool.Call(context.Background(), json.RawMessage(`{"command":"env"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var res struct {
		Stdout string `json:"stdout"`
	}
	_ = json.Unmarshal(raw, &res)
	env := envLines(res.Stdout)
	if env["KENAZ_ENV_PROVIDER_AMBIENT"] != "inherited" {
		t.Error("nil EnvProvider no longer inherits the process env")
	}
	if _, ok := env["KENAZ_SESSION"]; ok {
		t.Error("nil EnvProvider added a marker")
	}
}

func TestEnvProvider_BackgroundCarriesMarkers(t *testing.T) {
	reg := newBgRegistry()
	out := filepath.Join(t.TempDir(), "env.txt")
	tool := bash.New(bash.Options{
		SandboxRoot:     t.TempDir(),
		Allowlist:       []string{"env", "sh"},
		BackgroundSpawn: reg.spawn,
		BackgroundEnd:   reg.end,
		EnvProvider:     markerProvider,
	})
	ctx := context.WithValue(context.Background(), ctxKey{}, "s2")
	args, _ := json.Marshal(map[string]any{"command": "env > " + out, "run_in_background": true})
	if _, err := tool.Call(ctx, args); err != nil {
		t.Fatalf("Call: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, ended := reg.snapshot(); len(ended) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background process never ended")
		}
		time.Sleep(20 * time.Millisecond)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read env dump: %v", err)
	}
	env := envLines(string(b))
	if env["KENAZ_ACTOR"] != "agent" || env["KENAZ_SESSION"] != "hash-of-s2" {
		t.Fatalf("markers missing from background env: ACTOR=%q SESSION=%q", env["KENAZ_ACTOR"], env["KENAZ_SESSION"])
	}
}

func envLines(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			out[k] = v
		}
	}
	return out
}
