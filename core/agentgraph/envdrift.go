package agentgraph

import "strings"

// tool-error-legibility-01PMDL02 WP02.
//
// Today a tool-dispatch failure is surfaced to the model as the raw
// wrapped error string (err.Error()) with no distinction between
// wrong-args, a tool crash, a policy denial, or environment drift (the
// filesystem/permissions/resource the tool expected has changed since
// the model last observed it — "file doesn't exist, you should ls
// first"). Of those four legs, environment drift is the one most
// relevant to grounding: the model can often recover on its own if told
// to re-observe before retrying, but only if it can tell that's what
// happened.
//
// environmentDriftSignatures conservatively signature-matches a small,
// well-known set of failing tool outputs and APPENDS (never rewrites) a
// short, standard diagnostic suffix. A false suffix on an unrelated
// error is worse than none, so matching stays narrow and literal.

// environmentDriftSignature pairs a lowercase substring to match against
// failing tool output with the diagnostic suffix to append when it hits.
type environmentDriftSignature struct {
	substr string
	hint   string
}

var environmentDriftSignatures = []environmentDriftSignature{
	{
		substr: "no such file or directory",
		hint:   "Environment-drift hint: the path may no longer exist. List the parent directory to confirm before retrying.",
	},
	{
		substr: "permission denied",
		hint:   "Environment-drift hint: access may have been revoked or was never granted. Request access or check permissions before retrying.",
	},
	{
		substr: "not found",
		hint:   "Environment-drift hint: the referenced resource may no longer exist. Re-list or re-query before retrying.",
	},
}

// AppendEnvironmentDriftHint scans a failing tool's error/content text for
// a well-known environment-drift signature and appends one short,
// standard diagnostic suffix when found. It never rewrites the input —
// only appends — and is a no-op (returns content unchanged) when no
// signature matches, so unrelated errors (wrong-args, tool crashes,
// policy denials) are never mislabeled. Matches the first signature that
// hits, in the fixed order above.
func AppendEnvironmentDriftHint(content string) string {
	if content == "" {
		return content
	}
	lower := strings.ToLower(content)
	for _, sig := range environmentDriftSignatures {
		if strings.Contains(lower, sig.substr) {
			return content + "\n\n" + sig.hint
		}
	}
	return content
}
