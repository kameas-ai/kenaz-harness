package fleet

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// AttrEventKind is the OTLP *log-record* attribute that names the telemetry
// event a record carries.
//
// Note the placement: kenaz-fleet's schema package documents this as a
// "resource attribute", but the receiver reads it off each log record
// (service/telemetry/receiver.go — `logAttr(lr.GetAttributes(), …)`). The code
// is authoritative; this mirror follows the code, not the doc comment.
const AttrEventKind = "kameas.event.kind"

// ── The compiled ceiling ─────────────────────────────────────────────────────

// This package embeds a VERBATIM vendored copy of the kind→class table from
// kenaz-fleet `service/telemetry/schema/v1/kind.go`. schema/source.json
// records which kenaz-fleet revision it came from plus the sha256 of the
// vendored bytes; log_event_kind_parity_test.go fails the build when either
// half drifts. Same anti-drift shape as kenaz's vendored harness MCP connector
// catalog (internal/connector/catalog: vendored file + source pin + parity
// gate), for the same reason — a hand-maintained mirror is a mirror for
// exactly one release.
//
// Updating is a two-file commit: re-vendor schema/kinds.json, update
// schema/source.json, cite the kenaz-fleet rev in the PR body.

//go:embed schema/kinds.json
var vendoredKindsJSON []byte

//go:embed schema/source.json
var vendoredKindsSourceJSON []byte

// LogEventKind is a telemetry event kind that Fleet's v1 schema recognises.
type LogEventKind string

// The v1 kind set, named so call sites can reference kinds symbolically. The
// authoritative membership test is LogEventKindAllowed, which reads the
// embedded table — these constants are convenience, not the source of truth.
const (
	LogKindHarnessConversationStarted LogEventKind = "harness.conversation_started"
	LogKindHarnessConversationEnded   LogEventKind = "harness.conversation_ended"
	LogKindHarnessToolInvoked         LogEventKind = "harness.tool_invoked"
	LogKindSigilStuckPredictor        LogEventKind = "sigil.stuck_predictor"
	LogKindSigilRoutingPolicy         LogEventKind = "sigil.routing_policy"
	LogKindSigilSuggestionEmitted     LogEventKind = "sigil.suggestion_emitted"
	LogKindSigilSuggestionOutcome     LogEventKind = "sigil.suggestion_outcome"
	LogKindHarnessError               LogEventKind = "harness.error"
)

// vendoredKindTable is the on-disk shape of schema/kinds.json.
type vendoredKindTable struct {
	SchemaVersion string `json:"schema_version"`
	Kinds         []struct {
		Kind  string `json:"kind"`
		Class string `json:"class"`
	} `json:"kinds"`
}

// VendoredKindSource is the provenance sidecar for the vendored table.
type VendoredKindSource struct {
	FleetRepo     string `json:"fleet_repo"`
	FleetRev      string `json:"fleet_rev"`
	FleetDescribe string `json:"fleet_describe"`
	SourcePath    string `json:"source_path"`
	SHA256        string `json:"sha256"`
	FetchedAt     string `json:"fetched_at"`
}

// logKindCeiling is the compiled-in kind→class table: the complete set of log
// event kinds this binary is CAPABLE of transmitting.
//
// # This is a ceiling, not a synchronised copy
//
// The harness allowlist and Fleet's receiver allowlist have different jobs.
// Fleet's decides what it will accept. This one decides what the user's
// machine will emit, and it is deliberately not remotely widenable: no server
// response, config pull, opt-in payload or policy document can add a kind to
// this map. Adding one requires shipping a new binary.
//
// That is the whole point. This is an egress control running on the user's
// machine, and constitution §XII condition 6 holds that authentication is not
// the boundary. If a Fleet response could widen what the client transmits, the
// privacy property degrades to "trust the server" — a compromised, coerced or
// merely misconfigured Fleet could instruct every client to start emitting
// kinds nobody consented to. A structural guarantee beats a policy one. Same
// shape as spec 091's org MCPServersPolicy (intersects with, never widens, the
// profile whitelist) and spec 074's relay config ratchet.
//
// Fleet retains every legitimate control it had, because narrowing still
// works: see LogKindsAdmittedBy.
var logKindCeiling map[LogEventKind]string

// vendoredKindSource is the parsed provenance sidecar.
var vendoredKindSource VendoredKindSource

func init() {
	var table vendoredKindTable
	if err := json.Unmarshal(vendoredKindsJSON, &table); err != nil {
		// The table is an embedded build input; a malformed one is a build
		// defect, not a runtime condition. Failing loudly at init beats
		// silently booting with an empty ceiling (which would be safe but
		// would also disable telemetry with no explanation).
		panic(fmt.Sprintf("fleet: malformed embedded schema/kinds.json: %v", err))
	}
	if err := json.Unmarshal(vendoredKindsSourceJSON, &vendoredKindSource); err != nil {
		panic(fmt.Sprintf("fleet: malformed embedded schema/source.json: %v", err))
	}
	logKindCeiling = make(map[LogEventKind]string, len(table.Kinds))
	for _, k := range table.Kinds {
		if k.Kind == "" || k.Class == "" {
			panic(fmt.Sprintf("fleet: embedded schema/kinds.json has an incomplete row: %+v", k))
		}
		logKindCeiling[LogEventKind(k.Kind)] = k.Class
	}
}

