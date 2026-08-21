package bundle

// TestInstall_HTTPMirror_FR002Leg is UNIT-6's AC-005 obligation (spec
// FR-002): "a bundle is installed from a URL the test serves, through
// channels.Registry.Open → Channel.Fetch". This exercises Install's
// full UNIT-5 pipeline against the http_mirror channel via a
// TEST-LOCAL registry built with WithChannelRegistry — the production
// registry constructed in core/rpc/api.go does NOT register
// http_mirror until UNIT-7's commit (plan.md Rule 2: the gate must
// exist before the fetch-from-a-caller-supplied-URL primitive is
// armed in production wiring). Exercising the pipeline here, package-
// scoped and gated behind an explicit test-only registry, is exactly
// what UNIT-6's task file sanctions: "it is acceptable for UNIT-6 to
// land the channel package and its tests; it is not acceptable for a
// commit to register the http factory into the production registry."

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/bundle/cache"
	"github.com/kameas-ai/kenaz-harness/core/bundle/channels"
	httpchannel "github.com/kameas-ai/kenaz-harness/core/bundle/channels/http"
	"github.com/kameas-ai/kenaz-harness/core/bundle/channels/localpath"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

func TestInstall_HTTPMirror_FR002Leg(t *testing.T) {
	artifactBytes := []byte("bytes served over http_mirror for FR-002")
	digest := sha256Hex(artifactBytes)
	manifestYAML := `schema_version: 1
name: remote-bundle
version: 1.0.0
license: MIT
artifacts:
  - name: policy.toml
    kind: policy
    path: policy/policy.toml
    content_hash: "` + digest + `"
`
	mux := http.NewServeMux()
	mux.HandleFunc("/kenaz.yaml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(manifestYAML))
	})
	mux.HandleFunc("/policy/policy.toml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(artifactBytes)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// A test-local registry with BOTH local_path (harmless, unused
	// here) and http_mirror registered — never the production registry
	// NewDefaultRegistry() builds, which UNIT-7 alone extends with
	// http_mirror.
	reg := channels.NewRegistry()
	if err := reg.Register(localpath.Kind, localpath.Factory); err != nil {
		t.Fatalf("register local_path: %v", err)
	}
	if err := reg.Register(httpchannel.Kind, httpchannel.Factory); err != nil {
		t.Fatalf("register http_mirror: %v", err)
	}

	rw := &memReadWriter{}
	casDir := t.TempDir()
	realCAS, err := cache.New(casDir)
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	api := NewAPI(
		WithReader(rw), WithWriter(rw),
		WithCAS(CASFromCache(realCAS)),
		WithChannelRegistry(reg),
		WithSecretsResolver(secrets.NoopResolver{}),
	)

	got, err := api.Install(context.Background(), InstallRequest{Kind: httpchannel.Kind, URL: srv.URL})
	if err != nil {
		t.Fatalf("Install over http_mirror: %v", err)
	}
	if got.Name != "remote-bundle" {
		t.Errorf("Install returned %+v", got)
	}
	if strings.Contains(got.Tier, "(uncached)") {
		t.Errorf("freshly-installed remote bundle should not carry (uncached); got tier=%q", got.Tier)
	}
	if !realCAS.Has(digest) {
		t.Fatalf("artifact digest %s not present in CAS after an http_mirror install", digest)
	}

	// The whole point of routing through the CAS: no local directory
	// backs this install at all (Path was never set), and Get still
	// resolves the artifact from the CAS.
	got2, err := api.Get(context.Background(), "remote-bundle")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got2.Artifacts) != 1 || got2.Artifacts[0].ContentHash != digest {
		t.Fatalf("Get = %+v, want 1 artifact with digest %s", got2.Artifacts, digest)
	}
}
