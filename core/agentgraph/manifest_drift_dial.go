// Package agentgraph — manifest_drift dial helpers.
//
// ManifestDriftMode is a user-configurable dial that controls how the
// runtime responds to manifest drift warnings (nodes authored against a
// stale manifest fingerprint).
//
// Acceptable values:
//
//	"warn"  — emit warning and continue running (default).
//	"block" — refuse to start the run; return ErrManifestDrift.
//
// The dial has NO store and NO producer today. It previously lived on
// core/agentgraph/dials.DialConfig.ManifestDriftMode, but that cascade
// never carried the field (applyLayer had no branch for it and
// EffectiveDials had no such field), and the whole dials subsystem was
// deleted on 2026-08-14 as rival infrastructure to core/autonomy. Until
// the owner mission gives the mode a home — a settings field or a graph
// attr — EnforceManifestDriftPolicy only ever sees the zero value.
//
// NOTE: The frontend drift badge is stubbed for WP06. The badge will
// surface ManifestDriftWarnings in the graph-editor sidebar; the wiring
// is left as a TODO until the typed-port-codegen mission settles the
// shared frontend surface. The backend dial and ErrManifestDrift are
// fully functional.
//
// Mission: manifest-versioning-01NDFSEX02, WP06.
package agentgraph

import (
	"errors"
	"fmt"
)

// ManifestDriftModeWarn is the default mode: emit warnings and continue.
const ManifestDriftModeWarn = "warn"

// ManifestDriftModeBlock causes the kernel to refuse to run a graph
// that has any nodes with a drifted fingerprint.
const ManifestDriftModeBlock = "block"

// ErrManifestDrift is returned by EnforceManifestDriftPolicy when mode
// is "block" and the graph contains at least one drifted node.
var ErrManifestDrift = errors.New("agentgraph: manifest drift detected (mode=block)")

// EnforceManifestDriftPolicy evaluates warnings against the configured
// mode. If mode is "" or "warn", it is a no-op. If mode is "block" and
// warnings is non-empty, it returns ErrManifestDrift wrapping a
// human-readable summary.
//
// Callers should invoke this after CheckManifestDrift at graph-load
// time.
func EnforceManifestDriftPolicy(mode string, warnings []ManifestDriftWarning) error {
	if mode == "" || mode == ManifestDriftModeWarn {
		return nil
	}
	if mode != ManifestDriftModeBlock {
		return fmt.Errorf("agentgraph: unknown ManifestDriftMode %q (want %q or %q)",
			mode, ManifestDriftModeWarn, ManifestDriftModeBlock)
	}
	if len(warnings) == 0 {
		return nil
	}
	// Summarise the first N drifted nodes (cap at 5 to keep errors scannable).
	const maxSummary = 5
	n := len(warnings)
	shown := warnings
	if n > maxSummary {
		shown = warnings[:maxSummary]
	}
	var detail string
	for _, w := range shown {
		detail += fmt.Sprintf("\n  node %s (kind %s): authored=%s live=%s",
			w.NodeID, w.Kind, w.ManifestVersionAtAuthor, w.LiveManifestVersion)
	}
	if n > maxSummary {
		detail += fmt.Sprintf("\n  … and %d more", n-maxSummary)
	}
	return fmt.Errorf("%w: graph %s has %d drifted node(s):%s",
		ErrManifestDrift, warnings[0].GraphID, n, detail)
}
