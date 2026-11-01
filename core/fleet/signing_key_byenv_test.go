package fleet

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"testing"
)

// Contract #4 public keys (fleet-policy push-down brief, 2026-10-31).
const (
	pinDev   = "6e4fedb22e05edc88e60ad195b59f4362e776ee92c8ed3ee1e67af0beb286919"
	pinStage = "9f22a054ba4e000d9848ee41dcf4ba812c9a2f8f065e0a9403ab5f194a87152a"
	pinProd  = "876d8e4082520ccbb01bfd4ab4b6fc2eb346f7e8259a01a87e784225fc190e57"
)

// withPins sets the three pin vars for one test (no t.Parallel: package vars).
func withPins(t *testing.T, set, legacy, byEnv string) {
	t.Helper()
	s, l, b := fleetSigningPublicKeys, fleetSigningPublicKeyBytes, fleetSigningPublicKeysByEnv
	fleetSigningPublicKeys, fleetSigningPublicKeyBytes, fleetSigningPublicKeysByEnv = set, legacy, byEnv
	t.Cleanup(func() { fleetSigningPublicKeys, fleetSigningPublicKeyBytes, fleetSigningPublicKeysByEnv = s, l, b })
}

func keysHex(keys []ed25519.PublicKey) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = hex.EncodeToString(k)
	}
	return out
}

const allEnvs = "dev=" + pinDev + ";stage=" + pinStage + ";prod=" + pinProd

func TestSigningKeysByEnv_Selection(t *testing.T) {
	withPins(t, "", "", allEnvs)
	for env, want := range map[string]string{"dev": pinDev, "stage": pinStage, "prod": pinProd, "": pinProd, "PROD": pinProd} {
		t.Setenv("KENAZ_HARNESS_ENV", env)
		got := keysHex(FleetSigningKeys())
		if len(got) != 1 || got[0] != want {
			t.Errorf("env %q: keys = %v, want [%s]", env, got, want)
		}
		if err := FleetSigningKeySetError(); err != nil {
			t.Errorf("env %q: unexpected set error %v", env, err)
		}
	}
}

func TestSigningKeysByEnv_RotationListAndWhitespace(t *testing.T) {
	withPins(t, "", "", " dev = "+pinDev+" , "+pinStage+" ; prod="+pinProd+";")
	t.Setenv("KENAZ_HARNESS_ENV", "dev")
	if got := FleetSigningKeys(); len(got) != 2 {
		t.Fatalf("dev rotation list: got %d keys, want 2", len(got))
	}
}

func TestSigningKeysByEnv_FailClosed(t *testing.T) {
	cases := map[string]struct{ byEnv, env string }{
		"env with no entry":   {"dev=" + pinDev, "stage"},
		"local with no entry": {allEnvs, "local"},
		"unknown env name":    {"qa=" + pinDev + ";dev=" + pinDev, "dev"},
		"duplicate env":       {"dev=" + pinDev + ";dev=" + pinStage, "dev"},
		"malformed group":     {"dev" + pinDev, "dev"},
		"bad pin":             {"dev=abcd;prod=" + pinProd, "prod"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// A valid legacy set is present: ByEnv must still win (fail closed).
			withPins(t, pinProd, "", tc.byEnv)
			t.Setenv("KENAZ_HARNESS_ENV", tc.env)
			if got := FleetSigningKeys(); len(got) != 0 {
				t.Fatalf("keys = %v, want none (fail closed)", keysHex(got))
			}
			if ConfigDistributionEnabled() {
				t.Error("ConfigDistributionEnabled = true, want false")
			}
			b := &Bundle{BundleID: 1, Signature: "AA=="}
			if err := VerifyWithKeySet(b, FleetSigningKeys(), 0); !errors.Is(err, ErrSigningKeyNotConfigured) {
				t.Errorf("verify err = %v, want ErrSigningKeyNotConfigured", err)
			}
		})
	}
	// Malformed values surface through FleetSigningKeySetError; a merely
	// missing entry is a well-formed config with no keys for this env.
	withPins(t, "", "", "dev=abcd")
	if err := FleetSigningKeySetError(); !errors.Is(err, ErrSigningKeyNotConfigured) {
		t.Errorf("FleetSigningKeySetError = %v", err)
	}
}

func TestSigningKeysByEnv_PrecedenceOverLegacy(t *testing.T) {
	withPins(t, pinDev, pinStage, allEnvs)
	t.Setenv("KENAZ_HARNESS_ENV", "prod")
	got := keysHex(FleetSigningKeys())
	if len(got) != 1 || got[0] != pinProd {
		t.Fatalf("keys = %v, want only the prod per-env pin", got)
	}
	// Without ByEnv the single-set vars still work (desktop release builds).
	withPins(t, pinDev, "", "")
	got = keysHex(FleetSigningKeys())
	if len(got) != 1 || got[0] != pinDev {
		t.Fatalf("legacy set: keys = %v", got)
	}
}
