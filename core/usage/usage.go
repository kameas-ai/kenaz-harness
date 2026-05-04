// Package usage implements per-session token + cost aggregation for the
// token-cost-telemetry-01KQ8TD7 mission.
//
// Architecture: UsageTurn rows are written to session_messages (via the
// four nullable columns added in migration 0314) on each assistant turn
// completion. GetSession reads those columns back via SQL aggregation.
// The Manager is wired into the chat runner's driveRun finish path.
package usage

import (
	"context"
	"fmt"
	"os"

	"github.com/sigil-tech/kaneaz-harness/core/storage"
)

// envCostTelemetry is the opt-out env var. Default "on".
// Setting HARNESS_COST_TELEMETRY=off installs the NoopManager so no
// writes occur (migration 0314 still ran; columns exist but stay NULL).
const envCostTelemetry = "HARNESS_COST_TELEMETRY"

// UsageTurn is the per-turn accounting snapshot passed to Manager.Add.
type UsageTurn struct {
	// SessionID identifies the session this turn belongs to.
	SessionID string
	// MessageID is the session_messages.id of the assistant row that
	// was just written. When empty, the manager skips the write.
	MessageID string
	// ProviderKind is the provider kind string ("anthropic", "openrouter", …).
	ProviderKind string
	// ModelID is the model id string (e.g. "claude-sonnet-4-5").
	ModelID string
	// PromptTokens is the provider-reported input token count.
	PromptTokens int
	// CompletionTokens is the provider-reported output/completion token count.
	CompletionTokens int
	// CostUSD is the derived-or-provider-reported cost in USD. Nil means
	// the cost is unknown (no pricing entry + no provider cost).
	CostUSD *float64
	// CostSource is one of "provider", "derived", "unknown".
	CostSource string
}

// Aggregate is the per-session cumulative total returned by GetSession.
type Aggregate struct {
	// PromptTokens is the sum of all prompt_tokens rows for the session.
	PromptTokens int
	// CompletionTokens is the sum of all completion_tokens rows.
	CompletionTokens int
	// TotalTokens is PromptTokens + CompletionTokens.
	TotalTokens int
	// CostUSD is the sum of all non-NULL cost_usd rows (0.0 when no rows
	// have cost data yet).
	CostUSD float64
	// CostSource summarises the cost_source values across all turns:
	//   "provider"  — every turn with a cost had source=provider
	//   "derived"   — every turn with a cost had source=derived
	//   "mixed"     — some turns have source=provider, others derived
	//   "unknown"   — no turns have any cost data
	CostSource string
	// MessageCount is the number of assistant turns that contributed usage.
	MessageCount int
}

// Manager is the per-process token + cost aggregation surface.
type Manager interface {
	// Add persists usage for one assistant turn. MessageID must be the
	// session_messages.id of the assistant row that was just written.
	// Returns nil when the write succeeded or when telemetry is disabled.
	Add(ctx context.Context, turn UsageTurn) error
	// GetSession returns the cumulative aggregate for a session.
	GetSession(ctx context.Context, sessionID string) (Aggregate, error)
}

// sqlManager is the production Manager backed by session_messages SQL columns.
type sqlManager struct {
	db storage.DB
}

// New returns a Manager that writes to the session_messages table columns
// added by migration 0314. When HARNESS_COST_TELEMETRY=off is set, or
// when db is nil, a no-op manager is returned instead.
func New(db storage.DB) Manager {
	if os.Getenv(envCostTelemetry) == "off" {
		return &noopManager{}
	}
	if db == nil {
		return &noopManager{}
	}
	return &sqlManager{db: db}
}

// Add writes the four usage columns onto the session_messages row
// identified by turn.MessageID. It is a no-op when MessageID is empty.
func (m *sqlManager) Add(ctx context.Context, turn UsageTurn) error {
	if turn.MessageID == "" {
		return nil
	}
	return m.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		var costArg any
		if turn.CostUSD != nil {
			costArg = *turn.CostUSD
		}
		var sourceArg any
		if turn.CostSource != "" {
			sourceArg = turn.CostSource
		}
		_, err := tx.Exec(ctx,
			`UPDATE session_messages
			 SET prompt_tokens = ?, completion_tokens = ?, cost_usd = ?, cost_source = ?
			 WHERE id = ?`,
			turn.PromptTokens,
			turn.CompletionTokens,
			costArg,
			sourceArg,
			turn.MessageID,
		)
		return err
	})
}

// GetSession returns the aggregated usage across all assistant turns for
// sessionID. Rows with NULL cost_usd contribute 0 to the cost sum; the
// CostSource field summarises whether any/all are from the provider.
func (m *sqlManager) GetSession(ctx context.Context, sessionID string) (Aggregate, error) {
	row := m.db.Reader().QueryRow(ctx,
		`SELECT
		   COALESCE(SUM(prompt_tokens), 0),
		   COALESCE(SUM(completion_tokens), 0),
		   COALESCE(SUM(COALESCE(cost_usd, 0)), 0.0),
		   COUNT(CASE WHEN prompt_tokens IS NOT NULL THEN 1 END),
		   COUNT(CASE WHEN cost_source = 'provider' THEN 1 END),
		   COUNT(CASE WHEN cost_source = 'derived'  THEN 1 END)
		 FROM session_messages
		 WHERE session_id = ? AND role = 'assistant'`,
		sessionID,
	)
	var (
		prompt, completion                   int
		costTotal                            float64
		msgCount, providerCount, derivedCount int
	)
	if err := row.Scan(&prompt, &completion, &costTotal, &msgCount, &providerCount, &derivedCount); err != nil {
		return Aggregate{}, fmt.Errorf("usage: GetSession: %w", err)
	}
	src := deriveAggSource(providerCount, derivedCount, msgCount)
	return Aggregate{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
		CostUSD:          costTotal,
		CostSource:       src,
		MessageCount:     msgCount,
	}, nil
}

// deriveAggSource returns the aggregate cost source label from per-turn counts.
func deriveAggSource(providerCount, derivedCount, msgCount int) string {
	hasCost := providerCount + derivedCount
	if hasCost == 0 {
		return "unknown"
	}
	if providerCount > 0 && derivedCount == 0 {
		return "provider"
	}
	if derivedCount > 0 && providerCount == 0 {
		return "derived"
	}
	return "mixed"
}

// noopManager silently discards all writes. Used when telemetry is off.
type noopManager struct{}

func (n *noopManager) Add(_ context.Context, _ UsageTurn) error { return nil }
func (n *noopManager) GetSession(_ context.Context, _ string) (Aggregate, error) {
	return Aggregate{CostSource: "unknown"}, nil
}
