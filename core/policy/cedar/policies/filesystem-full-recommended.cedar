// filesystem-full-recommended.cedar
// =====================================================================
// RECOMMENDED hardening policy when the `filesystem-full` MCP recipe is
// installed. NOT applied by default — copy this file into your DataDir
// at <DataDir>/policy/filesystem-full-recommended.cedar to activate.
//
// Stance: deny reads + writes against high-sensitivity paths even when
// the unrestricted filesystem recipe is enabled. The model can still
// see most of your home directory; it CANNOT touch your secrets,
// credentials, or .git internals through this policy.
//
// Cedar matches the Filesystem entity by its EUID (the canonical path
// supplied by the gate hook). The `like` operator does suffix/prefix/
// substring matching with `*` wildcards.
//
// You SHOULD review and extend the deny list for your own environment:
// add project-specific secret directories, dotfiles you treat as
// sensitive, build-output trees you don't want overwritten, etc.
//
// =====================================================================

// --- credentials & SSH ----------------------------------------------
forbid (
    principal == User::"local",
    action in [Action::"file_read", Action::"file_write"],
    resource is Filesystem
) when {
    resource like Filesystem::"*/.ssh/*" ||
    resource == Filesystem::"*/.ssh"
};

forbid (
    principal == User::"local",
    action in [Action::"file_read", Action::"file_write"],
    resource is Filesystem
) when {
    resource like Filesystem::"*/.aws/*" ||
    resource == Filesystem::"*/.aws"
};

forbid (
    principal == User::"local",
    action in [Action::"file_read", Action::"file_write"],
    resource is Filesystem
) when {
    resource like Filesystem::"*/.gnupg/*" ||
    resource == Filesystem::"*/.gnupg"
};

forbid (
    principal == User::"local",
    action in [Action::"file_read", Action::"file_write"],
    resource is Filesystem
) when {
    resource like Filesystem::"*/.config/gh/*" ||
    resource like Filesystem::"*/.config/op/*" ||
    resource like Filesystem::"*/.config/azure/*" ||
    resource like Filesystem::"*/.config/gcloud/*" ||
    resource like Filesystem::"*/.config/doctl/*"
};

forbid (
    principal == User::"local",
    action in [Action::"file_read", Action::"file_write"],
    resource is Filesystem
) when {
    resource like Filesystem::"*/.netrc" ||
    resource like Filesystem::"*/.npmrc" ||
    resource like Filesystem::"*/.pypirc" ||
    resource like Filesystem::"*/.cargo/credentials*" ||
    resource like Filesystem::"*/.docker/config.json"
};

// --- dotenv files (any depth) ---------------------------------------
forbid (
    principal == User::"local",
    action in [Action::"file_read", Action::"file_write"],
    resource is Filesystem
) when {
    resource like Filesystem::"*/.env" ||
    resource like Filesystem::"*/.env.*" ||
    resource like Filesystem::"*.env.local" ||
    resource like Filesystem::"*.env.production*"
};

// --- macOS keychain + login items -----------------------------------
forbid (
    principal == User::"local",
    action in [Action::"file_read", Action::"file_write"],
    resource is Filesystem
) when {
    resource like Filesystem::"*/Library/Keychains/*"
};

// --- system-wide deny (writes only; reads stay permitted) -----------
// The model can introspect /etc, /usr, etc. for context. It cannot
// modify them. Pair with `filesystem` recipe sandboxing if you want
// reads denied too.
forbid (
    principal == User::"local",
    action == Action::"file_write",
    resource is Filesystem
) when {
    resource like Filesystem::"/etc/*" ||
    resource like Filesystem::"/usr/*" ||
    resource like Filesystem::"/bin/*" ||
    resource like Filesystem::"/sbin/*" ||
    resource like Filesystem::"/var/log/*" ||
    resource like Filesystem::"/Library/*" ||
    resource like Filesystem::"/System/*" ||
    resource like Filesystem::"/Applications/*"
};

// --- git internals --------------------------------------------------
// Reads of .git/config are commonly needed (the model wants to know
// the remote URL). Writes are denied — agents that need to push or
// rewrite history should call out via the bash tool, not by editing
// .git internals directly.
forbid (
    principal == User::"local",
    action == Action::"file_write",
    resource is Filesystem
) when {
    resource like Filesystem::"*/.git/*"
};

// --- harness-internal data dir --------------------------------------
// The harness manages its own state in <DataDir>; the model has no
// business there.
forbid (
    principal == User::"local",
    action in [Action::"file_read", Action::"file_write"],
    resource is Filesystem
) when {
    resource like Filesystem::"*/kenaz-harness/data.db*" ||
    resource like Filesystem::"*/kenaz-harness/keychain*"
};

// --- end of recommended forbids -------------------------------------
//
// Add your own rules below. Common additions:
//   - Project-specific secret directories
//   - Production build artifacts
//   - Backup directories you don't want overwritten
//
// Cedar evaluates forbid rules with priority over permits, so anything
// added here will override the default-allow stance from default_policy.cedar.
