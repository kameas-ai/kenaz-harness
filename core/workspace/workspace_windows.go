//go:build windows

package workspace

// statDev is the Windows stand-in for the device-number probe backing
// statDevFn. It deliberately reports "unknown" rather than a real device
// number.
//
// Why the safe fallback and not GetFileInformationByHandle's volume serial
// number: the override seam this probe supports (KENAZ_HARNESS_WORKSPACE →
// isMountpoint, spec 089) is set exactly once in production, by the
// workbench leaf flake's systemd unit that runs *inside the Linux guest*
// (kenaz-workbench/nix/apps/kenaz-harness/flake.nix) — including on a
// Windows host, since the workbench substrate there is WSL2, itself Linux.
// The native Windows build of kenaz-harness is the desktop artifact
// (spec 089 non-goals: "Host-mode behaviour changes beyond the startup-
// ensure" are out of scope; the desktop harness keeps its private
// <DataDir>/agent-workspace sandbox). It never receives that override, so
// isMountpoint never runs for a real answer on Windows — only the compiler
// needs one. Per the documented invariant ("Unknown ⇒ false; the safe
// direction is the private fallback"), returning (0, false) here is exactly
// the same outcome a real-but-unimplemented probe would need to produce in
// the one Windows code path that could ever reach it. Wire up a genuine
// volume-serial-number implementation if/when a Windows code path starts
// setting KENAZ_HARNESS_WORKSPACE for real — not before.
func statDev(path string) (uint64, bool) {
	return 0, false
}
