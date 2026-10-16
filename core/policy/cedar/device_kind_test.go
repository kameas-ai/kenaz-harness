package cedar

import (
	"context"
	"testing"

	cedarlib "github.com/cedar-policy/cedar-go"
)

// device_kind (provider-credentials-client-01PCCLI01 H6.3) is on every
// request: "desktop" by default, so a mobile-only forbid neither errors
// nor fires on the desktop, and it fires where DeviceKind is "mobile".
func TestEvaluate_DeviceKindStampedOnEveryRequest(t *testing.T) {
	snips := map[string][]byte{"dk.cedar": []byte(`permit(principal, action, resource);
forbid(principal, action, resource) when { context.device_kind == "mobile" };`)}
	for _, tc := range []struct {
		kind string
		want Outcome
	}{{"", Allow}, {DeviceKindDesktop, Allow}, {"mobile", Deny}} {
		e, err := NewEngine(Options{DeviceKind: tc.kind})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.LoadHarnessSnippets(snips); err != nil {
			t.Fatal(err)
		}
		for _, action := range []string{ActionUseTool, ActionFileRead, ActionToolExec} {
			d := e.Evaluate(context.Background(), UserUID(), action, PermissionToolUID("x"), nil)
			if d.Outcome != tc.want {
				t.Errorf("DeviceKind=%q %s: outcome %s (%s), want %s", tc.kind, action, d.Outcome, d.Reason, tc.want)
			}
		}
	}
}

// A caller-set device_kind wins, and the caller's map is not mutated.
func TestEvaluate_CallerDeviceKindWinsAndMapUnmutated(t *testing.T) {
	in := map[cedarlib.String]cedarlib.Value{"session_kind": cedarlib.String("chat")}
	out := withDeviceKind(in, "")
	if _, ok := in[cedarlib.String(CtxKeyDeviceKind)]; ok {
		t.Fatal("withDeviceKind mutated the caller's map")
	}
	if out[cedarlib.String(CtxKeyDeviceKind)] != cedarlib.String(DeviceKindDesktop) {
		t.Fatalf("device_kind = %v, want desktop", out[cedarlib.String(CtxKeyDeviceKind)])
	}
	set := map[cedarlib.String]cedarlib.Value{cedarlib.String(CtxKeyDeviceKind): cedarlib.String("mobile")}
	if got := withDeviceKind(set, DeviceKindDesktop); got[cedarlib.String(CtxKeyDeviceKind)] != cedarlib.String("mobile") {
		t.Fatal("a caller-set device_kind was overwritten")
	}
}
