package llm

// dogfood 2026-10-08 round 2 (P3): the builtin-enabled predicate logged
// one "rpc.builtins.predicate" line per tool per read — 54 lines a turn.
// Discovery now writes ONE summary line naming the enabled builtins.

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

type namedBuiltin string

func (n namedBuiltin) Name() string                 { return string(n) }
func (n namedBuiltin) Description() string          { return "d" }
func (n namedBuiltin) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (n namedBuiltin) Call(context.Context, json.RawMessage) (json.RawMessage, error) {
	return nil, nil
}

func TestMCPToolDiscoverer_BuiltinsLogOneSummaryLine(t *testing.T) { //nolint:paralleltest // swaps the process logger
	var buf bytes.Buffer
	prev := logging.L().Handler()
	logging.Replace(slog.NewJSONHandler(&buf, nil))
	t.Cleanup(func() { logging.Replace(prev) })

	reg := toolloop.NewBuiltinRegistry()
	for _, n := range []string{"kenaz__a", "kenaz__b", "kenaz__c"} {
		reg.Register(namedBuiltin(n))
	}
	filter := toolloop.NewEnabledFilter(reg, func(name string) bool { return name != "kenaz__b" })
	d := NewMCPToolDiscovererWithBuiltins(nil, nil, filter)
	if _, err := d.Tools(context.Background(), "sess-log"); err != nil {
		t.Fatalf("Tools: %v", err)
	}

	var summaries []string
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if strings.Contains(line, `"session_id":"sess-log"`) && strings.Contains(line, `"msg":"llm.builtins.enabled"`) {
			summaries = append(summaries, line)
		}
	}
	if len(summaries) != 1 {
		t.Fatalf("summary lines = %d, want exactly 1; log:\n%s", len(summaries), buf.String())
	}
	if !strings.Contains(summaries[0], `"enabled":2`) || !strings.Contains(summaries[0], "kenaz__a") || strings.Contains(summaries[0], "kenaz__b") {
		t.Errorf("summary = %s, want the 2 enabled builtins named and the disabled one absent", summaries[0])
	}
}
