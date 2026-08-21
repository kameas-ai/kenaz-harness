package rpc

// wf_network_authz_adapter_test.go — automation-actually-runs-01PMZ404
// UNIT-7, AC-008. Drives the REAL shipped policy bundle (never
// cedar.AllowAll{}) through a real core.New + rpc.New chassis
// (api_cedar_gate_wiring_test.go's pattern), covering both arms —
// strict denies, permissive still permits — and the live dial: flipping
// cedarStrictWorkflowMode between two runs on the SAME engine, proving
// the mode is read per-call (workflowCedarModeFn's contract) rather than
// snapshotted at boot.
//
// X-9's warning is why this suite exists: wiring a NetworkAuthorizer
// adapter alone cannot make strict mode deny anything, because
// enforce() maps NotApplicable to nil exactly like Allow. Only an
// explicit `forbid` rule in the strict arm of
// default_workflows_policy.cedar makes GateWorkflowNetworkFetch return
// Deny. The mutation-guard for that claim (delete the forbid rule, the
// strict arm must go GREEN) was run manually against a temp copy of the
// adapter+policy pair and is recorded in the mission report, not as a
// permanent test — a test cannot delete lines from the shipped policy
// file it is itself asserting against.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	workflowsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/workflows"
)

func webFetchWorkflowYAML(fetchURL string) string {
	return `
id: zz-wf-network-authz-probe
name: "wf network authz probe"
version: 1
steps:
  - name: fetch
    kind: web_fetch
    url: "` + fetchURL + `"
`
}

// TestCedarWiring_WebFetch_StrictModeDeniesPermissiveStillPermits is
// AC-008: both arms on the SAME workflow, same test server, same API
// instance — only the dial changes.
func TestCedarWiring_WebFetch_StrictModeDeniesPermissiveStillPermits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	api := cedarWiringAPI(t, "")
	ctx := context.Background()

	saved, err := api.Workflows().Save(ctx, workflowsview.SaveInput{YAML: webFetchWorkflowYAML(srv.URL + "/page")})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	store := api.settingsImpl.Store()
	if store == nil {
		t.Fatal("settings store is nil — construction changed")
	}

	// Arm 1: permissive (default) — the fetch proceeds.
	res, err := api.Workflows().Run(ctx, saved.ID, nil)
	if err != nil {
		t.Fatalf("Run (permissive): %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("permissive run status = %q, want completed (err=%q, steps=%+v) — "+
			"a fixture using AllowAll would also pass this arm; this uses the REAL shipped bundle",
			res.Status, res.Err, res.Steps)
	}

	// Flip the dial live — no engine re-construction.
	if err := store.SaveCedarStrictWorkflowMode(true); err != nil {
		t.Fatalf("SaveCedarStrictWorkflowMode(true): %v", err)
	}

	// Arm 2: strict — the SAME workflow, on the SAME engine, must now be
	// denied. This is the arm that is unsatisfiable without UNIT-7's
	// explicit forbid rule (X-9) — a bare NetworkAuthorizer adapter
	// wired against absence-only strict policy would leave this GREEN
	// with the fetch still proceeding.
	res, err = api.Workflows().Run(ctx, saved.ID, nil)
	if err == nil && res.Status == "completed" {
		t.Fatal("web_fetch step completed under cedarStrictWorkflowMode=true — " +
			"strict mode denied nothing (X-9: NotApplicable is not enforcement)")
	}
	denied := false
	if len(res.Steps) > 0 {
		for _, s := range res.Steps {
			if s.Name == "fetch" && s.Status == "failed" {
				denied = true
			}
		}
	}
	if !denied && err == nil {
		t.Fatalf("expected the fetch step to fail under strict mode; run=%+v", res)
	}

	// Flip back — the dial is live in both directions.
	if err := store.SaveCedarStrictWorkflowMode(false); err != nil {
		t.Fatalf("SaveCedarStrictWorkflowMode(false): %v", err)
	}
	res, err = api.Workflows().Run(ctx, saved.ID, nil)
	if err != nil || res.Status != "completed" {
		t.Fatalf("run after flipping strict mode back off: status=%q err=%v/%q — the mode is being cached",
			res.Status, err, res.Err)
	}
}

// TestCedarWiring_WebScrape_StrictModeDenies covers the second call site
// (webScrapeRunner) — tasks.md UNIT-7 requires both, not just web_fetch.
func TestCedarWiring_WebScrape_StrictModeDenies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body>hi</body></html>"))
	}))
	defer srv.Close()

	api := cedarWiringAPI(t, "")
	ctx := context.Background()

	yaml := `
id: zz-wf-network-authz-scrape-probe
name: "wf network authz scrape probe"
version: 1
steps:
  - name: scrape
    kind: web_scrape
    url: "` + srv.URL + `/page"
    mode: "css"
`
	saved, err := api.Workflows().Save(ctx, workflowsview.SaveInput{YAML: yaml})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	store := api.settingsImpl.Store()
	if err := store.SaveCedarStrictWorkflowMode(true); err != nil {
		t.Fatalf("SaveCedarStrictWorkflowMode(true): %v", err)
	}

	res, _ := api.Workflows().Run(ctx, saved.ID, nil)
	if res.Status == "completed" {
		t.Fatal("web_scrape step completed under cedarStrictWorkflowMode=true — strict mode denied nothing")
	}
}
