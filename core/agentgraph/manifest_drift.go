// Package agentgraph — manifest drift detection on graph load.
//
// CheckManifestDrift compares the live manifest fingerprints (baked into
// the generated manifest_versions_gen.go constants) against the
// provenance recorded when each node was authored. When the live
// fingerprint differs from the stored one a ManifestDriftWarning is
// returned for that node.
//
// The detector is deliberately read-only and pure (no DB writes, no side
// effects) so callers can decide how to surface the warnings:
//
//	for _, w := range CheckManifestDrift(g, prov) {
//	    log.Warnf("manifest drift: node %s kind %s: live fp %s, authored fp %s",
//	        w.NodeID, w.Kind, w.LiveFingerprint, w.AuthoredFingerprint)
//	}
//
// Nodes with empty AuthoredFingerprint (i.e. pre-migration nodes that
// have no provenance row) are silently skipped — per spec WP03/WP04.
//
// Mission: manifest-versioning-01NDFSEX02, WP04.
package agentgraph

// NodeProvenance carries the manifest contract recorded when a node was
// authored. Loaded from agent_graph_node_provenance via the caller.
type NodeProvenance struct {
	// GraphID identifies the parent graph.
	GraphID string
	// NodeID is the node's ID within the graph.
	NodeID string
	// Kind is the NodeKind string at author time.
	Kind string
	// ManifestVersionAtAuthor is the semver from the manifest when the
	// node was saved.
	ManifestVersionAtAuthor string
	// FingerprintAtAuthor is the sha256 hex of the behavior-affecting
	// subset of the manifest at author time.
	FingerprintAtAuthor string
}

// ManifestDriftWarning describes a single fingerprint mismatch between
// the live manifest and the manifest that was current when the node was
// saved.
type ManifestDriftWarning struct {
	// GraphID identifies the graph containing the drifted node.
	GraphID string
	// NodeID is the affected node's ID.
	NodeID string
	// Kind is the NodeKind string.
	Kind string
	// ManifestVersionAtAuthor is the semver recorded when the node was saved.
	ManifestVersionAtAuthor string
	// LiveManifestVersion is the semver in the current shipped manifest.
	LiveManifestVersion string
	// AuthoredFingerprint is the fingerprint recorded at author time.
	AuthoredFingerprint string
	// LiveFingerprint is the fingerprint computed from the current manifest.
	LiveFingerprint string
}

// ProvenanceSource is the minimal read interface over
// agent_graph_node_provenance. Callers (e.g. the graph loader) implement
// this with a SQL query; tests use the in-memory fake below.
type ProvenanceSource interface {
	// LoadProvenance returns every provenance row for the graph.
	// Empty slice (no rows) means no provenance is recorded; the
	// drift detector skips nodes with no provenance.
	LoadProvenance(graphID string) ([]NodeProvenance, error)
}

// liveManifestFingerprints returns a map from kind → live fingerprint
// baked into the generated manifest_versions_gen.go constants.
// It uses ResolvedManifests (populated at init from the shipped catalog)
// and ComputeManifestFingerprint.
func liveManifestFingerprints() map[string]string {
	out := make(map[string]string, len(ResolvedManifests))
	for kind, rm := range ResolvedManifests {
		fp, err := ComputeManifestFingerprint(rm)
		if err != nil {
			// Should never happen for shipped manifests; skip on error.
			continue
		}
		out[string(kind)] = fp
	}
	return out
}

// liveManifestVersions returns a map from kind → live manifest version
// from the generated constants.
//
// We build this lazily from the generated per-kind constants rather than
// adding a registry to manifest_versions_gen.go (which would require the
// template to emit non-constant code). The AllNodeKinds() helper gives
// us every kind; we match by kind string to the generated constant table
// via a lookup map populated at first call.
var liveManifestVersionMap map[string]string

func getLiveManifestVersionMap() map[string]string {
	if liveManifestVersionMap != nil {
		return liveManifestVersionMap
	}
	// Build from the generated constant table embedded in
	// manifest_versions_gen.go. We use allManifestVersions() which is
	// generated below.
	m := make(map[string]string, len(allGeneratedManifestVersions))
	for kind, version := range allGeneratedManifestVersions {
		m[kind] = version
	}
	liveManifestVersionMap = m
	return m
}

// CheckManifestDrift returns one ManifestDriftWarning per node in g
// whose live fingerprint differs from the fingerprint recorded at author
// time. Nodes with no provenance row (pre-migration or never-saved) are
// silently skipped.
//
// The caller provides a provenanceRows slice (loaded from
// agent_graph_node_provenance for g.ID) to avoid coupling this function
// to the database.
func CheckManifestDrift(g Graph, provenanceRows []NodeProvenance) []ManifestDriftWarning {
	if len(provenanceRows) == 0 {
		return nil
	}

	liveFPs := liveManifestFingerprints()
	liveVersions := getLiveManifestVersionMap()

	// Index provenance by node ID for O(1) lookup.
	provByNode := make(map[string]NodeProvenance, len(provenanceRows))
	for _, p := range provenanceRows {
		provByNode[p.NodeID] = p
	}

	var warnings []ManifestDriftWarning
	for _, node := range g.Nodes {
		prov, ok := provByNode[node.ID]
		if !ok {
			continue // no provenance — skip (pre-migration node)
		}
		if prov.FingerprintAtAuthor == "" {
			continue // empty fingerprint — skip (unknown provenance)
		}

		kindStr := string(node.Kind)
		liveFP, hasFP := liveFPs[kindStr]
		if !hasFP {
			continue // unknown kind — skip (user override or future kind)
		}

		if liveFP == prov.FingerprintAtAuthor {
			continue // no drift
		}

		liveVersion := liveVersions[kindStr]

		warnings = append(warnings, ManifestDriftWarning{
			GraphID:                 g.ID,
			NodeID:                  node.ID,
			Kind:                    kindStr,
			ManifestVersionAtAuthor: prov.ManifestVersionAtAuthor,
			LiveManifestVersion:     liveVersion,
			AuthoredFingerprint:     prov.FingerprintAtAuthor,
			LiveFingerprint:         liveFP,
		})
	}
	return warnings
}
