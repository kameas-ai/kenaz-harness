package cedar

import (
	"context"
	"testing"
)

// graph-fs-gate-01GFSG01 WP05 (review F1): a corrupt USER policy file
// that shares its name with an embedded default or a harness/graph
// snippet must still be reported by UserPolicyLoadError, so the graph
// path fails closed and the Policy view lists it.
//
// Before WP05: (a) Reload pinned a parse error on the FIRST files entry
// with a matching Name — the embedded one — leaving the user entry
// ParseOK; (b) applySnippets set ParseOK=true on ANY name match, wiping
// a corrupt user file's error on every Reload.

func userEntryFailed(e *Engine, name string) bool {
	for _, f := range e.ListPolicies() {
		if f.Name == name && !f.Embedded && !f.ParseOK && f.ParseErr != "" {
			return true
		}
	}
	return false
}

func TestParseError_UserFileShadowingEmbeddedDefault_FailsClosed(t *testing.T) {
	e, _ := newDiskEngine(t, map[string]string{DefaultPolicyName: "permit ( {{{"})
	if e.UserPolicyLoadError() == nil {
		t.Fatalf("corrupt user %s not reported by UserPolicyLoadError: %+v", DefaultPolicyName, e.ListPolicies())
	}
	if !userEntryFailed(e, DefaultPolicyName) {
		t.Fatalf("parse error not attributed to the USER %s entry: %+v", DefaultPolicyName, e.ListPolicies())
	}
	for _, f := range e.ListPolicies() {
		if f.Name == DefaultPolicyName && f.Embedded && !f.ParseOK {
			t.Fatalf("embedded %s wrongly marked failed: %+v", DefaultPolicyName, f)
		}
	}
	if !IsPolicyDenied(CheckFileWrite(context.Background(), FailClosedOnLoadError(e, nil), "/tmp/x")) {
		t.Fatal("graph file_write not denied with a corrupt user file shadowing an embedded default")
	}
}

func TestParseError_UserFileShadowingSnippet_SurvivesReload(t *testing.T) {
	const name = "harness_write_forbid.cedar"
	e, _ := newDiskEngine(t, map[string]string{name: "forbid ( {{{"})
	if err := e.LoadHarnessSnippets(map[string][]byte{name: []byte(snipForbidMemory)}); err != nil {
		t.Fatal(err)
	}
	if err := e.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.UserPolicyLoadError() == nil {
		t.Fatalf("corrupt user %s error wiped by snippet re-apply: %+v", name, e.ListPolicies())
	}
	if !userEntryFailed(e, name) {
		t.Fatalf("user %s entry not reported failed: %+v", name, e.ListPolicies())
	}
	if !snippetListed(e, name) {
		t.Fatalf("snippet %s no longer listed as embedded: %+v", name, e.ListPolicies())
	}
	if !IsPolicyDenied(CheckMemoryWrite(context.Background(), e, "global")) {
		t.Fatal("retained snippet no longer active")
	}
}
