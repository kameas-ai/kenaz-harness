// Package snapshot defines the byte-stable [ResolutionSnapshot] consumed
// by the injector and replayed against the event log. The full snapshot
// store (storage-foundations tables) lands in WP06; this package
// contains the in-memory shape and the deterministic content-hash that
// keys the store.
package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	pack "github.com/sigil-tech/kaneaz-harness/core/context/pack"
	"github.com/sigil-tech/kaneaz-harness/core/context/merge"
	"github.com/sigil-tech/kaneaz-harness/core/context/verify"
)

// Mode mirrors plan §3 ResolutionMode.
type Mode string

const (
	ModeFresh     Mode = "fresh"
	ModeCacheOnly Mode = "cache-only"
	ModeStaleWarn Mode = "stale-warn"
)

// LayerActivation reuses merge.LayerActivation enriched with provenance.
type LayerActivation struct {
	merge.LayerActivation
	Provenance *verify.ProvenanceRecord `json:"provenance,omitempty"`
}

// Snapshot is the public surface (plan §3 ResolutionSnapshot).
type Snapshot struct {
	ID          string                  `json:"id"`
	Mode        Mode                    `json:"mode"`
	GeneratedAt time.Time               `json:"generated_at"`
	Workflow    string                  `json:"workflow,omitempty"`
	AgentID     string                  `json:"agent_id,omitempty"`
	Entries     []merge.ResolvedEntry   `json:"entries"`
	Layers      []LayerActivation       `json:"layers"`
	Overrides   []merge.OverrideRecord  `json:"overrides,omitempty"`
	Warnings    []pack.Warning          `json:"warnings,omitempty"`
}

// Build assembles a [Snapshot] from a merge result and per-layer
// provenance records. The snapshot ID is the SHA-256 of the canonical
// JSON encoding of the snapshot's identity-bearing fields, which is what
// SC-005 byte-identity replays against.
func Build(r *merge.Result, prov []verify.ProvenanceRecord, mode Mode, workflow, agent string, now time.Time) (*Snapshot, error) {
	if r == nil {
		return nil, fmt.Errorf("snapshot: nil merge result")
	}
	provByPack := map[string]verify.ProvenanceRecord{}
	for _, p := range prov {
		provByPack[p.Pack.String()] = p
	}
	layers := make([]LayerActivation, 0, len(r.Layers))
	for _, l := range r.Layers {
		la := LayerActivation{LayerActivation: l}
		if rec, ok := provByPack[l.Pack.String()]; ok {
			cp := rec
			la.Provenance = &cp
		}
		layers = append(layers, la)
	}
	s := &Snapshot{
		Mode:        mode,
		GeneratedAt: now,
		Workflow:    workflow,
		AgentID:     agent,
		Entries:     r.Entries,
		Layers:      layers,
		Overrides:   r.Overrides,
		Warnings:    r.Warnings,
	}
	id, err := canonicalHash(s)
	if err != nil {
		return nil, err
	}
	s.ID = id
	return s, nil
}

// canonicalHash hashes the snapshot's content-bearing fields. The
// generated_at and id fields are excluded so two snapshots produced
// minutes apart over identical inputs share an ID — this is the SC-005
// invariant.
func canonicalHash(s *Snapshot) (string, error) {
	view := struct {
		Mode      Mode                   `json:"mode"`
		Workflow  string                 `json:"workflow,omitempty"`
		AgentID   string                 `json:"agent_id,omitempty"`
		Entries   []merge.ResolvedEntry  `json:"entries"`
		Layers    []LayerActivation      `json:"layers"`
		Overrides []merge.OverrideRecord `json:"overrides,omitempty"`
		Warnings  []pack.Warning         `json:"warnings,omitempty"`
	}{
		Mode:      s.Mode,
		Workflow:  s.Workflow,
		AgentID:   s.AgentID,
		Entries:   s.Entries,
		Layers:    s.Layers,
		Overrides: s.Overrides,
		Warnings:  s.Warnings,
	}
	raw, err := json.Marshal(view)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
