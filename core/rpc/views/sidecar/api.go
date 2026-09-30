// Package sidecar is the view-scoped RPC surface for the local ML engine
// ("Kameas ML engine", the kenaz-ml sidecar) install + status + uninstall
// + update flow — laya-advisors-01LAYA001 WP13, spec §2c. It is a thin,
// honest adapter over core/mlsidecar.Manager: every state the Manager can
// be in is surfaced as-is, plus ONE view-only state (StateInstalledIdle)
// for the normal "installed, engine stopped, starts on demand" condition,
// which the Manager's state enum deliberately has no name for.
package sidecar

import "context"

// View-only state. Every other value of StatusView.State is an
// mlsidecar.State string verbatim (not_installed, installing, healthy,
// installed_unhealthy, unverified, contract_unsupported,
// legacy_unverified).
const StateInstalledIdle = "installed_idle"

// ReleaseView is the pre-download disclosure: what would be installed.
type ReleaseView struct {
	// Version is the pinned engine version this harness build installs.
	Version string `json:"version"`
	// SizeMB is the download size in whole megabytes (0 = unknown).
	SizeMB int `json:"sizeMB"`
}

// StatusView is the Settings panel's whole picture in one call.
type StatusView struct {
	// State is an mlsidecar.State string or StateInstalledIdle.
	State string `json:"state"`
	// Reason refines installed_unhealthy/unverified (crash, port_conflict,
	// digest_mismatch, update_pending, legacy_engine, ...); empty when none.
	Reason string `json:"reason"`
	// Detail is the Manager's own one-line explanation (install phase,
	// refusal text, last failure). Shown verbatim under the status line.
	Detail string `json:"detail"`
	// EngineVersion is the running engine's reported version, when known.
	EngineVersion string `json:"engineVersion"`

	// Installed reports a positively-verified install record exists.
	Installed bool `json:"installed"`
	// InstalledVersion is that record's version.
	InstalledVersion string `json:"installedVersion"`

	// Supported is false on platforms the engine is not built for;
	// UnavailableReason then (or when no release is published yet) says
	// why the install action is not offered. Available is the single
	// "may the Enable button be offered" bit.
	Supported         bool   `json:"supported"`
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailableReason"`

	// Release is what Enable would download (zero value when unavailable).
	Release ReleaseView `json:"release"`
	// InstallLocation is where the engine, its models and its config live
	// on this device — disclosed BEFORE the download, removed by
	// Uninstall.
	InstallLocation string `json:"installLocation"`

	// UpdateAvailable is true when the harness pins a newer engine than
	// the installed one. Updating is always a user action — never silent
	// (a model change alters recommendation behavior).
	UpdateAvailable bool `json:"updateAvailable"`
}

// SidecarAPI is the view-scoped RPC surface.
type SidecarAPI interface {
	// Status observes (read-only — never spawns) and returns the full
	// picture.
	Status(ctx context.Context) (StatusView, error)
	// Enable downloads, verifies, installs and starts the engine. Blocks
	// for the duration; the panel polls Status for the install phase.
	Enable(ctx context.Context) (StatusView, error)
	// Update installs the newer pinned engine (flip-and-respawn).
	Update(ctx context.Context) (StatusView, error)
	// Repair re-attempts to start an installed engine (Reconcile).
	Repair(ctx context.Context) (StatusView, error)
	// Uninstall stops the engine and removes it, its models and its
	// config from this device.
	Uninstall(ctx context.Context) (StatusView, error)
}
