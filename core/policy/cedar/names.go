package cedar

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// validNameRe is the security-critical regex for Cedar policy filenames
// accepted by the editor UI write paths. Accepted pattern:
//
//	^[a-zA-Z0-9._-]+\.cedar$
//
// The stem is one or more alphanumeric / dot / hyphen / underscore characters
// (no slashes, no spaces, no control chars, no leading dot). The suffix must
// be exactly ".cedar". Maximum total length is 255 bytes (OS limit).
//
// This is intentionally broader than the older snippet regex in cedarpolicy
// (which only allowed lowercase + underscores) to accommodate filenames like
// "filesystem-full-recommended.cedar" and "my.policy.cedar".
var validNameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+\.cedar$`)

// embeddedDefaultName is the synthetic name for the bundled starter policy.
// It is read-only and cannot be used as a destination for save/delete/install.
const embeddedDefaultName = "default_policy.cedar"

// ValidPolicyName returns nil when name is safe to use as a Cedar policy
// editor filename (for GetPolicy, SavePolicy, DeletePolicy, InstallTemplate).
//
// Rules enforced:
//   - Non-empty.
//   - Matches ^[a-zA-Z0-9._-]+\.cedar$.
//   - Does not start with a dot (dotfile).
//   - No path separators (belt-and-suspenders: filepath.Base must equal name).
//   - No trailing space.
//   - Total length ≤ 255 bytes.
//
// The embedded default name "default_policy.cedar" is valid for read-only
// paths (GetPolicy) but the callers that call ValidPolicyName on write paths
// must additionally check IsProtectedPolicyName.
func ValidPolicyName(name string) error {
	if name == "" {
		return errors.New("cedar: policy name must not be empty")
	}
	if len(name) > 255 {
		return fmt.Errorf("cedar: policy name too long (%d bytes, max 255)", len(name))
	}
	if strings.HasSuffix(name, " ") || strings.HasSuffix(name, "\t") {
		return fmt.Errorf("cedar: policy name %q has trailing whitespace", name)
	}
	if strings.HasPrefix(name, ".") {
		return fmt.Errorf("cedar: policy name %q must not start with a dot", name)
	}
	if !validNameRe.MatchString(name) {
		return fmt.Errorf("cedar: policy name %q does not match required pattern ^[a-zA-Z0-9._-]+\\.cedar$", name)
	}
	// Belt-and-suspenders: filepath.Base must equal name so even if the regex
	// somehow admitted a slash the join would be safe.
	if filepath.Base(name) != name {
		return fmt.Errorf("cedar: policy name %q contains path separators", name)
	}
	return nil
}

// IsProtectedPolicyName returns true when name refers to an embedded default
// policy that cannot be deleted or overwritten via the editor UI.
func IsProtectedPolicyName(name string) bool {
	protected := []string{
		embeddedDefaultName,
		DefaultCredentialPolicyName,
		DefaultBashPolicyName,
		DefaultFilesystemPolicyName,
		DefaultToolPolicyName,
		DefaultWorkflowsPolicyName,
	}
	for _, p := range protected {
		if name == p {
			return true
		}
	}
	return false
}
