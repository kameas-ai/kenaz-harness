package fs

// dangerousTable is the canonical list of path prefixes (and exact
// paths) considered system-critical by the harness. Each entry carries
// a human-readable danger copy that the gate surfaces in the prompt
// modal's hazard banner and in audit records.
//
// Criteria for inclusion:
//   - SSH credentials  — ~/.ssh/ (private keys, known_hosts, config)
//   - AWS credentials  — ~/.aws/ (access keys, profiles)
//   - XDG config root  — ~/.config/ (browser secrets, keychain data,
//     credentials for many services all in one directory tree)
//   - /etc/            — system-wide config (hosts, sudoers, PAM)
//   - /usr/            — OS binaries and libraries (macOS + Linux)
//   - /System/         — macOS system volume (immutable in SIP; writes
//     via SIP bypass are catastrophic)
//   - /private/etc/    — macOS private namespace for /etc
//   - /private/var/    — macOS private namespace (boot, security)
//   - /Library/        — macOS system / per-user managed content
//   - /bin/ /sbin/ /usr/bin/ — symlinked into /private on recent macOS;
//     included as independent entries for Linux compatibility
//   - /proc/ /sys/     — Linux pseudo-filesystems; writes can affect
//     kernel parameters
//   - ~/.gnupg/        — PGP keyrings and trust databases
//   - ~/.netrc         — plaintext credentials used by curl/git
//   - ~/.gitconfig     — git identity + credential helper config
//
// Matching semantics: IsDangerousPath uses HasPrefix against each
// entry's Path field after both the input and the entry are confirmed
// clean. An exact-file match (netrc, gitconfig) is handled by
// including the file itself as the Path (no trailing slash).
var dangerousTable = []dangerousEntry{
	// SSH credentials.
	{
		Path: "~/.ssh/",
		Copy: "This path is inside ~/.ssh/. Modifying SSH credentials or " +
			"known_hosts can expose private keys or enable MITM attacks. " +
			"Allow-always is disabled for dangerous-tier paths.",
	},
	// AWS credential store.
	{
		Path: "~/.aws/",
		Copy: "This path is inside ~/.aws/. Writing here can overwrite AWS " +
			"access keys or profiles and silently redirect cloud API calls.",
	},
	// XDG config root (browser secrets, keychain, etc.).
	{
		Path: "~/.config/",
		Copy: "This path is inside ~/.config/. Many applications store " +
			"credentials, tokens, and session cookies here. Modifications " +
			"can compromise multiple services.",
	},
	// GnuPG keyring.
	{
		Path: "~/.gnupg/",
		Copy: "This path is inside ~/.gnupg/. Writing here can corrupt PGP " +
			"keys or trust databases used for package verification and " +
			"commit signing.",
	},
	// Plaintext netrc credentials.
	{
		Path: "~/.netrc",
		Copy: "~/.netrc stores plaintext credentials for network services. " +
			"Overwriting it can expose or replace passwords used by " +
			"curl, git, and other tools.",
	},
	// git global config.
	{
		Path: "~/.gitconfig",
		Copy: "~/.gitconfig contains git identity and credential-helper " +
			"configuration. Changes here affect every git operation on this " +
			"machine.",
	},
	// System-wide config on POSIX.
	{
		Path: "/etc/",
		Copy: "This path is inside /etc/. System-wide configuration files " +
			"such as /etc/hosts, /etc/sudoers, and PAM modules live here. " +
			"Unintended writes can break authentication or networking.",
	},
	// macOS private namespace for /etc.
	{
		Path: "/private/etc/",
		Copy: "This path is inside /private/etc/ (the macOS private " +
			"namespace for /etc). Same risks as /etc/.",
	},
	// macOS private/var.
	{
		Path: "/private/var/",
		Copy: "This path is inside /private/var/ (macOS). Boot state, " +
			"security databases, and system logs live here.",
	},
	// macOS system volume.
	{
		Path: "/System/",
		Copy: "This path is inside /System/. macOS SIP protects this " +
			"volume; writes via SIP bypass can brick the OS installation.",
	},
	// macOS managed library.
	{
		Path: "/Library/",
		Copy: "This path is inside /Library/. System-managed extensions, " +
			"security frameworks, and launch daemons live here.",
	},
	// POSIX-standard OS binaries.
	{
		Path: "/usr/",
		Copy: "This path is inside /usr/. OS binaries and shared libraries " +
			"live here; writing here can corrupt installed software.",
	},
	{
		Path: "/bin/",
		Copy: "This path is inside /bin/. Core POSIX utilities live here; " +
			"replacement can create persistent backdoors.",
	},
	{
		Path: "/sbin/",
		Copy: "This path is inside /sbin/. System administration utilities " +
			"live here; replacement can create persistent backdoors.",
	},
	// Linux kernel pseudo-filesystems.
	{
		Path: "/proc/",
		Copy: "This path is inside /proc/. Writing to Linux /proc entries " +
			"can change kernel parameters or crash running processes.",
	},
	{
		Path: "/sys/",
		Copy: "This path is inside /sys/. Writing to Linux /sys entries " +
			"can toggle kernel driver configuration at runtime.",
	},
}

// dangerousEntry is one row in dangerousTable.
type dangerousEntry struct {
	// Path is the prefix (or exact path for files) to match against.
	// Tilde (~) is expanded to the home directory at call time.
	Path string
	// Copy is the danger explanation displayed in the hazard banner.
	Copy string
}
