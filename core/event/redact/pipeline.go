package redact

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Summary is the diagnostic output of a single Apply.
type Summary struct {
	MatchersFired     []string `json:"matchers_fired,omitempty"`
	FieldPathsRedacted []string `json:"field_paths_redacted,omitempty"`
	NoOp              bool     `json:"no_op,omitempty"`
	SaltFingerprint   string   `json:"salt_fingerprint,omitempty"`
}

// Pipeline is the non-bypassable redaction surface (C-004).
type Pipeline struct {
	policy   Policy
	matchers []Matcher
	fields   []fieldPath
	salt     *Secret
}

type fieldPath struct {
	raw   string
	parts []string
}

// New constructs a pipeline from policy and a resolver. The pipeline
// holds the resolved Secret for the process lifetime; rotation is
// invoked via Pipeline.RotateSalt.
func New(policy Policy, resolver Resolver) (*Pipeline, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if resolver == nil {
		return nil, fmt.Errorf("%w: nil resolver", ErrInvalidPolicy)
	}
	salt, err := resolver.Resolve(policy.SaltRef)
	if err != nil {
		return nil, err
	}
	matchers := append(DefaultMatchers(), policy.ExtraMatchers...)
	fields := make([]fieldPath, 0, len(policy.FieldPaths))
	for _, fp := range policy.FieldPaths {
		fields = append(fields, fieldPath{
			raw:   fp,
			parts: strings.Split(fp, "."),
		})
	}
	return &Pipeline{
		policy:   policy,
		matchers: matchers,
		fields:   fields,
		salt:     salt,
	}, nil
}

// SaltFingerprint returns the public fingerprint of the current salt.
// Safe to log and emit in RedactionSummary.
func (p *Pipeline) SaltFingerprint() string {
	if p == nil || p.salt == nil {
		return ""
	}
	return p.salt.Fingerprint()
}

// RotateSalt swaps the underlying salt. Old payloads keep their
// existing redacted forms; only future Apply calls use the new salt.
func (p *Pipeline) RotateSalt(newSalt []byte) {
	p.salt.Rotate(newSalt)
}

// Apply runs the pipeline against payload and returns the redacted,
// JSON-encoded byte form plus a Summary. Errors are returned for
// unmarshalable inputs or pipeline panics.
func (p *Pipeline) Apply(payload any) (out []byte, sum Summary, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v", ErrPipelinePanic, r)
		}
	}()

	if p == nil {
		return nil, Summary{}, fmt.Errorf("%w: nil pipeline", ErrPipelineDisabled)
	}

	// Round-trip through json.Marshal to normalize.
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, Summary{}, fmt.Errorf("redact: marshal: %w", err)
	}
	var anyVal any
	if err := json.Unmarshal(raw, &anyVal); err != nil {
		return nil, Summary{}, fmt.Errorf("redact: unmarshal: %w", err)
	}

	state := &applyState{
		matchers: p.matchers,
		fields:   p.fields,
		salt:     p.salt,
		fired:    make(map[string]struct{}),
		paths:    make(map[string]struct{}),
	}
	cleaned := state.walk(anyVal, "")

	encoded, err := json.Marshal(cleaned)
	if err != nil {
		return nil, Summary{}, fmt.Errorf("redact: re-marshal: %w", err)
	}

	sum.SaltFingerprint = p.salt.Fingerprint()
	if len(state.fired) == 0 && len(state.paths) == 0 {
		sum.NoOp = true
	} else {
		sum.MatchersFired = sortedKeys(state.fired)
		sum.FieldPathsRedacted = sortedKeys(state.paths)
	}
	return encoded, sum, nil
}

type applyState struct {
	matchers []Matcher
	fields   []fieldPath
	salt     *Secret
	fired    map[string]struct{}
	paths    map[string]struct{}
}

func (s *applyState) walk(v any, path string) any {
	// Field-path redaction first: if path matches an operator
	// selector, replace the entire subtree with a placeholder.
	if s.matchesField(path) {
		s.paths[path] = struct{}{}
		return s.placeholderForValue(v)
	}
	switch x := v.(type) {
	case string:
		return s.scrubString(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			child := path
			if child == "" {
				child = k
			} else {
				child = path + "." + k
			}
			out[k] = s.walk(vv, child)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = s.walk(item, path)
		}
		return out
	default:
		return v
	}
}

func (s *applyState) matchesField(path string) bool {
	if path == "" {
		return false
	}
	for _, fp := range s.fields {
		if path == fp.raw {
			return true
		}
		// Wildcard-free suffix match against the trailing component
		// of the path: e.g. policy "authorization" matches the path
		// "request.headers.authorization".
		if !strings.Contains(fp.raw, ".") {
			parts := strings.Split(path, ".")
			if parts[len(parts)-1] == fp.raw {
				return true
			}
		}
	}
	return false
}

func (s *applyState) scrubString(in string) string {
	out := in
	for _, m := range s.matchers {
		// Use ReplaceAllStringFunc so we record which matchers fired.
		matched := false
		out = m.Re.ReplaceAllStringFunc(out, func(hit string) string {
			matched = true
			if m.SubmatchIndex == 0 {
				return s.placeholderFor(hit)
			}
			// Replace only the named submatch within hit.
			groups := m.Re.FindStringSubmatchIndex(hit)
			if groups == nil || m.SubmatchIndex*2+1 >= len(groups) {
				return s.placeholderFor(hit)
			}
			start, end := groups[m.SubmatchIndex*2], groups[m.SubmatchIndex*2+1]
			if start < 0 || end < 0 {
				return s.placeholderFor(hit)
			}
			return hit[:start] + s.placeholderFor(hit[start:end]) + hit[end:]
		})
		if matched {
			s.fired[m.ID] = struct{}{}
		}
	}
	return out
}

func (s *applyState) placeholderFor(plaintext string) string {
	mac := hmac.New(sha256.New, s.salt.Bytes())
	mac.Write([]byte(plaintext))
	sum := mac.Sum(nil)
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:10])
	return "redacted:" + strings.ToLower(enc)
}

// placeholderForValue produces a deterministic string placeholder for
// an entire field subtree. We marshal the subtree and hash it so the
// same input produces the same placeholder.
func (s *applyState) placeholderForValue(v any) string {
	raw, _ := json.Marshal(v)
	return s.placeholderFor(string(raw))
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
