package trustanchor

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/kameas-ai/kenaz-harness/core/trust"
)

// engineAPI implements TrustAnchorAPI over a core/trust.TrustEngine.
type engineAPI struct {
	engine trust.TrustEngine
}

// New wraps engine as a TrustAnchorAPI. A nil engine degrades every
// method to an empty-list / explicit-error response rather than
// panicking — matches the rest of core/rpc/views' "nil chassis ⇒ empty
// state" convention (see core/rpc/views/bundle's fsReader nil-reader
// path for the established pattern this mirrors).
func New(engine trust.TrustEngine) TrustAnchorAPI {
	return &engineAPI{engine: engine}
}

func (a *engineAPI) ListAnchors(ctx context.Context) ([]Anchor, error) {
	if a == nil || a.engine == nil {
		return []Anchor{}, nil
	}
	anchors, err := a.engine.ListAnchors(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Anchor, 0, len(anchors))
	for _, an := range anchors {
		out = append(out, toWire(an))
	}
	return out, nil
}

func (a *engineAPI) InstallAnchor(ctx context.Context, req InstallAnchorRequest) (Anchor, error) {
	if a == nil || a.engine == nil {
		return Anchor{}, fmt.Errorf("trustanchor: no trust engine configured")
	}
	if req.AnchorID == "" {
		return Anchor{}, fmt.Errorf("trustanchor: anchor_id is required")
	}
	if req.KeyB64 == "" {
		return Anchor{}, fmt.Errorf("trustanchor: key is required")
	}
	keyBytes, err := base64.StdEncoding.DecodeString(req.KeyB64)
	if err != nil {
		return Anchor{}, fmt.Errorf("trustanchor: decode key: %w", err)
	}
	alg := trust.Algorithm(req.Algorithm)
	if alg == "" {
		alg = trust.AlgEd25519
	}
	kind := trust.AnchorRawPublicKey
	if req.Kind == "pinned_peer" {
		kind = trust.AnchorPinnedPeer
	}

	anchor := trust.Anchor{
		AnchorID:  req.AnchorID,
		Kind:      kind,
		PeerID:    req.PeerID,
		Algorithm: alg,
		PublicKey: trust.PublicKey{
			Algorithm:   alg,
			Bytes:       keyBytes,
			Fingerprint: trust.ComputeFingerprint(keyBytes),
		},
	}
	if err := a.engine.InstallAnchor(ctx, anchor); err != nil {
		return Anchor{}, err
	}
	return toWire(anchor), nil
}

func toWire(a trust.Anchor) Anchor {
	return Anchor{
		AnchorID:  a.AnchorID,
		Kind:      a.Kind.String(),
		PeerID:    a.PeerID,
		OrgID:     a.OrgID,
		Algorithm: string(a.Algorithm),
		PublicKey: PublicKey{
			Algorithm:   string(a.PublicKey.Algorithm),
			KeyB64:      base64.StdEncoding.EncodeToString(a.PublicKey.Bytes),
			Fingerprint: a.PublicKey.Fingerprint,
		},
		InstalledAt: a.InstalledAt.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		Removed:     a.Removed,
	}
}
