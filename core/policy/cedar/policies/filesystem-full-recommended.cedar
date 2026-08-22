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
// Cedar matches the Filesystem entity by its canonical_path in context
// (the absolute, cleaned path supplied by the gate hook). The `like`
// operator does suffix/prefix/substring matching with `*` wildcards.
//
// You SHOULD review and extend the deny list for your own environment:
// add project-specific secret directories, dotfiles you treat as
// sensitive, build-output trees you don't want overwritten, etc.
//
// =====================================================================

// --- credentials & SSH ----------------------------------------------
forbid (
    principal == User::"local",
    action in [Action::"read_filesystem", Action::"write_filesystem"],
    resource is FilesystemOp
) when {
    context.canonical_path like "*/.ssh/*" ||
    context.canonical_path like "*/.ssh"
};

forbid (
    principal == User::"local",
    action in [Action::"read_filesystem", Action::"write_filesystem"],
    resource is FilesystemOp
) when {
    context.canonical_path like "*/.aws/*" ||
    context.canonical_path like "*/.aws"
};

forbid (
    principal == User::"local",
    action in [Action::"read_filesystem", Action::"write_filesystem"],
    resource is FilesystemOp
) when {
    context.canonical_path like "*/.gnupg/*" ||
    context.canonical_path like "*/.gnupg"
};

forbid (
    principal == User::"local",
    action in [Action::"read_filesystem", Action::"write_filesystem"],
    resource is FilesystemOp
) when {
    context.canonical_path like "*/.config/gh/*" ||
    context.canonical_path like "*/.config/op/*" ||
    context.canonical_path like "*/.config/azure/*" ||
    context.canonical_path like "*/.config/gcloud/*" ||
    context.canonical_path like "*/.config/doctl/*"
};

forbid (
    principal == User::"local",
    action in [Action::"read_filesystem", Action::"write_filesystem"],
    resource is FilesystemOp
) when {
    context.canonical_path like "*/.netrc" ||
    context.canonical_path like "*/.npmrc" ||
    context.canonical_path like "*/.pypirc" ||
    context.canonical_path like "*/.cargo/credentials*" ||
    context.canonical_path like "*/.docker/config.json"
};

// --- dotenv files (any depth) ---------------------------------------
forbid (
    principal == User::"local",
    action in [Action::"read_filesystem", Action::"write_filesystem"],
    resource is FilesystemOp
) when {
    context.canonical_path like "*/.env" ||
    context.canonical_path like "*/.env.*" ||
    context.canonical_path like "*.env.local" ||
    context.canonical_path like "*.env.production*"
};

// --- macOS keychain + login items -----------------------------------
forbid (
    principal == User::"local",
    action in [Action::"read_filesystem", Action::"write_filesystem"],
    resource is FilesystemOp
) when {
    context.canonical_path like "*/Library/Keychains/*"
};

// --- system-wide deny (writes only; reads stay permitted) -----------
// The model can introspect /etc, /usr, etc. for context. It cannot
// modify them. Pair with `filesystem` recipe sandboxing if you want
// reads denied too.
forbid (
    principal == User::"local",
    action == Action::"write_filesystem",
    resource is FilesystemOp
) when {
    context.canonical_path like "/etc/*" ||
    context.canonical_path like "/usr/*" ||
    context.canonical_path like "/bin/*" ||
    context.canonical_path like "/sbin/*" ||
    context.canonical_path like "/var/log/*" ||
    context.canonical_path like "/Library/*" ||
    context.canonical_path like "/System/*" ||
    context.canonical_path like "/Applications/*"
};

// --- git internals --------------------------------------------------
// Reads of .git/config are commonly needed (the model wants to know
// the remote URL). Writes are denied — agents that need to push or
// rewrite history should call out via the bash tool, not by editing
// .git internals directly.
forbid (
    principal == User::"local",
    action == Action::"write_filesystem",
    resource is FilesystemOp
) when {
    context.canonical_path like "*/.git/*"
};

// --- harness-internal data dir --------------------------------------
// The harness manages its own state in <DataDir>; the model has no
// business there.
forbid (
    principal == User::"local",
    action in [Action::"read_filesystem", Action::"write_filesystem"],
    resource is FilesystemOp
) when {
    context.canonical_path like "*/kenaz-harness/data.db*" ||
    context.canonical_path like "*/kenaz-harness/keychain*"
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
