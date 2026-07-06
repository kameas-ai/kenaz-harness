package sites

// export_test.go exposes internal constructors for the external test package.
// Only compiled during `go test` runs; never included in the production binary.

// NewForTesting is the test-accessible constructor that accepts a FleetSitesClient
// interface and a fleetEnabled flag, bypassing the concrete *corefleet.Client
// requirement of New(). This lets unit tests supply a fake client.
func NewForTesting(fc FleetSitesClient, fleetEnabled bool, dataDir string, emitter Emitter) *SitesImpl {
	return newWithClient(fc, fleetEnabled, dataDir, emitter)
}