// VendoredKindSchemaSHA256 returns the hex sha256 of the embedded
// schema/kinds.json bytes. Used by the parity gate.
func VendoredKindSchemaSHA256() string {
	sum := sha256.Sum256(vendoredKindsJSON)
	return hex.EncodeToString(sum[:])
}

// VendoredKindSchemaSource returns the parsed provenance sidecar.
func VendoredKindSchemaSource() VendoredKindSource { return vendoredKindSource }

// LogEventKindAllowed reports whether kind is within the compiled ceiling —
// i.e. whether this binary is capable of transmitting it at all. Unknown or
// empty kinds return false; this is the fail-closed half of the fence.
//
// Being within the ceiling is necessary but NOT sufficient: the runtime
// intersection in LogKindsAdmittedBy applies Fleet's narrowing on top.
func LogEventKindAllowed(kind string) bool {
	_, ok := logKindCeiling[LogEventKind(kind)]
	return ok
}

// LogEventKindClass returns the telemetry class a kind belongs to, and whether
// the kind is within the ceiling.
func LogEventKindClass(kind string) (string, bool) {
	c, ok := logKindCeiling[LogEventKind(kind)]
	return c, ok
}

// CeilingLogEventKinds returns the compiled ceiling as a sorted slice. For
// tests, diagnostics and the parity gate.
func CeilingLogEventKinds() []string {
	out := make([]string, 0, len(logKindCeiling))
	for k := range logKindCeiling {
		out = append(out, string(k))
	}
	sort.Strings(out)
	return out
}

// ── The runtime intersection ─────────────────────────────────────────────────

// LogKindsAdmittedBy computes what may actually leave the process:
//
//	compiled ceiling  ∩  Fleet's per-class opt-ins
//
// The intersection is written out rather than left emergent because the
// direction is the security property. Iteration is over the CEILING, and
// optIns is only ever consulted to remove — so an opt-in item naming a class
// or kind outside the ceiling contributes nothing. There is no code path by
// which a Fleet response grows the returned set.
//
// optIns is the snapshot from GetTelemetryOptIns (see telemetry_optins.go),
// which is already Fleet's runtime narrowing channel — this reuses it rather
// than introducing a second mechanism that would need its own hardening.
//
// A nil/empty snapshot admits nothing. "We have not been told what is allowed"
// and "nothing is allowed" are the same answer for an egress control; the
// alternative (fall back to per-class defaults) would make a dropped network
// fetch quietly permissive.
func LogKindsAdmittedBy(optIns []TelemetryOptInItem) map[LogEventKind]struct{} {
	admitted := make(map[LogEventKind]struct{}, len(logKindCeiling))
	if len(optIns) == 0 {
		return admitted
	}
	optedInClass := make(map[string]bool, len(optIns))
	for _, item := range optIns {
		// Note: only `true` entries are recorded, and only classes are read.
		// Nothing about an opt-in item can name a NEW kind.
		if item.OptedIn {
			optedInClass[item.Class] = true
		}
	}
	for kind, class := range logKindCeiling {
		if optedInClass[class] {
			admitted[kind] = struct{}{}
		}
	}
	return admitted
}

// ── Record-level admission ───────────────────────────────────────────────────

// logRecordEventKind extracts the kameas.event.kind attribute from an
// sdklog.Record. Returns ("", false) when the attribute is absent or is not a
// string — both of which mean "not exportable".
func logRecordEventKind(r sdklog.Record) (string, bool) {
	var kind string
	var found bool
	r.WalkAttributes(func(kv otellog.KeyValue) bool {
		if kv.Key != AttrEventKind {
			return true
		}
		if kv.Value.Kind() != otellog.KindString {
			return false
		}
		kind = kv.Value.AsString()
		found = true
		return false
	})
	return kind, found
}

// logRecordExportable is the single admission predicate for the fleet log
// lane: a record leaves the process only if it carries a kameas.event.kind
// that is BOTH within the compiled ceiling and present in admitted (Fleet's
// narrowing, already intersected by LogKindsAdmittedBy).
//
// Everything else — every plain slog line the harness writes, which is what
// the slog→OTel bridge feeds into this pipeline — is dropped locally, before
// export. Application log bodies are content-bearing by nature (file paths,
// error strings, prompts, identifiers); constitution §IX requires that
// telemetry crossing the machine boundary carry no content and sit inside an
// enumerated budget. The receiver discarding unknown kinds is not a substitute
// for that: by the time it discards them the bytes have already crossed the
// network and terminated TLS on Kameas infrastructure.
//
// The ceiling is re-checked here even though `admitted` was built from it.
// Belt and braces: a future caller that hands in a set from somewhere else
// still cannot get a non-ceiling kind onto the wire.
func logRecordExportable(r sdklog.Record, admitted map[LogEventKind]struct{}) bool {
	kind, ok := logRecordEventKind(r)
	if !ok {
		return false
	}
	if !LogEventKindAllowed(kind) {
		return false
	}
	_, ok = admitted[LogEventKind(kind)]
	return ok
}
