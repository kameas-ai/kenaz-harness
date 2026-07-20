package telemetry

import (
	"context"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/logstore"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// installSlogBridge must wrap the CURRENT handler chain, not the raw file
// handler. rpc.New() installs a logstore ring-buffer TEE (which feeds the in-app
// Logs view) via logging.Replace before core.Start() runs; if installSlogBridge
// wraps logging.FileHandler() instead of logging.Handler(), that TEE is orphaned
// on boot and the Logs tab is empty in production. Regression guard for the
// 01NLOGS01 review fix.
func TestInstallSlogBridge_PreservesLogstoreTEE(t *testing.T) {
	// Mutates the process-global logging handler — not parallel; restore after.
	prev := logging.Handler()
	t.Cleanup(func() { logging.Replace(prev) })

	// Install a logstore TEE wrapping the previous handler (mirrors rpc.New()).
	store := logstore.New(100)
	logging.Replace(logstore.NewHandler(store, prev))

	// Simulate core.Start() -> initTelemetry() -> installSlogBridge with a real
	// (exporter-less) LoggerProvider.
	lp := sdklog.NewLoggerProvider()
	t.Cleanup(func() { _ = lp.Shutdown(context.Background()) })
	installSlogBridge(lp, nil)

	const marker = "logstore-tee-survives-slogbridge-marker"
	logging.L().Info(marker)

	for _, r := range store.Snapshot() {
		if strings.Contains(r.Message, marker) {
			return // captured — TEE survived the bridge install
		}
	}
	t.Fatal("logstore did not capture the record after installSlogBridge — the slog TEE was orphaned (bridge wrapped the raw file handler instead of the current chain)")
}
