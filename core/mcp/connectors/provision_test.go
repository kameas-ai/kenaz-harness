package connectors

import (
	"log/slog"
	"reflect"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

// TestParseAllowlist is the fail-closed parsing table (spec 091 FR-004):
// absent, empty, or ANY malformed id must yield the block-all outcome —
// the list is never partially applied.
func TestParseAllowlist(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  Provisioning
	}{
		{"absent/empty", "", Provisioning{Reason: ReasonNotProvisioned}},
		{"whitespace only", "   \t", Provisioning{Reason: ReasonNotProvisioned}},
		{"single valid", "datadog", Provisioning{Provisioned: true, IDs: []string{"datadog"}}},
		{"multiple valid", "datadog,slack,google-drive",
			Provisioning{Provisioned: true, IDs: []string{"datadog", "slack", "google-drive"}}},
		{"trimmed entries", " datadog , slack ",
			Provisioning{Provisioned: true, IDs: []string{"datadog", "slack"}}},
		{"duplicates collapsed", "slack,slack,datadog",
			Provisioning{Provisioned: true, IDs: []string{"slack", "datadog"}}},
		{"empty entry is malformed", "datadog,,slack", Provisioning{Reason: ReasonMalformed}},
		{"trailing comma is malformed", "datadog,", Provisioning{Reason: ReasonMalformed}},
		{"uppercase is malformed", "Datadog", Provisioning{Reason: ReasonMalformed}},
		{"underscore is malformed", "google_drive", Provisioning{Reason: ReasonMalformed}},
		{"leading digit is malformed", "1password", Provisioning{Reason: ReasonMalformed}},
		{"path chars are malformed", "../etc", Provisioning{Reason: ReasonMalformed}},
		{"one bad id poisons the list", "datadog,NOT VALID,slack", Provisioning{Reason: ReasonMalformed}},
		{"over-long id is malformed",
			"a234567890123456789012345678901234567890123456789012345678901234x", // 65 chars
			Provisioning{Reason: ReasonMalformed}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseAllowlist(tc.value)
			if got.Provisioned != tc.want.Provisioned || got.Reason != tc.want.Reason ||
				!reflect.DeepEqual(got.IDs, tc.want.IDs) {
				t.Errorf("ParseAllowlist(%q) = %+v, want %+v", tc.value, got, tc.want)
			}
		})
	}
}

// TestProvisionFromEnv_BlockAllByDefault verifies the global side effect:
// an unprovisioned served boot installs a sealed block-all allow-list.
func TestProvisionFromEnv_BlockAllByDefault(t *testing.T) {
	getenv := func(string) string { return "" }
	prov := ProvisionFromEnv(getenv, slog.Default())
	if prov.Provisioned {
		t.Fatal("Provisioned = true for absent env var")
	}
	if recipes.IsAllowed("datadog") {
		t.Error("block-all not in force after unprovisioned boot")
	}
	if !recipes.AllowlistSealed() {
		t.Error("allow-list not sealed after served provisioning")
	}
}

// TestProvisionFromEnv_ValidList verifies the applied whitelist gates
// IsAllowed and stays sealed against fleet writes.
func TestProvisionFromEnv_ValidList(t *testing.T) {
	getenv := func(k string) string {
		if k == EnvMCPAllowlist {
			return "datadog,slack"
		}
		return ""
	}
	prov := ProvisionFromEnv(getenv, slog.Default())
	if !prov.Provisioned {
		t.Fatalf("Provisioned = false, reason %q", prov.Reason)
	}
	if !recipes.IsAllowed("datadog") || !recipes.IsAllowed("slack") {
		t.Error("whitelisted ids not allowed")
	}
	if recipes.IsAllowed("github") {
		t.Error("non-whitelisted id allowed")
	}
	recipes.ApplyFleetAllowlist(nil) // must be a no-op — sole-writer rule
	if recipes.IsAllowed("github") {
		t.Error("fleet write re-opened a sealed served allow-list")
	}
}

// TestProvisionFromEnv_Malformed verifies a present-but-invalid value is
// treated exactly as absence: block-all.
func TestProvisionFromEnv_Malformed(t *testing.T) {
	getenv := func(k string) string {
		if k == EnvMCPAllowlist {
			return "datadog,BAD ID"
		}
		return ""
	}
	prov := ProvisionFromEnv(getenv, slog.Default())
	if prov.Provisioned || prov.Reason != ReasonMalformed {
		t.Fatalf("got %+v, want malformed block-all", prov)
	}
	if recipes.IsAllowed("datadog") {
		t.Error("partially applied a malformed list")
	}
}
