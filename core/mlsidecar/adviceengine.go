package mlsidecar

import (
	"context"
	"errors"
	"fmt"

	"github.com/kameas-ai/kenaz-harness/core/advice"
)

// AdviceEngine adapts the loopback Client onto advice.SidecarEngine — the
// port advice.SidecarAdvisor drives (laya-advisors-01LAYA001 WP15). It
// lives here, not in core/advice, so the lighter advice package never
// imports the lifecycle manager's dependency tree (the same direction
// SidecarProbe already keeps: advice -> interface, mlsidecar -> advice).
//
// It is a pure shape-mapper: no retries, no caching, no health logic —
// every one of those decisions belongs to SidecarAdvisor (per-call,
// never sticky).
type AdviceEngine struct {
	Client *Client
}

var _ advice.SidecarEngine = AdviceEngine{}

// Contracts implements advice.SidecarEngine over GET /v1/contracts.
func (e AdviceEngine) Contracts(ctx context.Context) (map[string]advice.EngineKindContract, error) {
	if e.Client == nil {
		return nil, fmt.Errorf("mlsidecar: advice engine has no client")
	}
	payload, err := e.Client.Contracts(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]advice.EngineKindContract, len(payload.Kinds))
	for id, k := range payload.Kinds {
		out[id] = advice.EngineKindContract{ContractVersion: k.Version, Available: k.Available}
	}
	return out, nil
}

// Recommend implements advice.SidecarEngine over POST
// /v1/recommend/{kind}. The engine's typed "kind not served" refusal
// (Amendment A3.2) is translated to advice.ErrEngineKindNotServed so
// SidecarAdvisor can tell it from a fault without importing this package.
func (e AdviceEngine) Recommend(ctx context.Context, req advice.EngineRequest) (advice.EngineResponse, error) {
	if e.Client == nil {
		return advice.EngineResponse{}, fmt.Errorf("mlsidecar: advice engine has no client")
	}
	resp, err := e.Client.Recommend(ctx, req.KindID, RecommendRequest{
		Features:               req.Features,
		FeatureContractVersion: req.FeatureContractVersion,
		SessionID:              req.SessionID,
		KindID:                 req.KindID,
	})
	if err != nil {
		if errors.Is(err, ErrKindNotServed) {
			return advice.EngineResponse{}, fmt.Errorf("%w: %v", advice.ErrEngineKindNotServed, err)
		}
		return advice.EngineResponse{}, err
	}
	return advice.EngineResponse{
		Decision:      resp.Decision,
		Confidence:    resp.Confidence,
		KindID:        resp.KindID,
		Model:         resp.Model,
		Rung:          resp.Rung,
		Unbenchmarked: resp.Unbenchmarked,
	}, nil
}
