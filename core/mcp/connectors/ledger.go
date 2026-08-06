// ledger.go — served-harness connector lifecycle → reporter ledger bridge
// (spec 091 FR-014).
//
// Mirrors cmd/harness-vm/ledger.go's frozen ingest contract (sigil
// internal/event.Event shape, NDJSON over the reporter ingest socket) so
// the host audit timeline shows connector activity alongside the existing
// task.* taxonomy:
//
//	{"kind":"ai","source":"harness","payload":{
//	   "phase":"connector.enabled",   // connector.enabled | connector.spawn | connector.tool_call
//	   "connector_id":"<recipe id>",
//	   "workbench_id":"<id>",
//	   "result":"ok|fail",            // connector.spawn only
//	   "reason":"<class>",            // connector.spawn fail only — reason CLASS, never error bytes
//	   "tool":"<name>"                // connector.tool_call only — tool NAME, never arguments
//	 },"timestamp":"..."}
//
// Privacy: connector id and tool name only. Tool arguments, credential
// bytes, env values, and raw error strings never cross this bridge.
package connectors

import (
	"encoding/json"
	"log/slog"
	"net"
	"sync"
	"time"
)

// Env vars naming the reporter ingest endpoint + workbench attribution
// inside the VM. Same contract as cmd/harness-vm.
const (
	EnvLedgerSocket = "SIGIL_INGEST_SOCKET"
	EnvWorkbenchID  = "SIGIL_WORKBENCH_ID"
)

// Connector ledger phases. Stable strings — the reporter and the host
// audit timeline key off these.
const (
	phaseConnectorEnabled  = "connector.enabled"
	phaseConnectorSpawn    = "connector.spawn"
	phaseConnectorToolCall = "connector.tool_call"
)

// ledgerEventKind / ledgerSource mirror sigil internal/event constants
// (event.KindAI); part of the frozen ingest contract.
const (
	ledgerEventKind = "ai"
	ledgerSource    = "harness"
)

// ledgerRecord is the on-wire shape pushed to the reporter ingest socket.
type ledgerRecord struct {
	Kind      string         `json:"kind"`
	Source    string         `json:"source"`
	Payload   map[string]any `json:"payload"`
	Timestamp time.Time      `json:"timestamp"`
}

// LedgerEmitter writes connector-lifecycle records to the reporter ingest
// socket. Fire-and-forget with short deadlines: a slow or absent reporter
// degrades to "no audit record", never to a stalled boot or tool call.
// A nil *LedgerEmitter and an emitter with no socket are both valid
// no-op receivers.
type LedgerEmitter struct {
	addr         string // unix socket path; "" disables
	network      string // "unix" (default) — settable for tests ("tcp")
	workbenchID  string
	dialTimeout  time.Duration
	writeTimeout time.Duration
	log          *slog.Logger

	mu     sync.Mutex
	dialFn func(network, addr string) (net.Conn, error)
}

// NewLedgerEmitterFromEnv builds an emitter from the standard in-VM env
// vars. When SIGIL_INGEST_SOCKET is unset the emitter is disabled.
func NewLedgerEmitterFromEnv(getenv func(string) string, log *slog.Logger) *LedgerEmitter {
	return newLedgerEmitter(getenv(EnvLedgerSocket), "", getenv(EnvWorkbenchID), log)
}

func newLedgerEmitter(addr, network, workbenchID string, log *slog.Logger) *LedgerEmitter {
	if network == "" {
		network = "unix"
	}
	if log == nil {
		log = slog.Default()
	}
	const dialTimeout = 500 * time.Millisecond
	return &LedgerEmitter{
		addr:         addr,
		network:      network,
		workbenchID:  workbenchID,
		dialTimeout:  dialTimeout,
		writeTimeout: 500 * time.Millisecond,
		log:          log,
		dialFn: func(network, addr string) (net.Conn, error) {
			return net.DialTimeout(network, addr, dialTimeout)
		},
	}
}

// enabled reports whether emission is active (socket configured).
func (e *LedgerEmitter) enabled() bool {
	return e != nil && e.addr != ""
}

// EmitEnabled records that a whitelisted connector was enabled at boot.
func (e *LedgerEmitter) EmitEnabled(connectorID string) {
	e.emit(phaseConnectorEnabled, connectorID, nil)
}

// EmitSpawn records a spawn attempt's outcome. reasonClass is a stable
// classification (e.g. "missing_env", "open_failed") — never raw error
// text, which could carry paths or argv fragments.
func (e *LedgerEmitter) EmitSpawn(connectorID string, ok bool, reasonClass string) {
	extra := map[string]any{"result": "ok"}
	if !ok {
		extra["result"] = "fail"
		if reasonClass != "" {
			extra["reason"] = reasonClass
		}
	}
	e.emit(phaseConnectorSpawn, connectorID, extra)
}

// EmitToolCall records a tool invocation by connector id + tool NAME only.
// Arguments never cross the bridge (FR-014).
func (e *LedgerEmitter) EmitToolCall(connectorID, tool string) {
	e.emit(phaseConnectorToolCall, connectorID, map[string]any{"tool": tool})
}

func (e *LedgerEmitter) emit(phase, connectorID string, extra map[string]any) {
	if !e.enabled() {
		return
	}

	payload := map[string]any{
		"phase":        phase,
		"connector_id": connectorID,
		"workbench_id": e.workbenchID,
	}
	for k, v := range extra {
		payload[k] = v
	}

	rec := ledgerRecord{
		Kind:      ledgerEventKind,
		Source:    ledgerSource,
		Payload:   payload,
		Timestamp: time.Now().UTC(),
	}

	line, err := json.Marshal(rec)
	if err != nil {
		e.log.Debug("connectors.ledger: marshal failed", "phase", phase, "err", err)
		return
	}
	line = append(line, '\n')

	e.mu.Lock()
	dial := e.dialFn
	e.mu.Unlock()

	conn, err := dial(e.network, e.addr)
	if err != nil {
		e.log.Debug("connectors.ledger: dial reporter ingest failed (record dropped)",
			"phase", phase, "addr", e.addr, "err", err)
		return
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetWriteDeadline(time.Now().Add(e.writeTimeout))
	if _, err := conn.Write(line); err != nil {
		e.log.Debug("connectors.ledger: write reporter ingest failed (record dropped)",
			"phase", phase, "err", err)
		return
	}
	e.log.Debug("connectors.ledger: emitted", "phase", phase, "connector_id", connectorID)
}
