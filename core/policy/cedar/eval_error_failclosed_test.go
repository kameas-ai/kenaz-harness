package cedar

import (
	"context"
	"strings"
	"testing"
	"time"

	cedarlib "github.com/cedar-policy/cedar-go"
)

// The §9.1 reproduction (provider-credentials-client-01PCCLI01): a
// forbid whose `when` reads a context attribute the request lacks
// errors during evaluation. cedar-go skips it and lists it only in
// diag.Errors, so before the fix the blanket permit won → Allow.
const reproPolicy = `
permit(principal, action, resource);
forbid(principal, action, resource) when { context.missing_attr == "mobile" };
`

func evalErrEngine(t *testing.T, src string) *Engine {
	t.Helper()
	e, err := NewEngine(Options{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err := e.SetPolicyText("repro.cedar", []byte(src)); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}
	return e
}

func TestErroringForbid_FailsClosed_Repro(t *testing.T) {
	e := evalErrEngine(t, reproPolicy)
	d := e.Evaluate(context.Background(), UserUID(), ActionFileRead, FilesystemUID("/tmp/x"), nil)
	if d.Outcome != Deny {
		t.Fatalf("erroring forbid + permit: outcome = %v (%s), want Deny", d.Outcome, d.Reason)
	}
	if d.Reason != "forbid policy could not be evaluated (fail-closed)" {
		t.Fatalf("reason = %q", d.Reason)
	}
	if d.MatchedPolicy == "" {
		t.Fatal("MatchedPolicy empty; want the erroring forbid's id")
	}
	// With the attribute present and not "mobile" the forbid evaluates
	// cleanly to false → the permit wins.
	d = e.Evaluate(context.Background(), UserUID(), ActionFileRead, FilesystemUID("/tmp/x"),
		map[cedarlib.String]cedarlib.Value{"missing_attr": cedarlib.String("desktop")})
	if d.Outcome != Allow {
		t.Fatalf("missing_attr=desktop: outcome = %v, want Allow", d.Outcome)
	}
	// And "mobile" matches the forbid outright.
	d = e.Evaluate(context.Background(), UserUID(), ActionFileRead, FilesystemUID("/tmp/x"),
		map[cedarlib.String]cedarlib.Value{"missing_attr": cedarlib.String("mobile")})
	if d.Outcome != Deny || d.Reason != "forbid policy matched" {
		t.Fatalf("missing_attr=mobile: %v / %q, want Deny / forbid policy matched", d.Outcome, d.Reason)
	}
}

func TestErroringForbid_BeatsMatchingPermit(t *testing.T) {
	e := evalErrEngine(t, `
permit(principal, action == Action::"file_read", resource);
forbid(principal, action == Action::"file_read", resource) when { context.missing_attr == "x" };
`)
	d := e.Evaluate(context.Background(), UserUID(), ActionFileRead, FilesystemUID("/tmp/x"), nil)
	if d.Outcome != Deny {
		t.Fatalf("outcome = %v (%s), want Deny", d.Outcome, d.Reason)
	}
}

func TestErroringPermit_NoOtherMatch_Unchanged(t *testing.T) {
	src := `permit(principal, action, resource) when { context.missing_attr == "x" };`
	for _, defaultDeny := range []bool{false, true} {
		e, err := NewEngine(Options{DefaultDeny: defaultDeny})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.SetPolicyText("p.cedar", []byte(src)); err != nil {
			t.Fatal(err)
		}
		d := e.Evaluate(context.Background(), UserUID(), ActionFileRead, FilesystemUID("/tmp/x"), nil)
		want := NotApplicable
		if defaultDeny {
			want = Deny
		}
		if d.Outcome != want {
			t.Fatalf("defaultDeny=%v: outcome = %v (%s), want %v", defaultDeny, d.Outcome, d.Reason, want)
		}
		if strings.Contains(d.Reason, "could not be evaluated") {
			t.Fatalf("erroring permit reported as fail-closed forbid: %q", d.Reason)
		}
	}
}

func TestErroringPermit_OtherPermitStillAllows(t *testing.T) {
	e := evalErrEngine(t, `
permit(principal, action, resource) when { context.missing_attr == "x" };
permit(principal, action, resource);
`)
	d := e.Evaluate(context.Background(), UserUID(), ActionFileRead, FilesystemUID("/tmp/x"), nil)
	if d.Outcome != Allow {
		t.Fatalf("outcome = %v (%s), want Allow", d.Outcome, d.Reason)
	}
}

func TestEvalErrors_Surfaced(t *testing.T) {
	e := evalErrEngine(t, reproPolicy)
	for i := 0; i < 3; i++ {
		e.Evaluate(context.Background(), UserUID(), ActionFileRead, FilesystemUID("/secret/path"),
			map[cedarlib.String]cedarlib.Value{"other": cedarlib.String("SENSITIVE-VALUE")})
	}
	errs := e.PolicyEvalErrors()
	if len(errs) != 1 {
		t.Fatalf("PolicyEvalErrors = %+v, want exactly one", errs)
	}
	pe := errs[0]
	if pe.Effect != "forbid" || pe.Count != 3 || pe.File != "repro.cedar" {
		t.Fatalf("record = %+v, want effect=forbid count=3 file=repro.cedar", pe)
	}
	if !strings.Contains(pe.Message, "missing_attr") {
		t.Fatalf("message %q does not name the missing attribute", pe.Message)
	}
	if strings.Contains(pe.Message, "SENSITIVE") || strings.Contains(pe.Message, "/secret/path") {
		t.Fatalf("message leaks request data: %q", pe.Message)
	}

	// ListPolicies attaches the record to its file (the policy view's
	// status surface).
	files := e.ListPolicies()
	if len(files) != 1 || len(files[0].EvalErrors) != 1 || files[0].EvalErrors[0].PolicyID != pe.PolicyID {
		t.Fatalf("ListPolicies = %+v, want the eval error on repro.cedar", files)
	}

	// Replacing the policy text clears the record.
	if err := e.SetPolicyText("repro.cedar", []byte(`permit(principal, action, resource);`)); err != nil {
		t.Fatal(err)
	}
	if got := e.PolicyEvalErrors(); len(got) != 0 {
		t.Fatalf("after SetPolicyText: %+v, want none", got)
	}
}

func TestEvalErrors_LogRateLimited(t *testing.T) {
	var tr evalErrorTracker
	now := time.Unix(1_700_000_000, 0)
	tr.now = func() time.Time { return now }
	ps := cedarlib.NewPolicySet()
	errs := []cedarlib.DiagnosticError{{PolicyID: "p", Message: "record does not have the attribute `x`"}}
	tr.record(ps, errs)
	first := tr.entries["p"].lastLogged
	now = now.Add(time.Minute)
	tr.record(ps, errs)
	if !tr.entries["p"].lastLogged.Equal(first) {
		t.Fatal("re-logged within the rate-limit interval")
	}
	now = now.Add(evalErrorLogInterval)
	tr.record(ps, errs)
	if tr.entries["p"].lastLogged.Equal(first) {
		t.Fatal("did not re-log after the interval elapsed")
	}
	if tr.entries["p"].Count != 3 {
		t.Fatalf("count = %d, want 3", tr.entries["p"].Count)
	}
}

func TestSanitizeEvalErrorMessage(t *testing.T) {
	cases := map[string]string{
		"record does not have the attribute `missing_attr`":                        "record does not have the attribute `missing_attr`",
		"`Tool::\"secret-tool\"` does not have the attribute `owner`":             "entity does not have the attribute `owner`",
		"entity `User::\"alice\"` does not exist":                                 "entity does not exist",
		"integer overflow while attempting to add `9223372036854775807` with `1`": "integer overflow",
		"type error: expected string, got long":                                   "type error: expected string, got long",
		"error parsing decimal value: value 99e9 would overflow":                  "error parsing decimal value (detail redacted)",
	}
	for in, want := range cases {
		if got := sanitizeEvalErrorMessage(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}
