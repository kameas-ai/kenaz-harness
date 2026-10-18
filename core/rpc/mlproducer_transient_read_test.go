package rpc

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/mlproducer"
)

// The 2026-10-13 dev log closed the ML gate with consent_read_failed during
// a dev-Fleet network outage. Only an OBSERVED withdrawal (Fleet says
// effective=false / not entitled / 403 ml_not_effective) may purge the
// outbox; a read Fleet never answered must pause shipping and keep every
// consented record, and the reopen must not purge either. Driven through
// the real fleet.Client so the error is the one production sees: a reset
// connection on GET /me/ml, then a 503.
func TestMLWiring_TransientConsentReadErrorPausesWithoutPurge(t *testing.T) {
	r := newMLWiringRig(t, mlDevOrg, nil)
	ctx := context.Background()
	if d := r.w.gate.Refresh(ctx); !d.Open {
		t.Fatalf("gate = %+v, want open", d)
	}
	r.toolCall("s1", "kenaz__read_file", `{"path":"a.go"}`, "x")
	before := len(r.outbox())
	if before == 0 {
		t.Fatal("fixture: nothing recorded with the gate open")
	}

	for _, outage := range []struct {
		name string
		set  func(*mlFleet)
	}{
		{"connection reset", func(f *mlFleet) { f.meMLDrop = true }},
		{"503", func(f *mlFleet) { f.meMLDrop = false; f.meMLStatus, f.meML = 503, `{"code":"unavailable"}` }},
	} {
		r.fleet.set(outage.set)
		d := r.w.gate.Refresh(ctx)
		if d.Open || d.Reason != mlproducer.ReasonConsentReadError || d.Definitive {
			t.Fatalf("%s: decision = %+v, want closed, %q, not definitive", outage.name, d, mlproducer.ReasonConsentReadError)
		}
		if r.w.gate.Recording() {
			t.Fatalf("%s: Recording() true while consent cannot be read", outage.name)
		}
		if got := len(r.outbox()); got != before {
			t.Fatalf("%s: outbox = %d, want %d — a transport error purged consented records", outage.name, got, before)
		}
	}

	// Fleet comes back, still effective: reopen, nothing lost, and it ships.
	r.fleet.set(func(f *mlFleet) { f.meMLStatus, f.meML = 200, mlEffectiveBody(true, "on", false, true) })
	if d := r.w.gate.Refresh(ctx); !d.Open {
		t.Fatalf("gate after recovery = %+v, want open", d)
	}
	if got := len(r.outbox()); got != before {
		t.Fatalf("outbox after close→open = %d, want %d — the reopen purged", got, before)
	}
	r.shipNow()
	if posts, _, _, _ := r.fleet.snapshot(); len(posts) != 1 || otlpRecordCount(posts[0]) != before {
		t.Fatalf("posts = %d, want one batch carrying the %d records kept through the outage", len(posts), before)
	}
}
