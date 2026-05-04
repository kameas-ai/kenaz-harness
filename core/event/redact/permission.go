package redact

import "errors"

// ErrArgvPresent is returned by lintPermissionPayload when the audit
// payload contains the "argv" key. Bash-family audit events must record
// only the derived pattern (FR-014), never the raw command-line.
var ErrArgvPresent = errors.New("redact: permission audit payload must not contain 'argv' (use pattern field instead)")

// allowedPermissionFields is the set of payload keys that are
// permitted in a permission audit event. Only "argv" is hard-rejected
// for now (see WP07 scope note); unknown keys are tolerated so that
// future WPs can extend the payload without breaking this lint.
//
// The set is checked only to drive the hard-reject logic; the actual
// pass-through of unknown keys is intentional (forward-compat).
var _ = map[string]struct{}{
	"session_id":     {},
	"pattern":        {},
	"path":           {},
	"tool_name":      {},
	"server_name":    {},
	"purpose":        {},
	"decision":       {},
	"policy_id":      {},
	"prompt_id":      {},
	"dangerous_tier": {},
	"scope":          {},
}

// lintPermissionPayload checks that a permission audit payload does not
// contain the "argv" key at any level of nesting. The raw command-line
// must be converted to a pattern (e.g. "git status") before recording;
// storing argv verbatim would leak sensitive arguments into the audit
// log.
//
// Returns ErrArgvPresent if "argv" is found, nil otherwise.
// The check recurses into nested map[string]any values.
func lintPermissionPayload(payload map[string]any) error {
	return lintPayloadRecurse(payload)
}

func lintPayloadRecurse(m map[string]any) error {
	for k, v := range m {
		if k == "argv" {
			return ErrArgvPresent
		}
		if nested, ok := v.(map[string]any); ok {
			if err := lintPayloadRecurse(nested); err != nil {
				return err
			}
		}
	}
	return nil
}
