package fleet

import "testing"

// TestSecretShapeReason_CleanPayloadPasses is the mutation-proof
// counterpart to every rejection test below: a rejector that refuses
// everything is not a filter. Ordinary, legitimate sync payloads for the
// five live kinds must pass clean.
func TestSecretShapeReason_CleanPayloadPasses(t *testing.T) {
	cases := []string{
		`{"theme":"dark","accent":"blue"}`,
		`{"compactionAggressiveness":"balanced","maxAgentTurns":40}`,
		`{"id":"slack","displayName":"Slack","transport":"stdio"}`,
		`{"items":[{"name":"my-recipe","envKeys":["FOO","BAR"]}]}`,
		`{}`,
		``,
	}
	for _, payload := range cases {
		if reason := SecretShapeReason([]byte(payload)); reason != "" {
			t.Errorf("SecretShapeReason(%q) = %q, want clean (\"\")", payload, reason)
		}
	}
}

// TestSecretShapeReason_DetectsSecretReference proves an @secret:
// reference — a *reference*, not a plaintext value — is still refused:
// it is meaningless (or dangerous) on a receiving device with a
// different credstore.
func TestSecretShapeReason_DetectsSecretReference(t *testing.T) {
	payload := []byte(`{"command":"echo @secret:user:github-token"}`)
	reason := SecretShapeReason(payload)
	if reason == "" {
		t.Fatal("expected a non-empty reason for an @secret: reference payload")
	}
}

// TestSecretShapeReason_DetectsAPIKeyLiteral covers both prefixes the
// static check-no-cred-bytes-in-rpc.sh CI gate also looks for, so the
// runtime and static gates agree on what "looks like an API key" means.
func TestSecretShapeReason_DetectsAPIKeyLiteral(t *testing.T) {
	cases := []string{
		`{"note":"sk-ant-abcdef0123456789abcdef"}`,
		`{"note":"sk-abcdefghij0123456789abcdef"}`,
	}
	for _, payload := range cases {
		if reason := SecretShapeReason([]byte(payload)); reason == "" {
			t.Errorf("SecretShapeReason(%q) = \"\", want a non-empty reason", payload)
		}
	}
}

// TestSecretShapeReason_DetectsCredentialFieldName proves the recursive
// field-name walk — including nested objects and arrays, the shape a
// real installed_mcp / org_config payload actually takes — catches a
// per-kind redaction bug that let a credential-named field with a real
// value slip through.
func TestSecretShapeReason_DetectsCredentialFieldName(t *testing.T) {
	cases := map[string]string{
		"top-level":        `{"api_key":"sk-live-1234567890"}`,
		"nested object":    `{"provider":{"password":"hunter2"}}`,
		"array of objects": `{"items":[{"id":"x"},{"id":"y","client_secret":"abc123"}]}`,
		"case-insensitive": `{"API_KEY":"abc123"}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if reason := SecretShapeReason([]byte(payload)); reason == "" {
				t.Errorf("SecretShapeReason(%q) = \"\", want a non-empty reason", payload)
			}
		})
	}
}

// TestSecretShapeReason_IgnoresLookalikeFieldNames is the precision
// counterpart to the detection tests: field names that merely CONTAIN
// "token"/"key"/"secret" as a substring, but are not themselves a
// credential field, must not trip the scanner — exact-match (not
// substring) is a deliberate design choice documented on
// secretFieldNames.
func TestSecretShapeReason_IgnoresLookalikeFieldNames(t *testing.T) {
	cases := map[string]string{
		"env var name, not a secret value": `{"tokenEnvVar":"GITHUB_TOKEN"}`,
		"client id, not a secret":          `{"client_id":"public-oauth-client-id"}`,
		"recipe id containing 'key'":       `{"id":"keyboard-shortcuts","displayName":"Keyboard"}`,
		"empty-string credential field":    `{"api_key":""}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if reason := SecretShapeReason([]byte(payload)); reason != "" {
				t.Errorf("SecretShapeReason(%q) = %q, want clean (\"\")", payload, reason)
			}
		})
	}
}

// TestSecretShapeReason_MalformedJSONDoesNotFalselyReject proves that a
// payload which merely fails to parse as JSON is not, by itself, treated
// as secret-shaped — that is the collector/applier's own decode error to
// raise, with a clearer message than "looks like a secret".
func TestSecretShapeReason_MalformedJSONDoesNotFalselyReject(t *testing.T) {
	if reason := SecretShapeReason([]byte(`not json at all`)); reason != "" {
		t.Errorf("SecretShapeReason(malformed JSON) = %q, want clean (decode errors are the caller's job)", reason)
	}
}
