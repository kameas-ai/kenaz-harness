package bash

// dangerousCopyTable maps dangerous-pattern basenames to a one-line
// human-readable reason shown in the permission modal (spec §10, Q4=C).
// The copy is per-command so the user understands precisely why the
// modal carries the extra stern notice.
//
// Keys are the argv[0] basename (before subcommand appending). The modal
// displays the matching reason for the first key that matches; if no key
// matches, a generic fallback is shown by DangerousCopy.
var dangerousCopyTable = map[string]string{
	"rm":       "Deletes files permanently; irreversible if -rf is used",
	"sudo":     "Runs command as root with full system access",
	"chmod":    "Changes file permissions and can expose sensitive files",
	"chown":    "Changes file ownership and can expose sensitive files",
	"dd":       "Reads or writes raw devices; can overwrite disks or destroy filesystems",
	"mkfs":     "Formats a disk partition; all data on the target is permanently lost",
	"kill":     "Sends a signal to a process and can terminate system services",
	"killall":  "Terminates all processes with a matching name",
	"pkill":    "Terminates processes by pattern; can match system services",
	"shutdown": "Powers off or reboots the machine immediately",
	"reboot":   "Reboots the machine, interrupting all running processes",
	"mv":       "Moves or renames files; may overwrite existing targets",
	"cp":       "Copies files; can overwrite existing targets at the destination",
	// Pipe-to-shell patterns matched by argv[0] of the outer command.
	// The check in IsDangerous handles "curl|sh" and "wget|sh" via
	// the pipeline-pattern heuristics; these entries handle the cases
	// where only the first program is in argv[0].
	"wget": "Downloads files from the internet; often used in 'wget | sh' attacks",
	"curl": "Transfers data; 'curl | sh' allows arbitrary remote code execution",
}

// DangerousCopy returns the modal's per-command danger line for the
// given argv[0] basename. Falls back to a generic string when the
// basename is not in the table. Callers MUST pass the basename (no path
// component) of argv[0]; DerivePattern already extracts this.
func DangerousCopy(basename string) string {
	if msg, ok := dangerousCopyTable[basename]; ok {
		return msg
	}
	return "This command can cause irreversible system changes"
}
