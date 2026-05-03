package recipes

import (
	"log"
	"regexp"
	"strings"
)

// tokenPattern matches ${IDENT} where IDENT is upper/lower alphanum
// plus underscore, starting with a letter or underscore. The recipe
// schema only uses uppercase tokens (${DATA_DIR}, ${ALLOWED_DIRS}),
// but the regex tolerates either case so that a typo in shipped.json
// still gets flagged via the unknown-var warn path rather than
// silently leaking the literal string.
var tokenPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Substitute walks template and replaces ${VAR} tokens with values
// drawn from vars (scalar) and listVars (multi-arg expansion).
//
// Two flavours of expansion exist because the filesystem recipe needs
// each allowed directory to land as a separate argv element (the
// reference server takes positional paths) rather than as one
// space-joined arg:
//
//   - Scalar ${VAR}: inline string replacement; multiple tokens may
//     appear in a single template arg. Drawn from vars.
//   - List ${VAR}: when an entire template arg is exactly "${VAR}"
//     and VAR is in listVars, that one arg expands to len(listVars[VAR])
//     output args, one per element. listVars takes precedence over
//     vars when both define the same name (so a recipe that registers
//     ALLOWED_DIRS in both wires gets the list semantics).
//
// Unknown vars are left as-is (the literal ${X} survives) and a warn
// is logged so an operator inspecting logs can spot the typo. An empty
// list var contributes zero args. A nil vars / listVars map is
// treated as empty.
//
// The template slice is not modified; the returned slice is fresh.
func Substitute(template []string, vars map[string]string, listVars map[string][]string) []string {
	if len(template) == 0 {
		return nil
	}
	out := make([]string, 0, len(template))
	for _, arg := range template {
		// List-substitute: the entire arg is exactly ${VAR} and VAR
		// resolves to a slice. Append N args, one per element.
		if name, ok := wholeToken(arg); ok {
			if list, present := listVars[name]; present {
				out = append(out, list...)
				continue
			}
		}
		// Scalar-substitute: inline replace every ${VAR} in arg.
		replaced := tokenPattern.ReplaceAllStringFunc(arg, func(match string) string {
			name := match[2 : len(match)-1]
			if v, present := vars[name]; present {
				return v
			}
			// Fall back to listVars for the scalar path — a list with
			// one element is a perfectly reasonable scalar value, and
			// space-joining a multi-element list into a single arg is
			// the documented escape hatch when a recipe really wants
			// the joined form. We keep the explicit list-as-multi-arg
			// path above as the preferred shape.
			if vs, present := listVars[name]; present {
				return strings.Join(vs, " ")
			}
			log.Printf("recipes: warn: unknown substitution token %s — leaving literal in argv", match)
			return match
		})
		out = append(out, replaced)
	}
	return out
}

// SubstituteHeaders applies the same ${VAR} expansion Substitute
// performs on argv to a header map. Header values are scalar-only
// (the listVars multi-arg expansion has no analogue in HTTP headers
// — there is no concept of "this header expands into N headers"),
// so callers pass only the scalar vars map.
//
// Returns a fresh map; the input is not mutated. A nil headers
// argument returns nil. Header keys are passed through unchanged so
// the recipe author controls the canonical-casing on the wire.
//
// Unknown tokens follow the Substitute contract: literal ${X}
// survives in the output and a warn is logged.
func SubstituteHeaders(headers map[string]string, vars map[string]string) map[string]string {
	if headers == nil {
		return nil
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		out[k] = substituteString(v, vars)
	}
	return out
}

// SubstituteString is the public entry point for scalar-only token
// expansion against a single string. Used by transports that
// substitute into URLs (HTTP / SSE recipes whose URL embeds an
// env-sourced subdomain or path segment).
func SubstituteString(s string, vars map[string]string) string {
	return substituteString(s, vars)
}

// substituteString is the scalar-substitution core shared by
// SubstituteHeaders and SubstituteString. Unknown tokens stay
// literal and emit a warn — same contract as Substitute's scalar
// branch.
func substituteString(s string, vars map[string]string) string {
	if s == "" {
		return s
	}
	return tokenPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := match[2 : len(match)-1]
		if v, ok := vars[name]; ok {
			return v
		}
		log.Printf("recipes: warn: unknown substitution token %s — leaving literal", match)
		return match
	})
}

// wholeToken reports whether arg is exactly "${IDENT}" with no other
// characters around it, returning the bare IDENT on a match.
func wholeToken(arg string) (string, bool) {
	if len(arg) < 4 || arg[0] != '$' || arg[1] != '{' || arg[len(arg)-1] != '}' {
		return "", false
	}
	inner := arg[2 : len(arg)-1]
	// Re-validate the inner against the regex's IDENT shape so a
	// pathological input like "${a}b}" (which starts with ${ and ends
	// with }) doesn't slip through.
	if !tokenPattern.MatchString(arg) {
		return "", false
	}
	if tokenPattern.FindString(arg) != arg {
		return "", false
	}
	return inner, true
}
